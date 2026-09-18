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

// Review #30: the ENOENT fallback (spelling-key match when a row's file
// cannot be stat'ed) applies only to a row of the SAME disk as the target,
// which shares this destRoot. A different-disk row folded-equal but living
// under another root is not flagged - that would refuse a valid restore.
// The false positive only arises on a case-sensitive filesystem (where a
// folded spelling is genuinely ENOENT under the root); this test builds it
// there and, on a case-insensitive filesystem, checks the alias path
// instead.
func TestBuildEnoentFallbackIsSameDiskOnly(t *testing.T) {
	m := open(t)
	rootA, outside := t.TempDir(), t.TempDir()
	write(t, filepath.Join(rootA, "real", "x.JPG"), "torn")
	write(t, filepath.Join(outside, "good.JPG"), "good")
	// Case sensitivity of rootA: does REAL/X.jpg resolve to real/x.JPG?
	_, insErr := os.Stat(filepath.Join(rootA, "REAL", "X.jpg"))
	caseInsensitive := insErr == nil

	if err := m.Upsert(manifest.Entry{SourceDisk: "A", SourcePath: "x.JPG", DestPath: "real/x.JPG", Size: 4, MtimeNs: 1,
		SHA256: sha("torn"), CopiedAt: 1, VerifiedAt: 2, Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	// A different disk B, folded-equal spelling, whose file is NOT under
	// rootA (it lives under B's own root, which restore does not know).
	if err := m.Upsert(manifest.Entry{SourceDisk: "B", SourcePath: "b", DestPath: "real/X.jpg", Size: 28, MtimeNs: 1,
		SHA256: sha("elsewhere"), CopiedAt: 1, Status: "deduped"}); err != nil {
		t.Fatal(err)
	}
	p, err := Build(context.Background(), m, "A", "x.JPG", filepath.Join(outside, "good.JPG"), rootA, sha("good"))
	got := map[string]bool{}
	for _, c := range p.Claimants {
		got[c.SourceDisk+":"+c.SourcePath] = true
	}
	if caseInsensitive {
		// real/X.jpg resolves to A's file: a genuine alias, caught by
		// stat+SameFile, correctly a claimant.
		if !errors.Is(err, ErrRefused) || !got["B:b"] {
			t.Errorf("case-insensitive: B's folded alias should be a claimant via SameFile; got %v, %v", got, err)
		}
	} else {
		// real/X.jpg is ENOENT under rootA; B belongs to another root and
		// must NOT be flagged on the folded key alone (the #30 false
		// positive), so this valid restore is not refused by B.
		if got["B:b"] {
			t.Errorf("case-sensitive: B (another disk under another root) was falsely claimed - the #30 false positive")
		}
	}

	// A missing sibling of the SAME disk, folded-equal, is still a claimant
	// (it shares this root). On a case-insensitive fs it resolves and is
	// caught by SameFile; on a case-sensitive one by the same-disk fallback.
	if err := m.Upsert(manifest.Entry{SourceDisk: "A", SourcePath: "sib", DestPath: "real/X.jpg", Size: 4, MtimeNs: 1,
		SHA256: sha("torn"), CopiedAt: 1, Status: "mismatch"}); err != nil {
		t.Fatal(err)
	}
	p, _ = Build(context.Background(), m, "A", "x.JPG", filepath.Join(outside, "good.JPG"), rootA, sha("good"))
	sib := false
	for _, c := range p.Claimants {
		if c.SourceDisk == "A" && c.SourcePath == "sib" {
			sib = true
		}
	}
	if !sib {
		t.Errorf("a same-disk missing sibling should be a claimant")
	}
}
