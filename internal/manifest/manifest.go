package manifest

import (
	"database/sql"
	"fmt"

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
	rows, err := m.db.Query(`
		SELECT source_disk, source_path, dest_path, size, mtime_ns, sha256,
		       copied_at, COALESCE(verified_at, 0), status
		FROM files
		WHERE source_disk = ?
		ORDER BY source_path
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
