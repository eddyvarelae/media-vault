package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eddyvarelae/media-vault/internal/certify"
	"github.com/eddyvarelae/media-vault/internal/manifest"
)

// The tests below run the real pipeline against t.TempDir() only. Nothing
// here may reach the NAS paths, even on a host where they exist: production
// footage lives there and the manifest is single-writer.
var forbiddenRoots = []string{"/volume1", "/mnt"}

func TestMain(m *testing.M) {
	// Re-exec hook: the test binary becomes `vault` when asked to, so the
	// round-trip tests exercise main() and get real exit codes.
	if os.Getenv("VAULT_TEST_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	for _, root := range forbiddenRoots {
		if strings.HasPrefix(os.TempDir(), root) {
			fmt.Fprintf(os.Stderr, "refusing to run: TMPDIR %q is under %s\n", os.TempDir(), root)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// safeDir is t.TempDir() plus the NAS guard, for every path a test hands to
// the CLI.
func safeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, root := range forbiddenRoots {
		if strings.HasPrefix(dir, root) {
			t.Fatalf("temp dir %q is under forbidden root %s", dir, root)
		}
	}
	return dir
}

// vault runs the CLI as a subprocess (this test binary re-exec'd through
// main) with VAULT_CONFIG pinned to cfg and cwd pinned to a temp dir, so a
// missing env var cannot fall back to the repo's ./vault-config either.
func vault(t *testing.T, cfg string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Dir = safeDir(t)
	cmd.Env = append(os.Environ(), "VAULT_TEST_MAIN=1", "VAULT_CONFIG="+cfg)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("vault %v: %v", args, err)
	}
	t.Logf("$ vault %s\n(exit %d)\n%s%s", strings.Join(args, " "), code, out.String(), errb.String())
	return out.String(), errb.String(), code
}

// copyInProcess calls runCopy directly and captures what it printed. This is
// the F3 decision under test: the status it returns is what main exits with.
func copyInProcess(t *testing.T, m *manifest.Manifest, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	capture := func(f **os.File) (func() string, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		orig := *f
		*f = w
		done := make(chan string)
		go func() {
			b, _ := io.ReadAll(r)
			done <- string(b)
		}()
		return func() string {
			w.Close()
			*f = orig
			return <-done
		}, nil
	}
	getOut, err := capture(&os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	getErr, err := capture(&os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	code = runCopy(context.Background(), m, args)
	stdout, stderr = getOut(), getErr()
	t.Logf("runCopy(%v) = %d\n%s%s", args, code, stdout, stderr)
	return stdout, stderr, code
}

func openManifest(t *testing.T) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Open(filepath.Join(safeDir(t), "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

var t0 = time.Date(2023, 6, 1, 12, 0, 0, 0, time.UTC)

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

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noPartials(t *testing.T, dst string) {
	t.Helper()
	filepath.WalkDir(dst, func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(path, ".vault-partial") {
			t.Errorf("leftover partial: %s", path)
		}
		return nil
	})
}

func statuses(t *testing.T, m *manifest.Manifest, disk string) map[string]string {
	t.Helper()
	rows, err := m.ListByDisk(disk)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range rows {
		out[e.SourcePath] = e.Status
	}
	return out
}

// TestRoundTrip drives scan → copy → verify → certify through main() on a
// temp source, temp destination and temp manifest, then the recopy and
// corruption paths that follow from it.
func TestRoundTrip(t *testing.T) {
	cfg, src, dst := safeDir(t), safeDir(t), safeDir(t)
	writeFile(t, filepath.Join(src, "DCIM", "C0001.MP4"), "clip one", t0)
	writeFile(t, filepath.Join(src, "DCIM", "C0002.MP4"), "clip two", t0)
	writeFile(t, filepath.Join(src, "DCIM", "C0001.JPG"), "still", t0)

	out, _, code := vault(t, cfg, "scan", "cam", src, dst, "--prefix", "DCIM", "--rule", "MP4=Videos", "--rule", "JPG=Photos")
	if code != 0 || !strings.Contains(out, "Files to copy:    3") {
		t.Fatalf("scan: exit %d", code)
	}

	out, _, code = vault(t, cfg, "copy", "cam", src, dst, "--prefix", "DCIM", "--rule", "MP4=Videos", "--rule", "JPG=Photos")
	if code != 0 || !strings.Contains(out, "Done. Copied 3/3 files") {
		t.Fatalf("copy: exit %d", code)
	}
	if got := readFile(t, filepath.Join(dst, "Videos", "C0002.MP4")); got != "clip two" {
		t.Errorf("dest content = %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "Photos", "C0001.JPG")); got != "still" {
		t.Errorf("dest content = %q", got)
	}
	noPartials(t, dst)

	// Second copy is a no-op and exits 0.
	out, _, code = vault(t, cfg, "copy", "cam", src, dst, "--prefix", "DCIM", "--rule", "MP4=Videos", "--rule", "JPG=Photos")
	if code != 0 || !strings.Contains(out, "Nothing to copy") {
		t.Fatalf("no-op copy: exit %d", code)
	}

	// Rows are `copied`, so certify must refuse (exit 1) before verify runs.
	_, errOut, code := vault(t, cfg, "certify", "cam")
	if code != 1 || !strings.Contains(errOut, "Cannot certify") {
		t.Fatalf("certify before verify: exit %d, want 1", code)
	}

	out, _, code = vault(t, cfg, "verify", "cam", dst)
	if code != 0 || !strings.Contains(out, "Verified: 3   Mismatch: 0   Missing: 0   Errors: 0") {
		t.Fatalf("verify: exit %d", code)
	}

	certPath := filepath.Join(safeDir(t), "cam.cert.json")
	_, _, code = vault(t, cfg, "certify", "cam", certPath)
	if code != 0 {
		t.Fatalf("certify: exit %d", code)
	}
	var cert certify.Certificate
	if err := json.Unmarshal([]byte(readFile(t, certPath)), &cert); err != nil {
		t.Fatal(err)
	}
	if cert.FileCount != 3 || cert.TotalBytes != int64(len("clip one")+len("clip two")+len("still")) || cert.SourceDisk != "cam" {
		t.Errorf("cert = %+v", cert)
	}
	if err := certify.Verify(&cert); err != nil {
		t.Errorf("certificate signature: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "key.pem")); err != nil {
		t.Errorf("signing key should live under VAULT_CONFIG: %v", err)
	}

	// Recopy: the source changes size AND mtime; scan plans exactly one
	// recopy, copy replaces the destination, and the row drops back to
	// `copied` so certify refuses again until verify re-promotes it.
	writeFile(t, filepath.Join(src, "DCIM", "C0001.MP4"), "clip one, re-exported", t0.Add(time.Hour))
	out, _, code = vault(t, cfg, "scan", "cam", src, dst, "--prefix", "DCIM", "--rule", "MP4=Videos", "--rule", "JPG=Photos")
	if code != 0 || !strings.Contains(out, "Files to recopy:  1") || !strings.Contains(out, "Files to skip:    2") {
		t.Fatalf("scan after change: exit %d", code)
	}
	out, _, code = vault(t, cfg, "copy", "cam", src, dst, "--prefix", "DCIM", "--rule", "MP4=Videos", "--rule", "JPG=Photos")
	if code != 0 || !strings.Contains(out, "Done. Copied 1/1 files") {
		t.Fatalf("recopy: exit %d", code)
	}
	if got := readFile(t, filepath.Join(dst, "Videos", "C0001.MP4")); got != "clip one, re-exported" {
		t.Errorf("recopied content = %q", got)
	}
	if _, _, code = vault(t, cfg, "certify", "cam"); code != 1 {
		t.Fatalf("certify after recopy: exit %d, want 1", code)
	}
	if _, _, code = vault(t, cfg, "verify", "cam", dst); code != 0 {
		t.Fatalf("verify after recopy: exit %d", code)
	}
	if _, _, code = vault(t, cfg, "certify", "cam"); code != 0 {
		t.Fatalf("certify after re-verify: exit %d", code)
	}

	// A mtime-only change is also a recopy (size equal, mtime differs).
	writeFile(t, filepath.Join(src, "DCIM", "C0001.JPG"), "still", t0.Add(2*time.Hour))
	out, _, code = vault(t, cfg, "scan", "cam", src, dst, "--prefix", "DCIM", "--rule", "MP4=Videos", "--rule", "JPG=Photos")
	if code != 0 || !strings.Contains(out, "Files to recopy:  1") {
		t.Fatalf("scan after mtime-only change: exit %d\n%s", code, out)
	}

	// Bit-rot at the destination: verify exits 1, names the file, marks the
	// row mismatch; certify refuses. A missing destination file also fails
	// verify without touching the row.
	writeFile(t, filepath.Join(dst, "Videos", "C0002.MP4"), "clip twx", t0)
	out, _, code = vault(t, cfg, "verify", "cam", dst)
	if code != 1 || !strings.Contains(out, "MISMATCH") || !strings.Contains(out, "C0002.MP4") {
		t.Fatalf("verify with corruption: exit %d, want 1 naming the file", code)
	}
	_, errOut, code = vault(t, cfg, "certify", "cam")
	if code != 1 || !strings.Contains(errOut, `"mismatch"`) {
		t.Fatalf("certify with mismatch: exit %d, stderr %q", code, errOut)
	}
	if err := os.Remove(filepath.Join(dst, "Photos", "C0001.JPG")); err != nil {
		t.Fatal(err)
	}
	out, _, code = vault(t, cfg, "verify", "cam", dst)
	if code != 1 || !strings.Contains(out, "missing") || !strings.Contains(out, "Missing: 1") {
		t.Fatalf("verify with missing: exit %d", code)
	}
}

// TestCopyExitStatus is the F3 "done when" list, plus the F2 cases it
// preserves, as a table over runCopy's return value.
func TestCopyExitStatus(t *testing.T) {
	type tc struct {
		name    string
		setup   func(t *testing.T, src, dst string) // files on both sides
		flags   []string
		want    int
		stderr  []string // substrings that must appear (INCOMPLETE reasons)
		stdout  []string
		noFiles bool // dst must stay empty (dry-run)
	}
	cases := []tc{
		{
			name: "fully successful run exits 0",
			setup: func(t *testing.T, src, dst string) {
				writeFile(t, filepath.Join(src, "a.mov"), "a", t0)
				writeFile(t, filepath.Join(src, "b.mov"), "b", t0)
			},
			want:   0,
			stdout: []string{"Done. Copied 2/2 files"},
		},
		{
			name:   "no-op run exits 0",
			setup:  func(t *testing.T, src, dst string) {},
			want:   0,
			stdout: []string{"Nothing to copy"},
		},
		{
			name: "unresolved collision under skip exits 1 and names collisions",
			setup: func(t *testing.T, src, dst string) {
				writeFile(t, filepath.Join(src, "only.mov"), "new bytes", t0)
				writeFile(t, filepath.Join(dst, "only.mov"), "foreign", t0)
			},
			want:   1,
			stderr: []string{"INCOMPLETE:", "1 file(s) skipped on unresolved destination collisions"},
			stdout: []string{"Done. Copied 0/0 files"},
		},
		{
			name: "rename-mtime-year still blocked exits 1 and names collisions",
			setup: func(t *testing.T, src, dst string) {
				writeFile(t, filepath.Join(src, "only.mov"), "new bytes", t0)
				writeFile(t, filepath.Join(dst, "only.mov"), "foreign", t0)
				writeFile(t, filepath.Join(dst, "only_2023.mov"), "also foreign", t0)
			},
			flags:  []string{"--on-collision", "rename-mtime-year"},
			want:   1,
			stderr: []string{"INCOMPLETE:", "1 file(s) skipped on unresolved destination collisions"},
		},
		{
			name: "every rename succeeds exits 0",
			setup: func(t *testing.T, src, dst string) {
				writeFile(t, filepath.Join(src, "only.mov"), "new bytes", t0)
				writeFile(t, filepath.Join(src, "other.mov"), "more bytes", t0)
				writeFile(t, filepath.Join(dst, "only.mov"), "foreign", t0)
				writeFile(t, filepath.Join(dst, "other.mov"), "foreign", t0)
			},
			flags:  []string{"--on-collision", "rename-mtime-year"},
			want:   0,
			stdout: []string{"Done. Copied 2/2 files"},
		},
		{
			name: "dry-run with collisions exits 0 and writes nothing",
			setup: func(t *testing.T, src, dst string) {
				writeFile(t, filepath.Join(src, "only.mov"), "new bytes", t0)
				writeFile(t, filepath.Join(src, "fresh.mov"), "fresh", t0)
				writeFile(t, filepath.Join(dst, "only.mov"), "foreign", t0)
			},
			flags:   []string{"--dry-run"},
			want:    0,
			stdout:  []string{"(dry-run; 1 files would be copied, 0 recorded as deduped, 1 collisions skipped)"},
			noFiles: true,
		},
		{
			name: "failed copy exits 1 and names the failure",
			setup: func(t *testing.T, src, dst string) {
				// dst/sub is a FILE, so mkdir for sub/x.mov fails inside
				// copy.File; scan does not see it as a collision because
				// the stat on sub/x.mov fails with ENOTDIR, not "exists".
				writeFile(t, filepath.Join(src, "sub", "x.mov"), "x", t0)
				writeFile(t, filepath.Join(src, "ok.mov"), "ok", t0)
				writeFile(t, filepath.Join(dst, "sub"), "i am a file", t0)
			},
			want:   1,
			stderr: []string{"INCOMPLETE:", "1 file(s) failed to copy"},
			stdout: []string{"Done. Copied 1/2 files"},
		},
		{
			name: "orphaned intra-run duplicate exits 1 and names both reasons",
			setup: func(t *testing.T, src, dst string) {
				// y.mov dedupes against sub/x.mov (same bytes, walked
				// first); x fails to copy, so y is left unarchived.
				writeFile(t, filepath.Join(src, "sub", "x.mov"), "twin", t0)
				writeFile(t, filepath.Join(src, "y.mov"), "twin", t0)
				writeFile(t, filepath.Join(dst, "sub"), "i am a file", t0)
			},
			flags:  []string{"--dedupe-content"},
			want:   1,
			stderr: []string{"INCOMPLETE:", "1 file(s) failed to copy", "1 duplicate(s) left unarchived"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := openManifest(t)
			src, dst := safeDir(t), safeDir(t)
			c.setup(t, src, dst)
			args := append([]string{"diskA", src, dst}, c.flags...)
			out, errOut, code := copyInProcess(t, m, args...)
			if code != c.want {
				t.Errorf("exit = %d, want %d", code, c.want)
			}
			for _, s := range c.stderr {
				if !strings.Contains(errOut, s) {
					t.Errorf("stderr missing %q", s)
				}
			}
			if c.want == 0 && strings.Contains(errOut, "INCOMPLETE") {
				t.Errorf("exit 0 must not print INCOMPLETE")
			}
			for _, s := range c.stdout {
				if !strings.Contains(out, s) {
					t.Errorf("stdout missing %q", s)
				}
			}
			noPartials(t, dst)
			if c.noFiles {
				if rows := statuses(t, m, "diskA"); len(rows) != 0 {
					t.Errorf("dry-run wrote manifest rows: %v", rows)
				}
				if _, err := os.Stat(filepath.Join(dst, "fresh.mov")); !os.IsNotExist(err) {
					t.Errorf("dry-run wrote a file")
				}
			}
		})
	}
}

// TestRenameLandsUnderRenamedPath pins what "every rename succeeds" means on
// disk and in the manifest: the row keeps the source path and points at the
// renamed destination, so verify and certify find it.
func TestRenameLandsUnderRenamedPath(t *testing.T) {
	m := openManifest(t)
	src, dst := safeDir(t), safeDir(t)
	writeFile(t, filepath.Join(src, "only.mov"), "new bytes", t0)
	writeFile(t, filepath.Join(dst, "only.mov"), "foreign", t0)

	if _, _, code := copyInProcess(t, m, "diskA", src, dst, "--on-collision", "rename-mtime-year"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := readFile(t, filepath.Join(dst, "only_2023.mov")); got != "new bytes" {
		t.Errorf("renamed dest = %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "only.mov")); got != "foreign" {
		t.Errorf("existing file was overwritten: %q", got)
	}
	e, err := m.Lookup("diskA", "only.mov")
	if err != nil || e == nil {
		t.Fatalf("row: %v %v", e, err)
	}
	if e.DestPath != "only_2023.mov" || e.Status != "copied" {
		t.Errorf("row = %+v", e)
	}
}

// TestCollisionRowsAndExitThroughMain checks the wiring: runCopy's status is
// the process exit code, and a collision-skipped file gets no manifest row.
func TestCollisionRowsAndExitThroughMain(t *testing.T) {
	cfg, src, dst := safeDir(t), safeDir(t), safeDir(t)
	writeFile(t, filepath.Join(src, "only.mov"), "new bytes", t0)
	writeFile(t, filepath.Join(src, "ok.mov"), "fine", t0)
	writeFile(t, filepath.Join(dst, "only.mov"), "foreign", t0)

	_, errOut, code := vault(t, cfg, "copy", "diskA", src, dst)
	if code != 1 || !strings.Contains(errOut, "INCOMPLETE:") || !strings.Contains(errOut, "collision") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	m, err := manifest.Open(filepath.Join(cfg, "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	rows := statuses(t, m, "diskA")
	if len(rows) != 1 || rows["ok.mov"] != "copied" {
		t.Errorf("rows = %v, want only ok.mov copied", rows)
	}

	// Usage errors are exit 2, distinct from INCOMPLETE.
	if _, _, code := vault(t, cfg, "copy", "diskA", src); code != 2 {
		t.Errorf("usage error exit = %d, want 2", code)
	}
	if _, _, code := vault(t, cfg); code != 2 {
		t.Errorf("no-command exit = %d, want 2", code)
	}
}
