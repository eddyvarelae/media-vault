package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eddyvarelae/media-vault/internal/manifest"
	"github.com/eddyvarelae/media-vault/internal/testguard"
)

func TestMain(m *testing.M) {
	testguard.Require() // never write fixtures under /volume1 or /mnt
	os.Exit(m.Run())
}

func openManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func writeFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func build(t *testing.T, m *manifest.Manifest, src, dst string, c CollisionStrategy) *Plan {
	t.Helper()
	p, err := Build(context.Background(), m, "diskA", src, dst, "", nil, c)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var t0 = time.Date(2023, 6, 1, 12, 0, 0, 0, time.UTC)

func TestBuildSkipsUnchangedAndRecopiesChanged(t *testing.T) {
	m := openManifest(t)
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "same.mov"), "aaaa", t0)
	writeFile(t, filepath.Join(src, "bigger.mov"), "bbbbbbbb", t0)
	writeFile(t, filepath.Join(src, "touched.mov"), "cccc", t0.Add(time.Hour))
	writeFile(t, filepath.Join(src, "new.mov"), "dddd", t0)

	// Manifest says all three were archived at size 4 with mtime t0.
	for _, name := range []string{"same.mov", "bigger.mov", "touched.mov"} {
		if err := m.Upsert(manifest.Entry{SourceDisk: "diskA", SourcePath: name, DestPath: name,
			Size: 4, MtimeNs: t0.UnixNano(), SHA256: "x", CopiedAt: 1, Status: "verified"}); err != nil {
			t.Fatal(err)
		}
	}

	p := build(t, m, src, dst, CollisionSkip)
	if p.SkipCount != 1 {
		t.Errorf("SkipCount = %d, want 1 (same.mov)", p.SkipCount)
	}
	if got := rels(p.ToRecopy); len(got) != 2 || got["bigger.mov"] == "" || got["touched.mov"] == "" {
		t.Errorf("ToRecopy = %v, want bigger.mov + touched.mov", got)
	}
	if p.BytesToRecopy != 12 {
		t.Errorf("BytesToRecopy = %d, want 12", p.BytesToRecopy)
	}
	if got := rels(p.ToCopy); len(got) != 1 || got["new.mov"] == "" {
		t.Errorf("ToCopy = %v, want new.mov", got)
	}
	if len(p.DstCollisions) != 0 {
		t.Errorf("DstCollisions = %d, want 0", len(p.DstCollisions))
	}
}

func TestBuildCollisionSkipLeavesFileUnarchived(t *testing.T) {
	m := openManifest(t)
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "only.mov"), "new bytes", t0)
	writeFile(t, filepath.Join(dst, "only.mov"), "foreign bytes", t0)

	p := build(t, m, src, dst, CollisionSkip)
	if len(p.ToCopy) != 0 || len(p.DstCollisions) != 1 {
		t.Fatalf("ToCopy=%d DstCollisions=%d, want 0/1", len(p.ToCopy), len(p.DstCollisions))
	}
	if p.DstCollisions[0].DstRel != "only.mov" {
		t.Errorf("collision DstRel = %q", p.DstCollisions[0].DstRel)
	}
}

func TestBuildRenameMtimeYearResolvesCollision(t *testing.T) {
	m := openManifest(t)
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "Videos", "only.mov"), "new bytes", t0)
	writeFile(t, filepath.Join(dst, "Videos", "only.mov"), "foreign bytes", t0)

	// A successful rename lands in ToCopy, not DstCollisions: this is why a
	// run that renames every file still exits 0 under F3.
	p := build(t, m, src, dst, CollisionRenameMtimeYear)
	if len(p.DstCollisions) != 0 {
		t.Fatalf("DstCollisions = %d, want 0", len(p.DstCollisions))
	}
	if len(p.ToCopy) != 1 || p.ToCopy[0].DstRel != filepath.Join("Videos", "only_2023.mov") {
		t.Fatalf("ToCopy = %+v, want Videos/only_2023.mov", p.ToCopy)
	}
	if p.ToCopy[0].RelPath != filepath.Join("Videos", "only.mov") {
		t.Errorf("RelPath must stay the source path, got %q", p.ToCopy[0].RelPath)
	}
}

func TestBuildRenameStillBlockedIsACollision(t *testing.T) {
	m := openManifest(t)
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "only.mov"), "new bytes", t0)
	writeFile(t, filepath.Join(dst, "only.mov"), "foreign bytes", t0)
	writeFile(t, filepath.Join(dst, "only_2023.mov"), "also foreign", t0)

	// Renamed path exists too: the policy had its say and the file is still
	// not archivable. This is the exact set F3 fails the run on.
	p := build(t, m, src, dst, CollisionRenameMtimeYear)
	if len(p.ToCopy) != 0 || len(p.DstCollisions) != 1 {
		t.Fatalf("ToCopy=%d DstCollisions=%d, want 0/1", len(p.ToCopy), len(p.DstCollisions))
	}
	if p.DstCollisions[0].DstRel != "only_2023.mov" {
		t.Errorf("collision recorded at %q, want the renamed path", p.DstCollisions[0].DstRel)
	}
}

