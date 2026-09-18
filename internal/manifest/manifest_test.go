package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eddyvarelae/media-vault/internal/testguard"
)

func TestMain(m *testing.M) {
	testguard.Require() // writes a temp manifest
	os.Exit(m.Run())
}

// B31: a read-only open cannot write a row, does not create a missing
// file, and the in-memory manifest has the schema and no rows.
func TestOpenReadOnlyAndEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.db")
	if _, err := OpenReadOnly(path); err == nil {
		t.Fatal("OpenReadOnly created or opened a manifest that does not exist")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("OpenReadOnly created the file")
	}
	rw, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	row := Entry{SourceDisk: "d", SourcePath: "a", DestPath: "a", Size: 1, MtimeNs: 1, SHA256: "x", CopiedAt: 1, Status: "copied"}
	if err := rw.Upsert(row); err != nil {
		t.Fatal(err)
	}
	rw.Close()

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if got, _ := ro.Lookup("d", "a"); got == nil || got.SHA256 != "x" {
		t.Errorf("read-only open cannot read the row: %+v", got)
	}
	for name, write := range map[string]func() error{
		"Upsert": func() error {
			return ro.Upsert(Entry{SourceDisk: "d", SourcePath: "b", DestPath: "b", Size: 1, MtimeNs: 1, SHA256: "y", CopiedAt: 1, Status: "copied"})
		},
		"MarkVerified":   func() error { return ro.MarkVerified("d", "a", 2) },
		"UpdateDestPath": func() error { return ro.UpdateDestPath("d", "a", "z") },
		"DeleteEntry":    func() error { return ro.DeleteEntry("d", "a") },
	} {
		if err := write(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "readonly") && !strings.Contains(strings.ToLower(err.Error()), "read-only") {
			t.Errorf("%s on a read-only manifest: err = %v, want a read-only refusal", name, err)
		}
	}

	empty, err := OpenEmpty()
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if rows, err := empty.ListByDisk("d"); err != nil || len(rows) != 0 {
		t.Errorf("empty manifest: %d rows, %v", len(rows), err)
	}
	if err := empty.Upsert(row); err != nil { // usable for a plan that records nothing on disk
		t.Errorf("empty manifest should accept rows in memory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "file::memory:")); err == nil {
		t.Errorf("in-memory manifest touched the disk")
	}
}
