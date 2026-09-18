// Package scripts_test runs the shell tests under scripts/test/ as part of
// `go test ./...`, so one command covers the Go pipeline and the NAS
// scripts' handling of its exit codes.
package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/eddyvarelae/media-vault/internal/testguard"
)

func TestMain(m *testing.M) {
	testguard.Require() // the shell tests mktemp under TMPDIR
	os.Exit(m.Run())
}

func TestTarsCopyAllLogsFailedCards(t *testing.T) {
	cmd := exec.Command("bash", "./nas-tars-copy-all.sh")
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	t.Logf("\n%s", out)
	if err != nil {
		t.Fatalf("shell test failed: %v", err)
	}
}

func TestKippCopyAllShape(t *testing.T) {
	cmd := exec.Command("bash", "./nas-kipp-copy-all.sh")
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	t.Logf("\n%s", out)
	if err != nil {
		t.Fatalf("shell test failed: %v", err)
	}
}

func TestVerifyCertifyAllLogsEachLineOnce(t *testing.T) {
	cmd := exec.Command("bash", "./nas-verify-certify-all.sh")
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	t.Logf("\n%s", out)
	if err != nil {
		t.Fatalf("shell test failed: %v", err)
	}
}

// The tagging job is exercised against a manifest built by this very
// binary, so the wrapper builds vault first and hands its path over.
func TestRunTaggingAgainstARealManifest(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "vault")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/vault")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd := exec.Command("bash", "./run-tagging.sh")
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir(), "VAULT_BIN="+bin)
	out, err := cmd.CombinedOutput()
	t.Logf("\n%s", out)
	if err != nil {
		t.Fatalf("shell test failed: %v", err)
	}
}
