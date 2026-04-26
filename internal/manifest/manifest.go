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
