package manifest

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS files (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  source_disk  TEXT    NOT NULL,
  source_path  TEXT    NOT NULL,
  dest_path    TEXT    NOT NULL,
  size         INTEGER NOT NULL,
  mtime_ns     INTEGER NOT NULL,
  sha256       TEXT    NOT NULL,
  copied_at    INTEGER NOT NULL,
  verified_at  INTEGER,
  status       TEXT    NOT NULL,
  UNIQUE(source_disk, source_path)
);

CREATE INDEX IF NOT EXISTS idx_files_dest   ON files(dest_path);
CREATE INDEX IF NOT EXISTS idx_files_status ON files(status);
CREATE INDEX IF NOT EXISTS idx_files_disk   ON files(source_disk);
-- Content dedup looks files up by size first (see VerifiedHashesForSize).
-- Without this the planner falls back to idx_files_status, which matches every
-- verified row and then filters, turning a per-file lookup into a full scan.
CREATE INDEX IF NOT EXISTS idx_files_size   ON files(size, status);

CREATE TABLE IF NOT EXISTS tags (
  source_disk TEXT    NOT NULL,
  source_path TEXT    NOT NULL,
  tag         TEXT    NOT NULL,
  created_at  INTEGER NOT NULL,
  PRIMARY KEY (source_disk, source_path, tag)
);

CREATE INDEX IF NOT EXISTS idx_tags_tag  ON tags(tag);
CREATE INDEX IF NOT EXISTS idx_tags_disk ON tags(source_disk);

CREATE TABLE IF NOT EXISTS metadata (
  source_disk TEXT    NOT NULL,
  source_path TEXT    NOT NULL,
  key         TEXT    NOT NULL,
  value       TEXT    NOT NULL,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (source_disk, source_path, key)
);

