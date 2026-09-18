package repair

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/eddyvarelae/media-vault/internal/manifest"
	"github.com/eddyvarelae/media-vault/internal/testguard"
)

func TestMain(m *testing.M) {
	testguard.Require() // never write fixtures under /volume1 or /mnt
	os.Exit(m.Run())
}

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// row inserts a `copied` row for disk "sony" pointing at dest with the hash
// and size of content - the state the 195 B24 rows are in.
func row(t *testing.T, m *manifest.Manifest, src, dest, content string) manifest.Entry {
	t.Helper()
	e := manifest.Entry{SourceDisk: "sony", SourcePath: src, DestPath: dest,
		Size: int64(len(content)), MtimeNs: 1, SHA256: sha(content), CopiedAt: 2, Status: "copied"}
	if err := m.Upsert(e); err != nil {
		t.Fatal(err)
	}
	return e
}

func rowsOf(t *testing.T, m *manifest.Manifest, disk string) map[string]manifest.Entry {
	t.Helper()
	rows, err := m.ListByDisk(disk)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]manifest.Entry{}
	for _, e := range rows {
		out[e.SourcePath] = e
	}
	return out
}

func outcomes(p *Plan) map[string]Change {
	out := map[string]Change{}
	for _, c := range p.Changes {
		out[c.Row.SourcePath] = c
	}
	return out
}

