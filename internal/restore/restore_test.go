package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func open(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

// The physical checks restore makes on the destination, and the empty
// dest_path rule (verify's), without the CLI.
func TestBuildChecksTheDestinationPhysically(t *testing.T) {
	m := open(t)
	root, outside := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "DCIM", "torn.JPG"), "torn")
	write(t, filepath.Join(outside, "good.JPG"), "good")
	seed := func(src, dest string) {
		t.Helper()
		if err := m.Upsert(manifest.Entry{SourceDisk: "sony", SourcePath: src, DestPath: dest, Size: 4, MtimeNs: 1,
			SHA256: sha("torn"), CopiedAt: 1, VerifiedAt: 2, Status: "verified"}); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	good := filepath.Join(outside, "good.JPG")

	seed("DCIM/torn.JPG", "") // B39 shape: empty dest_path, located by source_path
	p, err := Build(ctx, m, "sony", "DCIM/torn.JPG", good, root, sha("good"))
	if err != nil || p.DestRel != "DCIM/torn.JPG" || p.CurrentSHA != sha("torn") || !p.RowAttests || len(p.Claimants) != 0 || p.AlreadyThere {
		t.Errorf("empty dest_path: %+v, %v", p, err)
	}

	// Rows whose own destination is not a plain file under the root.
	seed("via-link.JPG", "alias/other.JPG")
	seed("is-dir.JPG", "DCIM")
	seed("is-link.JPG", "DCIM/lnk.JPG")
	if err := os.Symlink(filepath.Join(root, "DCIM"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "DCIM", "other.JPG"), "torn")
	write(t, filepath.Join(outside, "elsewhere.JPG"), "torn")
	if err := os.Symlink(filepath.Join(outside, "elsewhere.JPG"), filepath.Join(root, "DCIM", "lnk.JPG")); err != nil {
		t.Fatal(err)
	}
	for src, want := range map[string]string{
		"via-link.JPG": "passes through a symlink",
		"is-dir.JPG":   "not a regular file",
		"is-link.JPG":  "not a regular file",
	} {
		_, err := Build(ctx, m, "sony", src, good, root, sha("good"))
		if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want refusal %q", src, err, want)
		}
	}
}

// Review #24 finding 1: a row path that climbs out of the root - empty
// dest_path with a `../` source_path, or a `../` dest_path - is refused
// before anything is hashed, and the file outside is never touched.
func TestBuildRefusesPathsOutsideTheRoot(t *testing.T) {
	m := open(t)
	base := t.TempDir()
	root := filepath.Join(base, "archive")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(base, "outside.JPG"), "outside bytes")
	write(t, filepath.Join(base, "good.JPG"), "good")
	for src, dest := range map[string]string{"../outside.JPG": "", "escape.JPG": "../outside.JPG", "abs.JPG": filepath.Join(base, "outside.JPG")} {
		if err := m.Upsert(manifest.Entry{SourceDisk: "sony", SourcePath: src, DestPath: dest, Size: 13, MtimeNs: 1,
			SHA256: sha("outside bytes"), CopiedAt: 1, VerifiedAt: 2, Status: "verified"}); err != nil {
			t.Fatal(err)
		}
		p, err := Build(context.Background(), m, "sony", src, filepath.Join(base, "good.JPG"), root, sha("good"))
		if !errors.Is(err, ErrRefused) || !(strings.Contains(err.Error(), "climbs out of") || strings.Contains(err.Error(), "is absolute")) {
			t.Errorf("%s (dest %q): %v, want a containment refusal", src, dest, err)
		}
		if p != nil && p.CurrentSHA != "" {
			t.Errorf("%s: the outside file was hashed before the refusal", src)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(base, "outside.JPG")); string(got) != "outside bytes" {
		t.Errorf("outside file touched: %q", got)
	}
}

