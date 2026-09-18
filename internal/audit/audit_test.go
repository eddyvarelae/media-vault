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
	cases := []struct {
		name string
		size int64
		tail []byte
		want Verdict
	}{
		{"DCIM/a.JPG", 7_000_000, append([]byte("imagedata"), eoi...), Plausible},             // EOI present
		{"DCIM/pad.jpg", 7_000_000, append(append([]byte("x"), eoi...), zeros...), Plausible}, // EOI then zero padding (DJI)
		{"DCIM/torn.JPG", 7_285_047, zeros, Suspect},                                          // no EOI in the tail
		{"DCIM/empty.jpg", 0, nil, Suspect},                                                   // zero-length image
		{"CLIP/x.ARW", 28 << 20, zeros, Plausible},                                            // exact MiB multiple
		{"CLIP/short.arw", (28 << 20) + 1, zeros, Review},                                     // off the MiB signature
		{"CLIP/C1431.RSV", 128 << 20, zeros, Plausible},                                       // Sony reserve
		{"Backup/PRIVATE/DATABASE/DATABASE.BIN", 9_670_656, zeros, Plausible},                 // Sony DB, zero-padded
		{"FlightLogs/x.SRT", 0, nil, Plausible},                                               // empty SRT twin
		{"FlightLogs/notes.srt", 40, []byte("00:00"), Skipped},                                // non-empty SRT text
		{"Videos/clip.MP4", 1_000_000, zeros, Skipped},                                        // type not audited
	}
	for _, c := range cases {
		got, reason := Classify(c.name, c.size, c.tail)
		if got != c.want {
			t.Errorf("Classify(%q, %d) = %s (%s), want %s", c.name, c.size, got, reason, c.want)
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
	write("Logs/empty.SRT", nil)                     // empty SRT → PLAUSIBLE
	write("Logs/notes.SRT", []byte("subtitle"))      // SRT text → SKIPPED
	write("Videos/clip.MP4", []byte("moov"))         // unknown → SKIPPED
	// and one row whose file is missing → ERROR

	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	rels := []string{"DCIM/good.JPG", "DCIM/torn.JPG", "CLIP/raw.ARW", "CLIP/short.ARW",
		"CLIP/C1.RSV", "Backup/DATABASE.BIN", "Logs/empty.SRT", "Logs/notes.SRT", "Videos/clip.MP4", "DCIM/missing.JPG"}
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
	if r.Rows != 10 {
		t.Errorf("rows = %d, want 10", r.Rows)
	}
	if r.Suspect != 1 || len(suspects) != 1 || suspects[0] != "DCIM/torn.JPG" {
		t.Errorf("suspect = %d %v, want 1 [DCIM/torn.JPG]", r.Suspect, suspects)
	}
	if r.Review != 1 {
		t.Errorf("review = %d, want 1 (short.ARW)", r.Review)
	}
	if r.Errors != 1 {
		t.Errorf("errors = %d, want 1 (missing file)", r.Errors)
	}
	if r.Skipped != 2 {
		t.Errorf("skipped = %d, want 2 (notes.SRT, clip.MP4)", r.Skipped)
	}
	if r.Plausible != 5 { // good.JPG, raw.ARW, C1.RSV, DATABASE.BIN, empty.SRT
		t.Errorf("plausible = %d, want 5", r.Plausible)
	}
	// Findings list every non-plausible, non-skipped row (suspect + review + error).
	if len(r.Findings) != r.Suspect+r.Review+r.Errors {
		t.Errorf("findings %d != suspect+review+error %d", len(r.Findings), r.Suspect+r.Review+r.Errors)
	}
	// Report-only: nothing under root was modified.
	if got, _ := os.ReadFile(filepath.Join(root, "DCIM/torn.JPG")); !bytes.Equal(got, make([]byte, 4096)) {
		t.Errorf("audit modified a file")
	}
}