func TestBuildFindsOnlyHashBackedCandidatesOneLevelDown(t *testing.T) {
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	root := t.TempDir()

	// The B24 shape: rows say `X`, files are at `CLIP/X` and `DCIM/X`.
	write(t, filepath.Join(root, "CLIP", "C0001.XML"), "clip metadata")
	row(t, m, "CLIP/C0001.XML", "C0001.XML", "clip metadata")
	write(t, filepath.Join(root, "DCIM", "DSC0001.JPG"), "a photo")
	row(t, m, "DCIM/DSC0001.JPG", "DSC0001.JPG", "a photo")
	// Same basename, same size, different bytes: hashed, rejected.
	write(t, filepath.Join(root, "DCIM", "DSC0002.JPG"), "photo TWO")
	row(t, m, "DCIM/DSC0002.JPG", "DSC0002.JPG", "photo two")
	// Same basename, different size: rejected without reading.
	write(t, filepath.Join(root, "DCIM", "DSC0003.JPG"), "a much longer photo")
	row(t, m, "DCIM/DSC0003.JPG", "DSC0003.JPG", "short")
	// The right bytes in two subdirectories: ambiguous, not chosen.
	write(t, filepath.Join(root, "CLIP", "DUP.MP4"), "same clip")
	write(t, filepath.Join(root, "DCIM", "DUP.MP4"), "same clip")
	row(t, m, "DUP.MP4", "DUP.MP4", "same clip")
	// Two levels down is out of scope.
	write(t, filepath.Join(root, "DCIM", "100MSDCF", "DEEP.ARW"), "deep")
	row(t, m, "DEEP.ARW", "DEEP.ARW", "deep")
	// A row whose directory is itself gone.
	row(t, m, "gone/G.MOV", "gone/G.MOV", "gone")
	// A nested row: its dir exists, the file is one below it.
	write(t, filepath.Join(root, "Videos", "2024", "V.MP4"), "video")
	row(t, m, "V.MP4", "Videos/V.MP4", "video")
	// Intact row and an inventoried row: untouched, counted.
	write(t, filepath.Join(root, "OK.MOV"), "fine")
	row(t, m, "OK.MOV", "OK.MOV", "fine")
	if err := m.Upsert(manifest.Entry{SourceDisk: "sony", SourcePath: "inv.txt", Size: 1, SHA256: "x", CopiedAt: 1, Status: "inventoried"}); err != nil {
		t.Fatal(err)
	}
	// Another disk with the same broken shape must not be touched.
	write(t, filepath.Join(root, "CLIP", "OTHER.XML"), "other disk")
	if err := m.Upsert(manifest.Entry{SourceDisk: "zve", SourcePath: "CLIP/OTHER.XML", DestPath: "OTHER.XML",
		Size: 10, MtimeNs: 1, SHA256: sha("other disk"), CopiedAt: 2, Status: "copied"}); err != nil {
		t.Fatal(err)
	}
	before := rowsOf(t, m, "sony")
	otherBefore := rowsOf(t, m, "zve")

	p, err := Build(context.Background(), m, "sony", root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Checked != 9 || p.Intact != 1 || p.NoDest != 1 || len(p.Changes) != 8 {
		t.Errorf("plan = checked %d intact %d nodest %d changes %d; want 9/1/1/8", p.Checked, p.Intact, p.NoDest, len(p.Changes))
	}
	want := map[string]struct {
		outcome Outcome
		dest    string
	}{
		"CLIP/C0001.XML":   {Repairable, "CLIP/C0001.XML"},
		"DCIM/DSC0001.JPG": {Repairable, "DCIM/DSC0001.JPG"},
		"DCIM/DSC0002.JPG": {NotFound, ""},
		"DCIM/DSC0003.JPG": {NotFound, ""},
		"DUP.MP4":          {Ambiguous, ""},
		"DEEP.ARW":         {NotFound, ""},
		"gone/G.MOV":       {NotFound, ""},
		"V.MP4":            {Repairable, "Videos/2024/V.MP4"},
	}
	got := outcomes(p)
	for src, w := range want {
		c, ok := got[src]
		if !ok {
			t.Errorf("no change for %s", src)
			continue
		}
		if c.Outcome != w.outcome || c.NewDest != w.dest {
			t.Errorf("%s: %s → %q, want %s → %q", src, c.Outcome, c.NewDest, w.outcome, w.dest)
		}
	}
	if c := got["DUP.MP4"]; len(c.Candidates) != 2 {
		t.Errorf("DUP.MP4 candidates = %v, want both", c.Candidates)
	}
	// Hashed: the two repairable at root level (13+7), DSC0002 (9), both DUP (9+9), V (5).
	if p.BytesHashed != 13+7+9+9+9+5 {
		t.Errorf("BytesHashed = %d, want %d (size-mismatched files are never read)", p.BytesHashed, 13+7+9+9+9+5)
	}
	if r, u, by := p.Counts(); r != 3 || u != 5 || by[NotFound] != 4 || by[Ambiguous] != 1 {
		t.Errorf("Counts = %d repairable, %d unresolved, %v", r, u, by)
	}
	// Build writes nothing.
	if got := rowsOf(t, m, "sony"); !reflect.DeepEqual(got, before) {
		t.Errorf("Build changed rows")
	}

	// Apply rewrites dest_path on the three repairable rows and nothing else.
	var reported []string
	n, err := Apply(m, p, func(c Change) { reported = append(reported, c.Row.SourcePath) })
	if err != nil || n != 3 || len(reported) != 3 {
		t.Fatalf("Apply = %d, %v, reported %v", n, err, reported)
	}
	after := rowsOf(t, m, "sony")
	for src, b := range before {
		a := after[src]
		w, repaired := want[src]
		if repaired && w.outcome == Repairable {
			if a.DestPath != w.dest {
				t.Errorf("%s dest = %q, want %q", src, a.DestPath, w.dest)
			}
			b.DestPath = w.dest
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s changed beyond dest_path:\n before %+v\n after  %+v", src, b, a)
		}
	}
	if got := rowsOf(t, m, "zve"); !reflect.DeepEqual(got, otherBefore) {
		t.Errorf("another disk's rows changed: %+v", got)
	}
	// A second pass finds the repaired rows intact and only the rest missing.
	p2, err := Build(context.Background(), m, "sony", root)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Intact != 4 || len(p2.Changes) != 5 {
		t.Errorf("second pass: intact %d changes %d, want 4/5", p2.Intact, len(p2.Changes))
	}
}

func TestUpdateDestPathRefusesUnknownRow(t *testing.T) {
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.UpdateDestPath("sony", "nope", "x"); err == nil {
		t.Error("UpdateDestPath on a missing row should fail, not silently affect 0 rows")
	}
}

// Review #7 finding 1: the search never follows a symlink - not as the
// candidate, not as the subdirectory, not as a component of the row's own
// directory - and an accepted path is proven under the root.
func TestBuildNeverFollowsSymlinks(t *testing.T) {
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	root, outside := t.TempDir(), t.TempDir()

	// Leaf symlink: root/DCIM/LEAF.JPG -> outside/LEAF.JPG with matching bytes.
	write(t, filepath.Join(outside, "LEAF.JPG"), "leaf bytes")
	if err := os.MkdirAll(filepath.Join(root, "DCIM"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "LEAF.JPG"), filepath.Join(root, "DCIM", "LEAF.JPG")); err != nil {
		t.Fatal(err)
	}
	row(t, m, "LEAF.JPG", "LEAF.JPG", "leaf bytes")

	// Subdirectory symlink: root/LINKDIR -> outside/dir holding a match.
	write(t, filepath.Join(outside, "dir", "SUB.JPG"), "sub bytes")
	if err := os.Symlink(filepath.Join(outside, "dir"), filepath.Join(root, "LINKDIR")); err != nil {
		t.Fatal(err)
	}
	row(t, m, "SUB.JPG", "SUB.JPG", "sub bytes")

	// Ancestor symlink: the row's own directory root/Videos -> outside/videos,
	// with the match one level below it.
	write(t, filepath.Join(outside, "videos", "2024", "ANC.MP4"), "anc bytes")
	if err := os.Symlink(filepath.Join(outside, "videos"), filepath.Join(root, "Videos")); err != nil {
		t.Fatal(err)
	}
	row(t, m, "ANC.MP4", "Videos/ANC.MP4", "anc bytes")

	// A symlink at the old dest_path itself is not intact, even if it points
	// at the right bytes.
	write(t, filepath.Join(outside, "SELF.JPG"), "self bytes")
	if err := os.Symlink(filepath.Join(outside, "SELF.JPG"), filepath.Join(root, "SELF.JPG")); err != nil {
		t.Fatal(err)
	}
	row(t, m, "SELF.JPG", "SELF.JPG", "self bytes")

	// A directory at the old dest_path (review #7 finding 2).
	if err := os.MkdirAll(filepath.Join(root, "DIR.JPG"), 0o755); err != nil {
		t.Fatal(err)
	}
	row(t, m, "DIR.JPG", "DIR.JPG", "dir bytes")

	// A row whose dest_path climbs out of the root lexically.
	write(t, filepath.Join(outside, "up", "UP.JPG"), "up bytes")
	row(t, m, "UP.JPG", "../UP.JPG", "up bytes")

	// Control: a real match through real directories still repairs.
	write(t, filepath.Join(root, "DCIM", "REAL.JPG"), "real bytes")
	row(t, m, "REAL.JPG", "REAL.JPG", "real bytes")

	p, err := Build(context.Background(), m, "sony", root)
	if err != nil {
		t.Fatal(err)
	}
	got := outcomes(p)
	want := map[string]Outcome{
		"LEAF.JPG": NotFound,
		"SUB.JPG":  NotFound,
		"ANC.MP4":  NotFound,
		"SELF.JPG": NotAFile,
		"DIR.JPG":  NotAFile,
		"UP.JPG":   NotFound,
		"REAL.JPG": Repairable,
	}
	for src, w := range want {
		if got[src].Outcome != w {
			t.Errorf("%s: %s, want %s (candidates %v)", src, got[src].Outcome, w, got[src].Candidates)
		}
	}
	if p.Intact != 0 || len(p.Changes) != 7 {
		t.Errorf("intact %d, changes %d; want 0/7", p.Intact, len(p.Changes))
	}
	if got["SELF.JPG"].Detail != "a symlink" || got["DIR.JPG"].Detail != "a directory" {
		t.Errorf("NOT A FILE details: %q, %q", got["SELF.JPG"].Detail, got["DIR.JPG"].Detail)
	}
	// Only the control was hashed: nothing behind a symlink was ever read.
	if p.BytesHashed != int64(len("real bytes")) {
		t.Errorf("BytesHashed = %d, want %d - a file behind a symlink was read", p.BytesHashed, len("real bytes"))
	}
}

// B33: a candidate that is already some row's dest_path - any disk, any
// status, compared physically - is OWNED and never chosen, even when it is
// the only match.
func TestBuildOwnedCandidateIsNeverChosen(t *testing.T) {
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	root := t.TempDir()
	write(t, filepath.Join(root, "DCIM", "TAKEN.JPG"), "taken bytes")
	row(t, m, "TAKEN.JPG", "TAKEN.JPG", "taken bytes")
	// Another disk's row claims that file, spelled with a case alias.
	if err := m.Upsert(manifest.Entry{SourceDisk: "zve", SourcePath: "x", DestPath: "dcim/taken.jpg",
		Size: 11, MtimeNs: 1, SHA256: sha("taken bytes"), CopiedAt: 2, Status: "deduped"}); err != nil {
		t.Fatal(err)
	}
	p, err := Build(context.Background(), m, "sony", root)
	if err != nil {
		t.Fatal(err)
	}
	c := outcomes(p)["TAKEN.JPG"]
	if c.Outcome != Owned || c.Owner != "zve:x" || c.NewDest != "" || len(c.Candidates) != 1 {
		t.Errorf("change = %+v, want OWNED by zve:x with the candidate listed and no NewDest", c)
	}
	if n, err := Apply(m, p, nil); err != nil || n != 0 {
		t.Errorf("Apply = %d, %v, want 0 rows", n, err)
	}
	if r, u, by := p.Counts(); r != 0 || u != 1 || by[Owned] != 1 {
		t.Errorf("Counts = %d/%d/%v", r, u, by)
	}
}
