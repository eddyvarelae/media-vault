package certify

import (
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

func openManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func row(path, status string) manifest.Entry {
	e := manifest.Entry{SourceDisk: "diskA", SourcePath: path, DestPath: path,
		Size: 10, MtimeNs: 1, SHA256: "deadbeef", CopiedAt: 2, Status: status}
	if status == "verified" {
		e.VerifiedAt = 3
	}
	return e
}

func TestBuildRefusesOnAnyNonVerifiedRow(t *testing.T) {
	// Every status in the vocabulary except verified. A certificate over a
	// disk with one such row would claim proof it does not have.
	for _, status := range []string{"copied", "mismatch", "deduped", "inventoried"} {
		t.Run(status, func(t *testing.T) {
			m := openManifest(t)
			cfg := t.TempDir()
			for _, e := range []manifest.Entry{row("ok1.mov", "verified"), row("bad.mov", status), row("ok2.mov", "verified")} {
				if err := m.Upsert(e); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Build(m, "diskA", cfg)
			if !errors.Is(err, ErrNotCertifiable) {
				t.Fatalf("err = %v, want ErrNotCertifiable", err)
			}
			if !strings.Contains(err.Error(), "bad.mov") || !strings.Contains(err.Error(), status) {
				t.Errorf("error should name the row and its status: %v", err)
			}
			if _, err := os.Stat(filepath.Join(cfg, "key.pem")); !os.IsNotExist(err) {
				t.Error("refusal must not create a signing key")
			}
		})
	}
}

func TestBuildRefusesEmptyDisk(t *testing.T) {
	m := openManifest(t)
	if _, err := Build(m, "nothing-here", t.TempDir()); err == nil {
		t.Fatal("expected an error for a disk with no rows")
	}
}

func TestBuildSignsWhenAllVerified(t *testing.T) {
	m := openManifest(t)
	cfg := t.TempDir()
	for _, e := range []manifest.Entry{row("a.mov", "verified"), row("b.mov", "verified")} {
		if err := m.Upsert(e); err != nil {
			t.Fatal(err)
		}
	}
	cert, err := Build(m, "diskA", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cert.FileCount != 2 || cert.TotalBytes != 20 || cert.SourceDisk != "diskA" || len(cert.Files) != 2 {
		t.Errorf("cert = %+v", cert)
	}
	if err := Verify(cert); err != nil {
		t.Fatalf("signature should verify: %v", err)
	}

	// Tamper: the signature must stop matching.
	tampered := *cert
	tampered.FileCount++
	if err := Verify(&tampered); err == nil {
		t.Error("tampered certificate verified")
	}

	// The key is created once and reused: a second certificate carries the
	// same public key, so certificates from one config dir are comparable.
	cert2, err := Build(m, "diskA", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cert2.PublicKeyHex != cert.PublicKeyHex {
		t.Error("public key changed between two builds from the same config dir")
	}
	info, err := os.Stat(filepath.Join(cfg, "key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key.pem mode = %o, want 600", info.Mode().Perm())
	}
}

// B25: the certificate must not land in the tree it certifies. certify has
// no destination root, so the tree is recognised by its own files: an
// ancestor of the output path under which the rows' dest_paths exist.
func TestInsideArchiveFindsTheTreeByItsFiles(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "media", "SonyA6700")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("DCIM/DSC0001.JPG", "a photo")
	write("CLIP/C0001.XML", "clip xml")
	rows := []manifest.Entry{
		{DestPath: "DCIM/DSC0001.JPG", Size: 7},
		{DestPath: "CLIP/C0001.XML", Size: 8},
		{DestPath: "", Size: 1}, // inventoried: no dest, ignored
	}
	if err := os.MkdirAll(filepath.Join(base, "docker", "vault-certs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "DCIM"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink into the tree from outside: the physical directory counts.
	if err := os.Symlink(root, filepath.Join(base, "certs-link")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		filepath.Join(root, "media-sonya6700.cert.json"):                     root,
		filepath.Join(root, "DCIM", "cert.json"):                             root,
		filepath.Join(root, "notyet", "cert.json"):                           root, // dir absent: lexical ancestors still hit
		filepath.Join(base, "certs-link", "cert.json"):                       root,
		filepath.Join(base, "docker", "vault-certs", "media-sonya6700.json"): "",
		filepath.Join(base, "media", "cert.json"):                            "", // sibling level: media/DCIM/DSC0001.JPG does not exist
		filepath.Join(t.TempDir(), "x.json"):                                 "",
	}
	for out, want := range cases {
		got, err := InsideArchive(out, rows)
		if err != nil {
			t.Errorf("InsideArchive(%s): %v", out, err)
			continue
		}
		if want != "" {
			r, _ := filepath.EvalSymlinks(want)
			want = r
		}
		if got != want {
			t.Errorf("InsideArchive(%s) = %q, want %q", out, got, want)
		}
	}
	// A same-name file of the wrong size is not the archive.
	write("DCIM/DSC0002.JPG", "a longer photo")
	if got, _ := InsideArchive(filepath.Join(root, "c.json"), []manifest.Entry{{DestPath: "DCIM/DSC0002.JPG", Size: 7}}); got != "" {
		t.Errorf("size mismatch should not identify the tree, got %q", got)
	}
}

// Review #12: CheckOutput's three layers directly, including --root with
// "." and "/" and a tree whose files no longer match their rows.
func TestCheckOutputLayers(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "archive")
	if err := os.MkdirAll(filepath.Join(root, "DCIM"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "DCIM", "a.JPG"), []byte("eight!!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := []manifest.Entry{{DestPath: "DCIM/a.JPG", Size: 7}} // the file is 8 bytes now
	certs := filepath.Join(base, "certs")
	if err := os.MkdirAll(certs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "DCIM", "a.JPG"), filepath.Join(certs, "leaf-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(certs, "dir-link")); err != nil {
		t.Fatal(err)
	}
	// Leaf symlink: refused regardless of root.
	for _, r := range []string{"", root} {
		if _, err := CheckOutput(filepath.Join(certs, "leaf-link"), r, stale); !errors.Is(err, ErrOutputNotAFile) {
			t.Errorf("root %q: leaf symlink: %v, want ErrOutputNotAFile", r, err)
		}
	}
	// Without --root and stale rows: the heuristic cannot see the tree.
	if got, err := CheckOutput(filepath.Join(root, "c.json"), "", stale); err != nil || got != "" {
		t.Errorf("fallback with stale rows = %q, %v (expected clear: the heuristic is only as good as the files)", got, err)
	}
	// With --root: containment, whatever the files say.
	for _, out := range []string{filepath.Join(root, "c.json"), filepath.Join(root, "DCIM", "c.json"), filepath.Join(root, "new", "c.json"), filepath.Join(certs, "dir-link", "c.json")} {
		got, err := CheckOutput(out, root, stale)
		if err != nil || got == "" {
			t.Errorf("--root: %s = %q, %v; want the root", out, got, err)
		}
	}
	if got, err := CheckOutput(filepath.Join(certs, "c.json"), root, stale); err != nil || got != "" {
		t.Errorf("--root, beside: %q, %v; want clear", got, err)
	}
	if got, _ := CheckOutput(filepath.Join(base, "c.json"), root, stale); got != "" {
		t.Errorf("--root, the parent of the root: %q; want clear", got)
	}
	// Roots "." and "/".
	wd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	if got, err := CheckOutput("DCIM/c.json", ".", stale); err != nil || got == "" {
		t.Errorf("root \".\": %q, %v; want inside", got, err)
	}
	if got, err := CheckOutput(filepath.Join(certs, "c.json"), ".", stale); err != nil || got != "" {
		t.Errorf("root \".\", outside: %q, %v; want clear", got, err)
	}
	if got, err := CheckOutput(filepath.Join(certs, "c.json"), "/", stale); err != nil || got == "" {
		t.Errorf("root \"/\": %q, %v; everything is inside /", got, err)
	}
}
