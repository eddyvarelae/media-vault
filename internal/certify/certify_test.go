package certify

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

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
