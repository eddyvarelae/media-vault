package audit

import (
	"bytes"
	"context"
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

// Classify is pure, so the per-type rules are pinned on bytes alone.
func TestClassify(t *testing.T) {
	eoi := []byte{0xFF, 0xD9}
	zeros := make([]byte, 1024)
	eoiThenZeros := append(append([]byte("x"), eoi...), zeros...)
	eoiThenJunk := append(append([]byte("x"), eoi...), []byte("more")...)
	cases := []struct {
		name string
		size int64
		tail []byte
		twin bool
		want Verdict
	}{
		{"DCIM/a.JPG", 7_000_000, append([]byte("imagedata"), eoi...), false, Plausible}, // EOI at EOF
		{"DCIM/pad.jpg", 7_000_000, eoiThenZeros, false, Plausible},                      // EOI then only zeros
		{"DCIM/junk.jpg", 7_000_000, eoiThenJunk, false, Suspect},                        // EOI then non-zero data
		{"DCIM/torn.JPG", 7_285_047, zeros, false, Suspect},                              // no EOI in the tail
		{"DCIM/empty.jpg", 0, nil, false, Suspect},                                       // zero-length image
		{"CLIP/x.ARW", 28 << 20, zeros, false, Plausible},                                // exact MiB multiple
		{"CLIP/short.arw", (28 << 20) + 1, zeros, false, Review},                         // off the MiB signature
		{"CLIP/C1431.RSV", 128 << 20, zeros, false, Plausible},                           // Sony reserve
		{"Backup/DATABASE.BIN", 9_670_656, zeros, false, Plausible},                      // Sony DB, zero-padded
		{"Logs/twin.SRT", 0, nil, true, Plausible},                                       // empty SRT WITH a media twin
		{"Logs/orphan.SRT", 0, nil, false, Review},                                       // empty SRT, no twin
		{"Logs/notes.srt", 40, []byte("00:00"), false, Skipped},                          // non-empty SRT text
		{"Videos/clip.MP4", 1_000_000, zeros, false, Skipped},                            // type not audited
	}
	for _, c := range cases {
		got, reason := Classify(c.name, c.size, c.tail, c.twin)
		if got != c.want {
			t.Errorf("Classify(%q, %d, twin=%v) = %s (%s), want %s", c.name, c.size, c.twin, got, reason, c.want)
		}
	}
}

func TestRun(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, b []byte) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	eoi := []byte{0xFF, 0xD9}
	write("DCIM/good.JPG", append([]byte("photo"), eoi...))
	write("DCIM/torn.JPG", make([]byte, 4096))       // all zeros, no EOI → SUSPECT
	write("CLIP/raw.ARW", make([]byte, 1<<20))       // 1 MiB exactly → PLAUSIBLE
	write("CLIP/short.ARW", make([]byte, (1<<20)+1)) // off MiB → REVIEW
	write("CLIP/C1.RSV", make([]byte, 4096))         // reserve → PLAUSIBLE
	write("Backup/DATABASE.BIN", make([]byte, 4096)) // Sony DB → PLAUSIBLE
	write("Logs/empty.SRT", nil)                     // empty SRT WITH twin → PLAUSIBLE
	write("Logs/empty.MP4", []byte("moov"))          // the twin (a video row) → SKIPPED
	write("Logs/orphan.SRT", nil)                    // empty SRT, no twin → REVIEW
	write("Videos/clip.MP4", []byte("moov"))         // unknown → SKIPPED
	// and one row whose file is missing → ERROR

	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	rels := []string{"DCIM/good.JPG", "DCIM/torn.JPG", "CLIP/raw.ARW", "CLIP/short.ARW",
		"CLIP/C1.RSV", "Backup/DATABASE.BIN", "Logs/empty.SRT", "Logs/empty.MP4",
		"Logs/orphan.SRT", "Videos/clip.MP4", "DCIM/missing.JPG"}
	for _, rel := range rels {
		if err := m.Upsert(manifest.Entry{SourceDisk: "cam", SourcePath: rel, DestPath: rel,
			Size: 1, MtimeNs: 1, SHA256: "deadbeef", CopiedAt: 1, Status: "verified"}); err != nil {
			t.Fatal(err)
		}
	}

	var suspects []string
	r, err := Run(context.Background(), m, "cam", root, func(f Finding) {
		if f.Verdict == Suspect {
			suspects = append(suspects, f.Rel)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != 11 {
		t.Errorf("rows = %d, want 11", r.Rows)
	}
	if r.Suspect != 1 || len(suspects) != 1 || suspects[0] != "DCIM/torn.JPG" {
		t.Errorf("suspect = %d %v, want 1 [DCIM/torn.JPG]", r.Suspect, suspects)
	}
	if r.Review != 2 { // short.ARW, orphan.SRT
		t.Errorf("review = %d, want 2", r.Review)
	}
	if r.Errors != 1 { // missing file
		t.Errorf("errors = %d, want 1", r.Errors)
	}
	if r.Skipped != 2 || r.SkippedExt[".mp4"] != 2 { // empty.MP4, clip.MP4
		t.Errorf("skipped = %d %v, want 2 with .mp4:2", r.Skipped, r.SkippedExt)
	}
	if r.Plausible != 5 { // good.JPG, raw.ARW, C1.RSV, DATABASE.BIN, empty.SRT(twin)
		t.Errorf("plausible = %d, want 5", r.Plausible)
	}
	if len(r.Findings) != r.Suspect+r.Review+r.Errors {
		t.Errorf("findings %d != suspect+review+error %d", len(r.Findings), r.Suspect+r.Review+r.Errors)
	}
	// Report-only: nothing under root was modified.
	if got, _ := os.ReadFile(filepath.Join(root, "DCIM/torn.JPG")); !bytes.Equal(got, make([]byte, 4096)) {
		t.Errorf("audit modified a file")
	}
}

// A symlinked file is refused by the O_NOFOLLOW open, reported ERROR, not read
// through.
func TestRunRefusesSymlinkLeaf(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(t.TempDir(), "real.JPG")
	if err := os.WriteFile(real, append([]byte("x"), 0xFF, 0xD9), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "link.JPG")); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Upsert(manifest.Entry{SourceDisk: "cam", SourcePath: "link.JPG", DestPath: "link.JPG",
		Size: 1, MtimeNs: 1, SHA256: "x", CopiedAt: 1, Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), m, "cam", root, func(Finding) {})
	if err != nil {
		t.Fatal(err)
	}
	if r.Errors != 1 || r.Plausible != 0 {
		t.Errorf("symlink leaf: errors=%d plausible=%d, want 1 error, 0 plausible", r.Errors, r.Plausible)
	}
}