func TestBuildAppliesPrefixRulesAndSkipsJunk(t *testing.T) {
	m := openManifest(t)
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "DCIM", "100MEDIA", "C0001.MP4"), "v", t0)
	writeFile(t, filepath.Join(src, "DCIM", "100MEDIA", "C0001.JPG"), "p", t0)
	writeFile(t, filepath.Join(src, "DCIM", "100MEDIA", "C0001.SRT"), "s", t0)
	writeFile(t, filepath.Join(src, "DCIM", ".DS_Store"), "junk", t0)
	writeFile(t, filepath.Join(src, "DCIM", "._C0001.MP4"), "junk", t0)
	writeFile(t, filepath.Join(src, "MISC", "outside.txt"), "x", t0)

	rules, err := ParseRules([]string{"MP4=Videos", "jpg=Photos"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Build(context.Background(), m, "diskA", src, dst, "DCIM", rules, CollisionSkip)
	if err != nil {
		t.Fatal(err)
	}
	got := rels(p.ToCopy)
	want := map[string]string{
		"DCIM/100MEDIA/C0001.MP4": "Videos/C0001.MP4",
		"DCIM/100MEDIA/C0001.JPG": "Photos/C0001.JPG",
		"DCIM/100MEDIA/C0001.SRT": "100MEDIA/C0001.SRT", // no rule: prefix stripped, path kept
	}
	if len(got) != len(want) {
		t.Fatalf("ToCopy = %v, want %v", got, want)
	}
	for rel, dstRel := range want {
		if got[filepath.FromSlash(rel)] != filepath.FromSlash(dstRel) {
			t.Errorf("%s → %q, want %q", rel, got[filepath.FromSlash(rel)], dstRel)
		}
	}
}

// B20: the tagger's reports/ directories are skipped at any depth, by
// exact name; files and directories that merely contain the word are not.
func TestBuildSkipsReportsDirectories(t *testing.T) {
	m := openManifest(t)
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "reports", "a.json"), "tagger", t0)
	writeFile(t, filepath.Join(src, "Videos", "reports", "b.json"), "tagger", t0)
	writeFile(t, filepath.Join(src, "Videos", "reports", "deep", "c.json"), "tagger", t0)
	writeFile(t, filepath.Join(src, "Videos", "GX010008.MP4"), "clip", t0)
	writeFile(t, filepath.Join(src, "Videos", "reports.txt"), "a file, not a dir", t0)
	writeFile(t, filepath.Join(src, "Videos", "reportsX", "d.mov"), "not that name", t0)
	// Exact name only - and under its own parent, because on a case-folding
	// filesystem Videos/Reports would be Videos/reports.
	writeFile(t, filepath.Join(src, "Other", "Reports", "e.mov"), "case differs", t0)
	p := build(t, m, src, dst, CollisionSkip)
	got := rels(p.ToCopy)
	want := []string{"Videos/GX010008.MP4", "Videos/reports.txt", "Videos/reportsX/d.mov", "Other/Reports/e.mov"}
	if len(got) != len(want) {
		t.Fatalf("ToCopy = %v, want exactly %v", got, want)
	}
	for _, rel := range want {
		if got[filepath.FromSlash(rel)] == "" {
			t.Errorf("%s missing from ToCopy: %v", rel, got)
		}
	}
	for rel := range got {
		if strings.Contains(rel, "reports/") {
			t.Errorf("reports/ content planned: %s", rel)
		}
	}
}

func TestParseCollision(t *testing.T) {
	for _, s := range []string{"", "skip"} {
		if c, err := ParseCollision(s); err != nil || c != CollisionSkip {
			t.Errorf("ParseCollision(%q) = %v, %v", s, c, err)
		}
	}
	if c, err := ParseCollision("rename-mtime-year"); err != nil || c != CollisionRenameMtimeYear {
		t.Errorf("ParseCollision(rename-mtime-year) = %v, %v", c, err)
	}
	if _, err := ParseCollision("overwrite"); err == nil {
		t.Error("ParseCollision(overwrite) should fail")
	}
}

// rels maps RelPath → DstRel for a task list.
func rels(tasks []FileTask) map[string]string {
	out := map[string]string{}
	for _, f := range tasks {
		out[f.RelPath] = f.DstRel
	}
	return out
}