CREATE INDEX IF NOT EXISTS idx_metadata_disk ON metadata(source_disk);
CREATE INDEX IF NOT EXISTS idx_metadata_key  ON metadata(key);
`

type Manifest struct {
	db *sql.DB
}

type Entry struct {
	SourceDisk string
	SourcePath string
	DestPath   string
	Size       int64
	MtimeNs    int64
	SHA256     string
	CopiedAt   int64
	VerifiedAt int64
	Status     string
}

func Open(path string) (*Manifest, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// busy_timeout is critical — without it, a concurrent reader and
	// writer immediately collide with SQLITE_BUSY. With 30 s, contended
	// writes wait politely.
	if _, err := db.Exec("PRAGMA busy_timeout = 30000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("busy_timeout: %w", err)
	}
	// Try to upgrade to WAL if we're not already there. Mode change
	// requires exclusive DB access — if another writer beat us to it
	// (or already set WAL on a previous run), we tolerate that and
	// move on; the persistent setting in the DB file is what matters.
	var mode string
	_ = db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		_, _ = db.Exec("PRAGMA journal_mode = WAL")
	}
	_, _ = db.Exec("PRAGMA synchronous = NORMAL")

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Manifest{db: db}, nil
}

func (m *Manifest) Close() error { return m.db.Close() }

func (m *Manifest) Lookup(disk, sourcePath string) (*Entry, error) {
	row := m.db.QueryRow(`
		SELECT source_disk, source_path, dest_path, size, mtime_ns, sha256,
		       copied_at, COALESCE(verified_at, 0), status
		FROM files
		WHERE source_disk = ? AND source_path = ?
	`, disk, sourcePath)

	var e Entry
	err := row.Scan(&e.SourceDisk, &e.SourcePath, &e.DestPath, &e.Size, &e.MtimeNs,
		&e.SHA256, &e.CopiedAt, &e.VerifiedAt, &e.Status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// DuplicateGroup represents one set of files that share a sha256.
type DuplicateGroup struct {
	SHA256      string
	Size        int64
	Locations   []Location
	WastedBytes int64 // (count-1) * size
}

type Location struct {
	Disk string
	Path string
}

// FindDuplicates returns all sha256 groups that have 2+ entries.
// If minSize > 0, only groups with file size >= minSize are returned.
func (m *Manifest) FindDuplicates(minSize int64) ([]DuplicateGroup, error) {
	rows, err := m.db.Query(`
		SELECT sha256, size, COUNT(*) AS n
		FROM files
		WHERE size >= ?
		GROUP BY sha256
		HAVING n > 1
		ORDER BY size * (n - 1) DESC
	`, minSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []DuplicateGroup
	for rows.Next() {
		var g DuplicateGroup
		var n int64
		if err := rows.Scan(&g.SHA256, &g.Size, &n); err != nil {
			return nil, err
		}
		g.WastedBytes = g.Size * (n - 1)
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range groups {
		locs, err := m.locationsByHash(groups[i].SHA256)
		if err != nil {
			return nil, err
		}
		groups[i].Locations = locs
	}
	return groups, nil
}

// VerifiedBySize returns every VERIFIED file in the archive with exactly this
// size, keyed by sha256, across all source disks.
//
// Size is a free discriminator: a candidate whose size matches nothing here
// cannot be a duplicate of anything archived, so the caller can skip hashing
// it entirely. Only size-collisions need to be read.
//
// Deliberately verified-only. A `copied` row's sha256 attests to what the
// SOURCE held, not that the destination is still intact — skipping a copy
// against an unverified destination could drop the last good copy of a file.
// Callers should surface how many rows were eligible, so "no duplicates found"
// stays distinguishable from "nothing was eligible to compare against".
func (m *Manifest) VerifiedBySize(size int64) (map[string]Entry, error) {
	rows, err := m.db.Query(
		`select source_disk, source_path, dest_path, size, mtime_ns, sha256, verified_at
		   from files
		  where size = ? and status = 'verified' and sha256 != ''`, size)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]Entry)
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.SourceDisk, &e.SourcePath, &e.DestPath,
			&e.Size, &e.MtimeNs, &e.SHA256, &e.VerifiedAt); err != nil {
			return nil, err
		}
		e.Status = "verified"
		out[e.SHA256] = e
	}
	return out, rows.Err()
}

// CountVerifiedHashable reports how many rows are eligible to be matched
// against by VerifiedBySize. Zero means content dedup cannot possibly find
// anything, which is a very different message to the user than "no duplicates".
func (m *Manifest) CountVerifiedHashable() (int, error) {
	var n int
	err := m.db.QueryRow(
		`select count(*) from files where status = 'verified' and sha256 != ''`).Scan(&n)
	return n, err
}

func (m *Manifest) locationsByHash(sha string) ([]Location, error) {
	rows, err := m.db.Query(`
		SELECT source_disk, source_path FROM files WHERE sha256 = ? ORDER BY source_disk, source_path
	`, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Location
	for rows.Next() {
		var l Location
		if err := rows.Scan(&l.Disk, &l.Path); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteEntry removes a file row and any tags pointing at it. Used when a
// source file is recognized as a verified duplicate of one already at the
// destination, so the source is deleted and the manifest catches up.
func (m *Manifest) DeleteEntry(disk, path string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM tags WHERE source_disk = ? AND source_path = ?`, disk, path); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM files WHERE source_disk = ? AND source_path = ?`, disk, path); err != nil {
		return err
	}
	return tx.Commit()
}

// MoveEntry atomically rewrites a manifest row from (srcDisk, srcPath) to
// (dstDisk, dstPath), carrying tags along. Used after a host-level mv so the
// manifest reflects the new location.
func (m *Manifest) MoveEntry(srcDisk, srcPath, dstDisk, dstPath string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO files
		  (source_disk, source_path, dest_path, size, mtime_ns, sha256,
		   copied_at, verified_at, status)
		SELECT ?, ?, ?, size, mtime_ns, sha256,
		       copied_at, verified_at, status
		FROM files
		WHERE source_disk = ? AND source_path = ?
	`, dstDisk, dstPath, dstPath, srcDisk, srcPath); err != nil {
		return fmt.Errorf("insert dst row: %w", err)
	}

	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO tags (source_disk, source_path, tag, created_at)
		SELECT ?, ?, tag, created_at
		FROM tags
		WHERE source_disk = ? AND source_path = ?
	`, dstDisk, dstPath, srcDisk, srcPath); err != nil {
		return fmt.Errorf("copy tags: %w", err)
	}

	if _, err := tx.Exec(`
		DELETE FROM tags WHERE source_disk = ? AND source_path = ?
	`, srcDisk, srcPath); err != nil {
		return fmt.Errorf("delete src tags: %w", err)
	}

	if _, err := tx.Exec(`
		DELETE FROM files WHERE source_disk = ? AND source_path = ?
	`, srcDisk, srcPath); err != nil {
		return fmt.Errorf("delete src row: %w", err)
	}

	return tx.Commit()
}

// SetMetadata upserts a (key, value) pair against a manifest file.
func (m *Manifest) SetMetadata(disk, path, key, value string) error {
	_, err := m.db.Exec(`
		INSERT INTO metadata (source_disk, source_path, key, value, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source_disk, source_path, key) DO UPDATE SET
		  value      = excluded.value,
		  updated_at = excluded.updated_at
	`, disk, path, key, value, time.Now().UnixNano())
	return err
}

// FindByBasename returns manifest entries within `disk` whose source_path
// ends in `basename` (i.e., last path segment matches). Used to look up files
// by friendly name when ingesting external metadata.
func (m *Manifest) FindByBasename(disk, basename string) ([]Entry, error) {
	rows, err := m.db.Query(`
		SELECT source_disk, source_path, dest_path, size, mtime_ns, sha256,
		       copied_at, COALESCE(verified_at, 0), status
		FROM files
		WHERE source_disk = ? AND (source_path = ? OR source_path LIKE ?)
	`, disk, basename, "%/"+basename)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.SourceDisk, &e.SourcePath, &e.DestPath, &e.Size,
			&e.MtimeNs, &e.SHA256, &e.CopiedAt, &e.VerifiedAt, &e.Status); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ApplyTag adds `tag` to every file in `disk` whose source_path matches the
// SQL LIKE `pattern` (use `%` as the wildcard). Returns count of newly tagged
// files (already-tagged files are no-ops via INSERT OR IGNORE).
func (m *Manifest) ApplyTag(disk, pattern, tag string) (int64, error) {
	now := time.Now().UnixNano()
	res, err := m.db.Exec(`
		INSERT OR IGNORE INTO tags (source_disk, source_path, tag, created_at)
		SELECT source_disk, source_path, ?, ?
		FROM files
		WHERE source_disk = ? AND source_path LIKE ?
	`, tag, now, disk, pattern)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RemoveTag deletes a tag from files in `disk` matching `pattern`.
func (m *Manifest) RemoveTag(disk, pattern, tag string) (int64, error) {
	res, err := m.db.Exec(`
		DELETE FROM tags
		WHERE source_disk = ? AND source_path LIKE ? AND tag = ?
	`, disk, pattern, tag)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// FilesWithTag returns every file carrying `tag`, joined with manifest data.
func (m *Manifest) FilesWithTag(tag string) ([]Entry, error) {
	rows, err := m.db.Query(`
		SELECT f.source_disk, f.source_path, f.dest_path, f.size, f.mtime_ns,
		       f.sha256, f.copied_at, COALESCE(f.verified_at, 0), f.status
		FROM files f
		JOIN tags t ON t.source_disk = f.source_disk AND t.source_path = f.source_path
		WHERE t.tag = ?
		ORDER BY f.source_disk, f.source_path
	`, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.SourceDisk, &e.SourcePath, &e.DestPath, &e.Size,
			&e.MtimeNs, &e.SHA256, &e.CopiedAt, &e.VerifiedAt, &e.Status); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TagsFor returns the list of tags attached to a given (disk, path).
func (m *Manifest) TagsFor(disk, path string) ([]string, error) {
	rows, err := m.db.Query(`
		SELECT tag FROM tags WHERE source_disk = ? AND source_path = ?
		ORDER BY tag
	`, disk, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FindUniqueIn returns files in `disk` whose sha256 does not appear under any
// other disk in the manifest. Used to answer "what's only in #recycle?".
func (m *Manifest) FindUniqueIn(disk string) ([]Entry, error) {
	rows, err := m.db.Query(`
		SELECT source_disk, source_path, dest_path, size, mtime_ns, sha256,
		       copied_at, COALESCE(verified_at, 0), status
		FROM files f
		WHERE source_disk = ?
		  AND NOT EXISTS (
		    SELECT 1 FROM files g
		    WHERE g.sha256 = f.sha256 AND g.source_disk != f.source_disk
		  )
		ORDER BY size DESC
	`, disk)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.SourceDisk, &e.SourcePath, &e.DestPath, &e.Size,
			&e.MtimeNs, &e.SHA256, &e.CopiedAt, &e.VerifiedAt, &e.Status); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (m *Manifest) ListByDisk(disk string) ([]Entry, error) {
	return m.listByDisk(disk, false)
}

// ListByDiskUnverified returns only the rows for `disk` that are not yet
// verified.
//
// Deliberately a negative predicate rather than an allow-list of statuses. The
// vocabulary is copied / verified / mismatch / deduped / inventoried, and a
// `mismatch` row in particular must be re-checked — a later recopy may have
// fixed it, and excluding it would strand a row that can never be promoted.
// `!= 'verified'` gets that right by construction and needs no maintenance
// when a status is added.
func (m *Manifest) ListByDiskUnverified(disk string) ([]Entry, error) {
	return m.listByDisk(disk, true)
}

// CountVerifiedInDisk reports how many rows an incremental pass would skip, and
// the newest verified_at among them. That timestamp is the newest single row
// verification, not a full-sweep date: one incremental pass promoting one row
// moves it, while every other row on the disk stays as old as it was.
func (m *Manifest) CountVerifiedInDisk(disk string) (n int, newestVerifiedAt int64, err error) {
	err = m.db.QueryRow(`
		SELECT COUNT(*), COALESCE(MAX(verified_at), 0)
		FROM files WHERE source_disk = ? AND status = 'verified'
	`, disk).Scan(&n, &newestVerifiedAt)
	return
}

func (m *Manifest) listByDisk(disk string, onlyUnverified bool) ([]Entry, error) {
	q := `
		SELECT source_disk, source_path, dest_path, size, mtime_ns, sha256,
		       copied_at, COALESCE(verified_at, 0), status
		FROM files
		WHERE source_disk = ?`
	if onlyUnverified {
		// Filtered in SQL, not in Go: idx_files_disk covers the disk lookup
		// and the archive can hold tens of thousands of rows per disk.
		q += ` AND status != 'verified'`
	}
	q += `
		ORDER BY source_path`
	rows, err := m.db.Query(q, disk)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.SourceDisk, &e.SourcePath, &e.DestPath, &e.Size,
			&e.MtimeNs, &e.SHA256, &e.CopiedAt, &e.VerifiedAt, &e.Status); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (m *Manifest) MarkVerified(disk, sourcePath string, verifiedAt int64) error {
	_, err := m.db.Exec(`
		UPDATE files SET verified_at = ?, status = 'verified'
		WHERE source_disk = ? AND source_path = ?
	`, verifiedAt, disk, sourcePath)
	return err
}

func (m *Manifest) MarkMismatch(disk, sourcePath string, verifiedAt int64) error {
	_, err := m.db.Exec(`
		UPDATE files SET verified_at = ?, status = 'mismatch'
		WHERE source_disk = ? AND source_path = ?
	`, verifiedAt, disk, sourcePath)
	return err
}

func (m *Manifest) Upsert(e Entry) error {
	_, err := m.db.Exec(`
		INSERT INTO files
		  (source_disk, source_path, dest_path, size, mtime_ns, sha256,
		   copied_at, verified_at, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, 0), ?)
		ON CONFLICT(source_disk, source_path) DO UPDATE SET
		  dest_path   = excluded.dest_path,
		  size        = excluded.size,
		  mtime_ns    = excluded.mtime_ns,
		  sha256      = excluded.sha256,
		  copied_at   = excluded.copied_at,
		  verified_at = excluded.verified_at,
		  status      = excluded.status
	`, e.SourceDisk, e.SourcePath, e.DestPath, e.Size, e.MtimeNs,
		e.SHA256, e.CopiedAt, e.VerifiedAt, e.Status)
	return err
}
