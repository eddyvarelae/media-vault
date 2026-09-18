package gap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/eddyvarelae/media-vault/internal/manifest"
	"github.com/eddyvarelae/media-vault/internal/testguard"
)

func TestMain(m *testing.M) {
	testguard.Require() // never write fixtures under /volume1 or /mnt
	os.Exit(m.Run())
}

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The content rule, per folder, with the arithmetic checked: a verified
// row's bytes are archived whatever they are named or which disk they came
// from; a copied row's are not; same size with other bytes is absent; a
// unique size is absent without being read.
func TestRunMatchesByContentOnly(t *testing.T) {
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	row := func(disk, src, content, status string) {
		t.Helper()
		if err := m.Upsert(manifest.Entry{SourceDisk: disk, SourcePath: src, DestPath: src, Size: int64(len(content)),
			MtimeNs: 1, SHA256: sha(content), CopiedAt: 1, Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	row("media-sonya6700", "DCIM/DSC1.ARW", "archived raw", "verified")
	row("media-gopro", "Videos/GX1.MP4", "archived clip", "verified")
	row("media-sonya6700", "DCIM/DSC2.ARW", "copied only!", "copied") // same length as "same size!!!" below
	row("media-backup", "x.bin", "twelve bytes", "verified")

	root := t.TempDir()
	write(t, filepath.Join(root, "SonyA6700", "DCIM", "DSC1.ARW"), "archived raw")  // same name, same bytes
	write(t, filepath.Join(root, "SonyA6700", "DCIM", "DSC9.ARW"), "archived clip") // renamed, other folder: still archived by content
	write(t, filepath.Join(root, "SonyA6700", "DCIM", "DSC2.ARW"), "copied only!")  // only a copied row has it: absent
	write(t, filepath.Join(root, "SonyA6700", "DCIM", "DSC3.ARW"), "same size!!!")  // 12 bytes like x.bin: hashed, absent
	write(t, filepath.Join(root, "Backup", "big.mov"), "a size nobody has")         // unique size: absent, never read
	write(t, filepath.Join(root, "loose.txt"), "archived raw")                      // at the root: folder "."
	write(t, filepath.Join(root, "SonyA6700", ".DS_Store"), "junk")
	write(t, filepath.Join(root, "SonyA6700", "DCIM", "._DSC1.ARW"), "apple double")
	write(t, filepath.Join(root, "GoPro", "reports", "r.json"), "tagger output")
	write(t, filepath.Join(root, "#recycle", "gone.mov"), "trash")
	if err := os.Symlink(filepath.Join(root, "Backup", "big.mov"), filepath.Join(root, "Backup", "link.mov")); err != nil {
		t.Fatal(err)
	}

	r, err := Run(context.Background(), m, root)
	if err != nil {
		t.Fatal(err)
	}
	if r.VerifiedRows != 3 {
		t.Errorf("VerifiedRows = %d, want 3", r.VerifiedRows)
	}
	want := map[string]Folder{
		".":         {Name: ".", Files: 1, Bytes: 12, Archived: 1, ArchivedBytes: 12},
		"Backup":    {Name: "Backup", Files: 1, Bytes: 17, Absent: 1, AbsentBytes: 17},
		"SonyA6700": {Name: "SonyA6700", Files: 4, Bytes: 49, Archived: 2, ArchivedBytes: 25, Absent: 2, AbsentBytes: 24},
	}
	if len(r.Folders) != len(want) {
		t.Fatalf("folders = %+v", r.Folders)
	}
	for _, f := range r.Folders {
		if f != want[f.Name] {
			t.Errorf("folder %s = %+v, want %+v", f.Name, f, want[f.Name])
		}
	}
	if r.Files != 6 || r.Bytes != 78 || r.Archived != 3 || r.ArchivedBytes != 37 || r.Absent != 3 || r.AbsentBytes != 41 {
		t.Errorf("totals = files %d bytes %d archived %d/%d absent %d/%d", r.Files, r.Bytes, r.Archived, r.ArchivedBytes, r.Absent, r.AbsentBytes)
	}
	if r.Archived+r.Absent != r.Files || r.ArchivedBytes+r.AbsentBytes != r.Bytes {
		t.Errorf("arithmetic does not add up: %+v", r)
	}
	// Hashed: every file whose size (12 or 13) some verified row has - the
	// three archived ones plus DSC2 (12) and DSC3 (12); big.mov (17) never.
	if r.Hashed != 5 || r.HashedBytes != 12*4+13 {
		t.Errorf("hashed = %d files / %d bytes, want 5 / 61", r.Hashed, r.HashedBytes)
	}
	absent := map[string]File{}
	for _, f := range r.AbsentFiles {
		absent[f.Rel] = f
	}
	if len(absent) != 3 || absent["Backup/big.mov"].SHA256 != "" || absent["SonyA6700/DCIM/DSC2.ARW"].SHA256 != sha("copied only!") || absent["SonyA6700/DCIM/DSC3.ARW"].Size != 12 {
		t.Errorf("absent files = %+v", r.AbsentFiles)
	}
	// Nothing was written anywhere under the root.
	if _, err := os.Stat(filepath.Join(root, "Backup", "big.mov.vault-partial")); err == nil {
		t.Errorf("gap wrote a file")
	}
}

func TestRunEmptyArchive(t *testing.T) {
	m, err := manifest.OpenEmpty()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	root := t.TempDir()
	write(t, filepath.Join(root, "A", "x.mov"), "x")
	r, err := Run(context.Background(), m, root)
	if err != nil || r.Files != 1 || r.Absent != 1 || r.Hashed != 0 {
		t.Errorf("empty archive: %+v, %v", r, err)
	}
}
