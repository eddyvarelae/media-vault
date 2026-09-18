package audit

import (
	"bytes"
	"context"
	"io"
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

// TestReadTailRefusesOpenSubstitution is the open-substitution regression: the
// path being a symlink is refused by O_NOFOLLOW, and a file swapped for a
// different inode between Lstat and open is refused by SameFile.
func TestReadTailRefusesOpenSubstitution(t *testing.T) {
	dir := t.TempDir()
	// (a) the leaf is a symlink → O_NOFOLLOW open fails.
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	li, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTail(link, li); err == nil {
		t.Errorf("readTail followed a symlink leaf; O_NOFOLLOW should refuse it")
	}
	// (b) the file at the path is swapped for a different inode after Lstat.
	// Rename the original aside (never Remove): its inode stays live under the
	// new name, so the file written back at the path is guaranteed a fresh
	// inode. A remove+create can reuse the freed inode on Linux (it did in the
	// first alpine CI run), let SameFile pass, and make the test lie.
	swap := filepath.Join(dir, "swap")
	if err := os.WriteFile(swap, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := os.Lstat(swap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(swap, swap+".aside"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(swap, []byte("second"), 0o644); err != nil { // fresh inode
		t.Fatal(err)
	}
	if _, _, err := readTail(swap, first); err == nil {
		t.Errorf("readTail read a file that was swapped after Lstat; SameFile should refuse it")
	}
}

// A ReaderAt that returns one byte short of any request, to exercise the
// truncation short-read path deterministically.
type shortReaderAt struct{}

func (shortReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, io.EOF // a file that shrank mid-read
}

func TestTailFromShortRead(t *testing.T) {
	if _, err := tailFrom(shortReaderAt{}, 100); err == nil {
		t.Errorf("tailFrom accepted a short read; a truncated file must be an error")
	}
	// A full ReaderAt returns the tail.
	data := bytes.Repeat([]byte{0xAB}, 10)
	got, err := tailFrom(bytes.NewReader(data), int64(len(data)))
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("tailFrom of a whole reader: %v, %v", got, err)
	}
}

// TestRunTwinNeedsAValidMediaFile is the twin regression: an empty .SRT is a
// twin only of media that actually resolves to a regular file under the root —
// a mere manifest row for a missing video does not make it PLAUSIBLE.
func TestRunTwinNeedsAValidMediaFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.SRT"), nil, 0o644); err != nil { // empty SRT
		t.Fatal(err)
	}
	// x.MP4 has a row but no file on disk → not a valid twin.
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for _, rel := range []string{"x.SRT", "x.MP4"} {
		if err := m.Upsert(manifest.Entry{SourceDisk: "cam", SourcePath: rel, DestPath: rel,
			Size: 1, MtimeNs: 1, SHA256: "x", CopiedAt: 1, Status: "verified"}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Run(context.Background(), m, "cam", root, func(Finding) {})
	if err != nil {
		t.Fatal(err)
	}
	if r.Review != 1 { // x.SRT: empty, no VALID twin → REVIEW
		t.Errorf("empty SRT with only a missing-file video row: review=%d, want 1", r.Review)
	}
	if r.Plausible != 0 {
		t.Errorf("nothing should be plausible: plausible=%d", r.Plausible)
	}
	if r.Errors != 1 { // x.MP4 file missing → ERROR
		t.Errorf("missing video file: errors=%d, want 1", r.Errors)
	}
}

// TestRunRefusesLeafSubstitution is the open-substitution regression THROUGH
// Run: a hook swaps the leaf for a different inode after resolve's Lstat and
// before the O_NOFOLLOW open, so SameFile refuses it and the row is an ERROR —
// never read through the substituted file.
func TestRunRefusesLeafSubstitution(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "DCIM", "x.JPG")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("photo"), 0xFF, 0xD9), 0o644); err != nil {
		t.Fatal(err)
	}
	hookBeforeOpen = func(p string) {
		// Rename the original aside (never Remove): its inode stays live under
		// the new name, so the fresh file written at p is GUARANTEED a different
		// inode — a plain remove+create could reuse the freed inode and let
		// SameFile pass, making the test lie.
		if err := os.Rename(p, p+".aside"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, append([]byte("other"), 0xFF, 0xD9), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { hookBeforeOpen = nil }()

	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Upsert(manifest.Entry{SourceDisk: "cam", SourcePath: "DCIM/x.JPG", DestPath: "DCIM/x.JPG",
		Size: 1, MtimeNs: 1, SHA256: "x", CopiedAt: 1, Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), m, "cam", root, func(Finding) {})
	if err != nil {
		t.Fatal(err)
	}
	if r.Errors != 1 || r.Plausible != 0 {
		t.Errorf("leaf substituted after stat: errors=%d plausible=%d, want 1 error, 0 plausible", r.Errors, r.Plausible)
	}
}

// TestRunTruncatedAfterStat is the truncation regression THROUGH Run: a hook
// truncates the file after its size is taken from the fd and before the tail
// ReadAt, so the read comes up short and the row is an ERROR — audit never
// judges a file on a partial tail.
func TestRunTruncatedAfterStat(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.JPG")
	if err := os.WriteFile(path, append(bytes.Repeat([]byte("d"), 4096), 0xFF, 0xD9), 0o644); err != nil {
		t.Fatal(err)
	}
	hookAfterStat = func(p string) { // shrink the file to nothing before ReadAt
		if err := os.Truncate(p, 0); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { hookAfterStat = nil }()

	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Upsert(manifest.Entry{SourceDisk: "cam", SourcePath: "big.JPG", DestPath: "big.JPG",
		Size: 1, MtimeNs: 1, SHA256: "x", CopiedAt: 1, Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), m, "cam", root, func(Finding) {})
	if err != nil {
		t.Fatal(err)
	}
	if r.Errors != 1 || r.Plausible != 0 {
		t.Errorf("truncated after stat: errors=%d plausible=%d, want 1 error, 0 plausible", r.Errors, r.Plausible)
	}
}

// TestRunTwinSymlinkedIsNotATwin: an empty .SRT's media twin present only as a
// symlink is not a valid twin (resolve refuses a non-regular leaf), so the SRT
// is REVIEW, not PLAUSIBLE.
func TestRunTwinSymlinkedIsNotATwin(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.SRT"), nil, 0o644); err != nil { // empty SRT
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "real.MP4")
	if err := os.WriteFile(real, []byte("moov"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "x.MP4")); err != nil { // twin is a symlink
		t.Fatal(err)
	}
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for _, rel := range []string{"x.SRT", "x.MP4"} {
		if err := m.Upsert(manifest.Entry{SourceDisk: "cam", SourcePath: rel, DestPath: rel,
			Size: 1, MtimeNs: 1, SHA256: "x", CopiedAt: 1, Status: "verified"}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Run(context.Background(), m, "cam", root, func(Finding) {})
	if err != nil {
		t.Fatal(err)
	}
	if r.Review != 1 { // x.SRT: empty, twin is a symlink → not valid → REVIEW
		t.Errorf("empty SRT with a symlinked twin: review=%d, want 1", r.Review)
	}
	if r.Plausible != 0 {
		t.Errorf("nothing should be plausible: plausible=%d", r.Plausible)
	}
	if r.Errors != 1 { // x.MP4 leaf is a symlink → not a regular file → ERROR
		t.Errorf("symlinked twin file: errors=%d, want 1", r.Errors)
	}
}
