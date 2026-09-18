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
	seed("DCIM/torn.JPG", "") // B39 shape: empty dest_path, located by source_path
	seed("via-link.JPG", "alias/torn.JPG")
	seed("is-dir.JPG", "DCIM")
	seed("is-link.JPG", "DCIM/lnk.JPG")
	if err := os.Symlink(filepath.Join(root, "DCIM"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "DCIM", "torn.JPG"), filepath.Join(root, "DCIM", "lnk.JPG")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	good := filepath.Join(outside, "good.JPG")

	p, err := Build(ctx, m, "sony", "DCIM/torn.JPG", good, root, sha("good"))
	if err != nil || p.DestRel != "DCIM/torn.JPG" || p.CurrentSHA != sha("torn") || !p.RowAttests || len(p.Claimants) != 0 || p.AlreadyThere {
		t.Errorf("empty dest_path: %+v, %v", p, err)
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
	// Case-aliased claimant of another disk, and the target row excluded.
	if err := m.Upsert(manifest.Entry{SourceDisk: "kipp", SourcePath: "k", DestPath: "dcim/TORN.jpg", Size: 4, SHA256: sha("torn"), CopiedAt: 1, Status: "deduped"}); err != nil {
		t.Fatal(err)
	}
	p, err = Build(ctx, m, "sony", "DCIM/torn.JPG", good, root, sha("good"))
	if !errors.Is(err, ErrRefused) || len(p.Claimants) != 1 || p.Claimants[0].SourceDisk != "kipp" || !strings.Contains(err.Error(), "kipp:k (deduped)") {
		t.Errorf("claimant: %+v, %v", p.Claimants, err)
	}
	// The row's own file was hashed and recorded even on the refusal.
	if p.CurrentSHA != sha("torn") {
		t.Errorf("current sha not recorded on refusal")
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