// Review #24 finding 2: another row that reaches the replaced file through
// a symlink alias - a directory symlink, a leaf symlink, a hard link, a
// case alias - is a claimant, found by file identity, not by spelling.
func TestBuildFindsClaimantsByIdentity(t *testing.T) {
	m := open(t)
	root, outside := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "real", "x.JPG"), "torn")
	write(t, filepath.Join(outside, "good.JPG"), "good")
	if err := m.Upsert(manifest.Entry{SourceDisk: "A", SourcePath: "x.JPG", DestPath: "real/x.JPG", Size: 4, MtimeNs: 1,
		SHA256: sha("torn"), CopiedAt: 1, VerifiedAt: 2, Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real", "x.JPG"), filepath.Join(root, "leaf.JPG")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "real", "x.JPG"), filepath.Join(root, "hard.JPG")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "real", "unrelated.JPG"), "torn") // same bytes, different file: not a claimant
	claimants := map[string]string{                                // other rows: source_path -> dest_path
		"B:dir-alias":  "alias/x.JPG",
		"B:leaf-alias": "leaf.JPG",
		"B:hard-link":  "hard.JPG",
		"B:case-alias": "REAL/X.jpg",
		"C:missing":    "real/x.jpg", // no such spelling on disk (case-sensitive fs) or the same file (folding): claimant either way
	}
	for k, dest := range claimants {
		disk, src, _ := strings.Cut(k, ":")
		if err := m.Upsert(manifest.Entry{SourceDisk: disk, SourcePath: src, DestPath: dest, Size: 4, MtimeNs: 1,
			SHA256: sha("torn"), CopiedAt: 1, Status: "deduped"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Upsert(manifest.Entry{SourceDisk: "D", SourcePath: "u", DestPath: "real/unrelated.JPG", Size: 4, MtimeNs: 1,
		SHA256: sha("torn"), CopiedAt: 1, Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	p, err := Build(context.Background(), m, "A", "x.JPG", filepath.Join(outside, "good.JPG"), root, sha("good"))
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want the claimants refusal", err)
	}
	got := map[string]bool{}
	for _, c := range p.Claimants {
		got[c.SourceDisk+":"+c.SourcePath] = true
	}
	for k := range claimants {
		if !got[k] {
			t.Errorf("%s (dest %s) not found as a claimant; got %v", k, claimants[k], got)
		}
	}
	if got["D:u"] {
		t.Errorf("a different file with the same bytes was reported as a claimant")
	}
	if len(p.Claimants) != len(claimants) {
		t.Errorf("claimants = %d, want %d: %v", len(p.Claimants), len(claimants), got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "real", "x.JPG")); string(got) != "torn" {
		t.Errorf("target touched: %q", got)
	}
}

// Apply replaces through the writer and rewrites exactly the byte facts.
func TestApplyRewritesOnlyTheByteFacts(t *testing.T) {
	m := open(t)
	root, outside := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "DCIM", "torn.JPG"), "torn")
	write(t, filepath.Join(outside, "good.JPG"), "good")
	row := manifest.Entry{SourceDisk: "sony", SourcePath: "DCIM/torn.JPG", DestPath: "", Size: 4, MtimeNs: 1,
		SHA256: sha("torn"), CopiedAt: 1, VerifiedAt: 2, Status: "verified"}
	if err := m.Upsert(row); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := Build(ctx, m, "sony", "DCIM/torn.JPG", filepath.Join(outside, "good.JPG"), root, sha("good"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := Apply(ctx, m, p, root)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "DCIM", "torn.JPG"))
	if string(got) != "good" {
		t.Errorf("file = %q", got)
	}
	stored, _ := m.Lookup("sony", "DCIM/torn.JPG")
	if stored.SHA256 != sha("good") || stored.Size != 4 || stored.Status != "copied" || stored.VerifiedAt != 0 || stored.DestPath != "" || stored.CopiedAt <= 1 || stored.MtimeNs == 1 {
		t.Errorf("row after = %+v", stored)
	}
	if after.SHA256 != stored.SHA256 {
		t.Errorf("Apply returned %+v, stored %+v", after, stored)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "DCIM")); len(entries) != 1 {
		t.Errorf("leftovers in DCIM: %v", entries)
	}
}
