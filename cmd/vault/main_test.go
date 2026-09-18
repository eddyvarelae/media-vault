package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/eddyvarelae/media-vault/internal/certify"
	"github.com/eddyvarelae/media-vault/internal/manifest"
	"github.com/eddyvarelae/media-vault/internal/testguard"
)

// The tests below run the real pipeline against t.TempDir() only. Nothing
// here may reach the NAS paths, even on a host where they exist: production
// footage lives there and the manifest is single-writer. testguard.Require
// enforces that on the resolved temp root before anything is written.
func TestMain(m *testing.M) {
	testguard.Require()
	// Re-exec hook: the test binary becomes `vault` when asked to, so the
	// round-trip tests exercise main() and get real exit codes. Only the
	// vault() helper sets this, and only in the child's environment.
	if os.Getenv("VAULT_TEST_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// vault runs the CLI as a subprocess (this test binary re-exec'd through
// main) with VAULT_CONFIG pinned to cfg and cwd pinned to a temp dir, so a
// missing env var cannot fall back to the repo's ./vault-config either.
// Every path a test hands it comes from t.TempDir(), which TestMain guarded.
func vault(t *testing.T, cfg string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Dir = t.TempDir()
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
	m, err := manifest.Open(filepath.Join(t.TempDir(), "manifest.db"))
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

// rowsOf reopens the manifest under cfg and returns the persisted rows for
// disk, keyed by source path. A fresh open per call, never the subprocess's
// handle: this is what the next `vault` run (and certify) will actually see.
func rowsOf(t *testing.T, cfg, disk string) map[string]manifest.Entry {
	t.Helper()
	m, err := manifest.Open(filepath.Join(cfg, "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
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

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// wantRow checks the fields a manifest row must carry for a file with the
// given content, at the given status. VerifiedAt is checked for zero vs
// non-zero here; ordering against earlier snapshots is asserted inline.
func wantRow(t *testing.T, rows map[string]manifest.Entry, src, dst, content, status string) manifest.Entry {
	t.Helper()
	e, ok := rows[src]
	if !ok {
		t.Fatalf("no row for %s; rows: %v", src, rows)
	}
	if e.DestPath != dst || e.SHA256 != sha(content) || e.Size != int64(len(content)) || e.Status != status {
		t.Errorf("row %s = {dest %q sha %s… size %d status %q}, want {dest %q sha %s… size %d status %q}",
			src, e.DestPath, e.SHA256[:12], e.Size, e.Status, dst, sha(content)[:12], len(content), status)
	}
	if status == "verified" && e.VerifiedAt == 0 {
		t.Errorf("row %s is verified with verified_at = 0", src)
	}
	if status == "copied" && e.VerifiedAt != 0 {
		t.Errorf("row %s is copied but carries verified_at = %d", src, e.VerifiedAt)
	}
	return e
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
	cfg, src, dst := t.TempDir(), t.TempDir(), t.TempDir()
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
	rows := rowsOf(t, cfg, "cam")
	if len(rows) != 3 {
		t.Fatalf("rows after copy = %d, want 3", len(rows))
	}
	wantRow(t, rows, "DCIM/C0001.MP4", "Videos/C0001.MP4", "clip one", "copied")
	wantRow(t, rows, "DCIM/C0002.MP4", "Videos/C0002.MP4", "clip two", "copied")
	wantRow(t, rows, "DCIM/C0001.JPG", "Photos/C0001.JPG", "still", "copied")

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
	verified := rowsOf(t, cfg, "cam")
	wantRow(t, verified, "DCIM/C0001.MP4", "Videos/C0001.MP4", "clip one", "verified")
	wantRow(t, verified, "DCIM/C0002.MP4", "Videos/C0002.MP4", "clip two", "verified")
	wantRow(t, verified, "DCIM/C0001.JPG", "Photos/C0001.JPG", "still", "verified")
	for src, e := range verified {
		if e.SHA256 != rows[src].SHA256 || e.CopiedAt != rows[src].CopiedAt {
			t.Errorf("verify changed sha/copied_at of %s: %+v vs %+v", src, e, rows[src])
		}
	}

	certPath := filepath.Join(t.TempDir(), "cam.cert.json")
	_, _, code = vault(t, cfg, "certify", "cam", certPath)
	if code != 0 {
		t.Fatalf("certify: exit %d", code)
	}
	// certify is read-only on the manifest: the persisted rows are the same
	// bytes before and after, and the certificate's file refs are those rows.
	if after := rowsOf(t, cfg, "cam"); !reflect.DeepEqual(after, verified) {
		t.Errorf("certify changed manifest rows:\n before %+v\n after  %+v", verified, after)
	}
	var cert certify.Certificate
	if err := json.Unmarshal([]byte(readFile(t, certPath)), &cert); err != nil {
		t.Fatal(err)
	}
	if cert.FileCount != 3 || cert.TotalBytes != int64(len("clip one")+len("clip two")+len("still")) || cert.SourceDisk != "cam" {
		t.Errorf("cert = %+v", cert)
	}
	if len(cert.Files) != len(verified) {
		t.Fatalf("cert lists %d files, manifest has %d rows", len(cert.Files), len(verified))
	}
	for _, f := range cert.Files {
		e, ok := verified[f.SourcePath]
		if !ok {
			t.Errorf("cert lists %s, which has no manifest row", f.SourcePath)
			continue
		}
		if f.DestPath != e.DestPath || f.SHA256 != e.SHA256 || f.Size != e.Size ||
			!f.VerifiedAt.Equal(time.Unix(0, e.VerifiedAt)) {
			t.Errorf("cert ref %+v disagrees with row %+v", f, e)
		}
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
	recopied := rowsOf(t, cfg, "cam")
	wantRow(t, recopied, "DCIM/C0001.MP4", "Videos/C0001.MP4", "clip one, re-exported", "copied")
	if recopied["DCIM/C0001.MP4"].MtimeNs != t0.Add(time.Hour).UnixNano() {
		t.Errorf("recopied row mtime = %d, want the new source mtime", recopied["DCIM/C0001.MP4"].MtimeNs)
	}
	for _, src := range []string{"DCIM/C0002.MP4", "DCIM/C0001.JPG"} {
		if !reflect.DeepEqual(recopied[src], verified[src]) {
			t.Errorf("recopy touched an unrelated row %s: %+v vs %+v", src, recopied[src], verified[src])
		}
	}
	if _, _, code = vault(t, cfg, "certify", "cam"); code != 1 {
		t.Fatalf("certify after recopy: exit %d, want 1", code)
	}
	if _, _, code = vault(t, cfg, "verify", "cam", dst); code != 0 {
		t.Fatalf("verify after recopy: exit %d", code)
	}
	reverified := rowsOf(t, cfg, "cam")
	e := wantRow(t, reverified, "DCIM/C0001.MP4", "Videos/C0001.MP4", "clip one, re-exported", "verified")
	if e.VerifiedAt <= verified["DCIM/C0001.MP4"].VerifiedAt {
		t.Errorf("re-verify did not advance verified_at: %d → %d", verified["DCIM/C0001.MP4"].VerifiedAt, e.VerifiedAt)
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
	// The row keeps the sha the file HAD (the certificate's claim), flips to
	// mismatch, and stamps when the mismatch was seen. Nothing else moves.
	mismatched := rowsOf(t, cfg, "cam")
	e = wantRow(t, mismatched, "DCIM/C0002.MP4", "Videos/C0002.MP4", "clip two", "mismatch")
	if e.VerifiedAt <= reverified["DCIM/C0002.MP4"].VerifiedAt {
		t.Errorf("mismatch did not stamp verified_at: %d → %d", reverified["DCIM/C0002.MP4"].VerifiedAt, e.VerifiedAt)
	}
	for _, src := range []string{"DCIM/C0001.MP4", "DCIM/C0001.JPG"} {
		if mismatched[src].Status != "verified" {
			t.Errorf("row %s = %q after an unrelated mismatch, want verified", src, mismatched[src].Status)
		}
	}
	_, errOut, code = vault(t, cfg, "certify", "cam")
	if code != 1 || !strings.Contains(errOut, `"mismatch"`) {
		t.Fatalf("certify with mismatch: exit %d, stderr %q", code, errOut)
	}

	// A missing destination file fails verify but leaves its row untouched
	// (so a later copy can fix it): snapshot before, byte-identical after.
	before := rowsOf(t, cfg, "cam")["DCIM/C0001.JPG"]
	if err := os.Remove(filepath.Join(dst, "Photos", "C0001.JPG")); err != nil {
		t.Fatal(err)
	}
	out, _, code = vault(t, cfg, "verify", "cam", dst)
	if code != 1 || !strings.Contains(out, "missing") || !strings.Contains(out, "Missing: 1") {
		t.Fatalf("verify with missing: exit %d", code)
	}
	if after := rowsOf(t, cfg, "cam")["DCIM/C0001.JPG"]; !reflect.DeepEqual(after, before) {
		t.Errorf("missing-file verify touched the row:\n before %+v\n after  %+v", before, after)
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
			src, dst := t.TempDir(), t.TempDir()
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
	src, dst := t.TempDir(), t.TempDir()
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
	cfg, src, dst := t.TempDir(), t.TempDir(), t.TempDir()
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

// TestExitCodeOrdering pins the flag-vs-arity precedence CLAUDE.md documents
// (review #3, finding 4): scan/copy validate every flag value before arity,
// move validates --on-collision and missing values before arity but --rule
// values only after it. Wrong arity, so no scan or plan ever runs.
func TestExitCodeOrdering(t *testing.T) {
	cfg := t.TempDir()
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"unknown command", []string{"frobnicate"}, 2},
		{"copy wrong arity", []string{"copy", "diskA", "src"}, 2},
		{"copy bad --rule, wrong arity", []string{"copy", "--rule", "bad", "diskA", "src"}, 1},
		{"copy bad --on-collision, wrong arity", []string{"copy", "--on-collision", "bad", "diskA", "src"}, 1},
		{"scan bad --rule, wrong arity", []string{"scan", "--rule", "bad", "diskA", "src"}, 1},
		{"scan --prefix without value", []string{"scan", "diskA", "src", "--prefix"}, 1},
		{"move wrong arity", []string{"move", "a", "b", "c"}, 2},
		{"move bad --on-collision, wrong arity", []string{"move", "--on-collision", "bad", "a", "b", "c"}, 1},
		{"move --rule without value, wrong arity", []string{"move", "a", "b", "c", "--rule"}, 1},
		{"move bad --rule, wrong arity", []string{"move", "--rule", "bad", "a", "b", "c"}, 2},
		{"move bad --rule, right arity", []string{"move", "--rule", "bad", "a", "b", t.TempDir(), t.TempDir()}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, code := vault(t, cfg, c.args...); code != c.want {
				t.Errorf("vault %v: exit %d, want %d", c.args, code, c.want)
			}
		})
	}
}
