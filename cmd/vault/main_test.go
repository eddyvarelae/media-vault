package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	// The zero counts print on an ordinary run too (review #5, finding 3).
	if !strings.Contains(out, "Verified, changed: 0") || !strings.Contains(out, "Dst owned:        0") {
		t.Errorf("first copy did not print the zero counts:\n%s", out)
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

	// Recopy: the source changes size AND mtime while its row is still
	// `copied` (nothing has attested the destination yet), so scan plans
	// exactly one recopy and copy replaces the destination; the row takes
	// the new hash and mtime, the other rows do not move.
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
		if !reflect.DeepEqual(recopied[src], rows[src]) {
			t.Errorf("recopy touched an unrelated row %s: %+v vs %+v", src, recopied[src], rows[src])
		}
	}
	rows = recopied

	out, _, code = vault(t, cfg, "verify", "cam", dst)
	if code != 0 || !strings.Contains(out, "Verified: 3   Mismatch: 0   Missing: 0   Errors: 0") {
		t.Fatalf("verify: exit %d", code)
	}
	verified := rowsOf(t, cfg, "cam")
	wantRow(t, verified, "DCIM/C0001.MP4", "Videos/C0001.MP4", "clip one, re-exported", "verified")
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
	if cert.FileCount != 3 || cert.TotalBytes != int64(len("clip one, re-exported")+len("clip two")+len("still")) || cert.SourceDisk != "cam" {
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

	// A verified row whose source was merely touched (same bytes, new
	// mtime) is not a change: scan hashes it, reports it retouched, and copy
	// has nothing to do. No row moves, certify still passes.
	writeFile(t, filepath.Join(src, "DCIM", "C0001.JPG"), "still", t0.Add(2*time.Hour))
	out, _, code = vault(t, cfg, "scan", "cam", src, dst, "--prefix", "DCIM", "--rule", "MP4=Videos", "--rule", "JPG=Photos")
	if code != 0 || !strings.Contains(out, "Retouched:        1") || !strings.Contains(out, "Files to recopy:  0") || !strings.Contains(out, "Verified, changed: 0") {
		t.Fatalf("scan after mtime-only touch: exit %d\n%s", code, out)
	}
	out, _, code = vault(t, cfg, "copy", "cam", src, dst, "--prefix", "DCIM", "--rule", "MP4=Videos", "--rule", "JPG=Photos")
	if code != 0 || !strings.Contains(out, "Nothing to copy") || !strings.Contains(out, "Retouched:        1") || !strings.Contains(out, "Verified, changed: 0") {
		t.Fatalf("copy after mtime-only touch: exit %d\n%s", code, out)
	}
	if got := rowsOf(t, cfg, "cam"); !reflect.DeepEqual(got, verified) {
		t.Errorf("mtime-only touch moved a row:\n before %+v\n after  %+v", verified, got)
	}

	// A verified row whose source now holds different bytes is the B23(b)
	// case (TestVerifiedDestinationNeverOverwritten has the full assertion
	// set): never recopied, named in INCOMPLETE:, exit 1, destination and
	// row exactly as certified, certify still passes.
	writeFile(t, filepath.Join(src, "DCIM", "C0001.MP4"), "clip one, re-exported AGAIN", t0.Add(3*time.Hour))
	out, _, code = vault(t, cfg, "scan", "cam", src, dst, "--prefix", "DCIM", "--rule", "MP4=Videos", "--rule", "JPG=Photos")
	if code != 0 || !strings.Contains(out, "Verified, changed: 1") || !strings.Contains(out, "Files to recopy:  0") {
		t.Fatalf("scan after change under a verified row: exit %d\n%s", code, out)
	}
	_, errOut, code = vault(t, cfg, "copy", "cam", src, dst, "--prefix", "DCIM", "--rule", "MP4=Videos", "--rule", "JPG=Photos")
	if code != 1 || !strings.Contains(errOut, "INCOMPLETE: 1 file(s) skipped because their verified archive copy holds different content (kept)") {
		t.Fatalf("copy over a verified row: exit %d, stderr %q", code, errOut)
	}
	if got := readFile(t, filepath.Join(dst, "Videos", "C0001.MP4")); got != "clip one, re-exported" {
		t.Errorf("verified destination overwritten: %q", got)
	}
	if got := rowsOf(t, cfg, "cam"); !reflect.DeepEqual(got, verified) {
		t.Errorf("refused copy moved a row:\n before %+v\n after  %+v", verified, got)
	}
	if _, _, code = vault(t, cfg, "certify", "cam"); code != 0 {
		t.Fatalf("certify after the refused copy: exit %d", code)
	}
	reverified := verified

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
	e := wantRow(t, mismatched, "DCIM/C0002.MP4", "Videos/C0002.MP4", "clip two", "mismatch")
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
			stdout:  []string{"(dry-run; 1 files would be copied, 0 recorded as deduped, 1 collisions skipped, 0 verified kept, 0 owned destinations skipped, 0 through symlinks skipped)"},
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

// TestVerifiedDestinationNeverOverwritten is B23(b), the defect behind the
// 2,668 SonyA6700 photos lost on 2026-09-01: the camera's counter wrapped, a
// second card carried different photos under the same DCIM names, and a copy
// run under the same <disk> name treated them as changed files - recopy
// replaced the verified destinations in place, no collision policy was
// consulted, and the old hashes left the manifest with the bytes.
//
// Decided 2026-09-17 (DECISIONS.md): a verified destination is never
// overwritten. Same (source_disk, source_path), different content is a
// collision: the archived copy and its row stay exactly as they were, the
// file is skipped and counted in INCOMPLETE:, exit 1.
func TestVerifiedDestinationNeverOverwritten(t *testing.T) {
	for _, policy := range []string{"skip", "rename-mtime-year"} {
		t.Run(policy, func(t *testing.T) {
			cfg, src, dst := t.TempDir(), t.TempDir(), t.TempDir()
			const april = "DSC06245 as shot in April - the certified bytes"
			const sept = "DSC06245 as shot in September"
			writeFile(t, filepath.Join(src, "DCIM", "DSC06245.ARW"), april, t0)
			writeFile(t, filepath.Join(src, "DCIM", "DSC06246.ARW"), "untouched sibling", t0)

			if _, _, code := vault(t, cfg, "copy", "sony", src, dst); code != 0 {
				t.Fatalf("first copy: exit %d", code)
			}
			if _, _, code := vault(t, cfg, "verify", "sony", dst); code != 0 {
				t.Fatalf("verify: exit %d", code)
			}
			if _, _, code := vault(t, cfg, "certify", "sony"); code != 0 {
				t.Fatalf("certify: exit %d", code)
			}
			before := rowsOf(t, cfg, "sony")
			wantRow(t, before, "DCIM/DSC06245.ARW", "DCIM/DSC06245.ARW", april, "verified")

			// The second card: same name, different (smaller) bytes, later mtime.
			writeFile(t, filepath.Join(src, "DCIM", "DSC06245.ARW"), sept, t0.AddDate(0, 5, 0))

			_, errOut, code := vault(t, cfg, "copy", "sony", src, dst, "--on-collision", policy)
			if got := readFile(t, filepath.Join(dst, "DCIM", "DSC06245.ARW")); got != april {
				t.Errorf("verified destination was overwritten: now %q, want the April bytes", got)
			}
			after := rowsOf(t, cfg, "sony")
			if !reflect.DeepEqual(after["DCIM/DSC06245.ARW"], before["DCIM/DSC06245.ARW"]) {
				t.Errorf("verified row changed:\n before %+v\n after  %+v", before["DCIM/DSC06245.ARW"], after["DCIM/DSC06245.ARW"])
			}
			if !reflect.DeepEqual(after["DCIM/DSC06246.ARW"], before["DCIM/DSC06246.ARW"]) {
				t.Errorf("unrelated row changed: %+v", after["DCIM/DSC06246.ARW"])
			}
			if code != 1 || !strings.Contains(errOut, "INCOMPLETE:") || !strings.Contains(errOut, "verified") {
				t.Errorf("exit %d, stderr %q; want 1 with INCOMPLETE: naming the verified-row skip", code, errOut)
			}
			noPartials(t, dst)
			// The certificate's claim still holds: verify and certify pass.
			if _, _, code := vault(t, cfg, "verify", "sony", dst); code != 0 {
				t.Errorf("verify after the refused copy: exit %d, want 0", code)
			}
			if _, _, code := vault(t, cfg, "certify", "sony"); code != 0 {
				t.Errorf("certify after the refused copy: exit %d, want 0", code)
			}
		})
	}
}

// TestVerifiedDestinationOwnedByAnotherRow is review #5 finding 1: the
// guard is by destination, not by source row. Every path copy.File would
// write - the final name and the .vault-partial staging name - is refused
// when a verified row of any disk records it as its archived copy.
func TestVerifiedDestinationOwnedByAnotherRow(t *testing.T) {
	// The Reviewer's scenario: disk A's x.mov is verified; disk B's identical
	// x.mov is copied with --dedupe-content and lands as a deduped row
	// pointing at A's file; B's source then changes and B is copied again.
	// B's row is not verified, so the source-row check does not fire - but
	// the recopy's destination is A's certified file.
	t.Run("deduped row recopy over another disk's verified file", func(t *testing.T) {
		cfg, srcA, srcB, dst := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(srcA, "x.mov"), "the clip", t0)
		writeFile(t, filepath.Join(srcB, "x.mov"), "the clip", t0)
		if _, _, code := vault(t, cfg, "copy", "A", srcA, dst); code != 0 {
			t.Fatalf("copy A: exit %d", code)
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Fatalf("verify A: exit %d", code)
		}
		out, _, code := vault(t, cfg, "copy", "B", srcB, dst, "--dedupe-content")
		if code != 0 || !strings.Contains(out, "Recorded 1 already-archived files") {
			t.Fatalf("copy B deduped: exit %d\n%s", code, out)
		}
		wantRow(t, rowsOf(t, cfg, "B"), "x.mov", "x.mov", "the clip", "deduped")
		aBefore := rowsOf(t, cfg, "A")

		writeFile(t, filepath.Join(srcB, "x.mov"), "B re-exported, longer", t0.Add(time.Hour))
		for _, policy := range []string{"skip", "rename-mtime-year"} {
			for _, extra := range [][]string{nil, {"--dedupe-content"}} {
				args := append([]string{"copy", "B", srcB, dst, "--on-collision", policy}, extra...)
				out, errOut, code := vault(t, cfg, args...)
				if got := readFile(t, filepath.Join(dst, "x.mov")); got != "the clip" {
					t.Fatalf("%v: A's verified file overwritten: %q", args[4:], got)
				}
				if code != 1 || !strings.Contains(errOut, "1 file(s) skipped because a verified row owns their destination path") {
					t.Errorf("%v: exit %d, stderr %q", args[4:], code, errOut)
				}
				if !strings.Contains(out, "Dst owned:        1") {
					t.Errorf("%v: count not reported:\n%s", args[4:], out)
				}
				if got := rowsOf(t, cfg, "A"); !reflect.DeepEqual(got, aBefore) {
					t.Errorf("%v: A's row changed: %+v", args[4:], got)
				}
				noPartials(t, dst)
			}
		}
		// The dry-run names the owner.
		out, _, code = vault(t, cfg, "copy", "B", srcB, dst, "--dry-run")
		if code != 0 || !strings.Contains(out, "x.mov → x.mov (owned by A:x.mov)") {
			t.Errorf("dry-run: exit %d\n%s", code, out)
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Errorf("verify A afterwards: exit %d", code)
		}
	})

	// An archived file that happens to be named like a staging file: a new
	// x.mov would open x.mov.vault-partial with O_TRUNC before anything
	// else - the certified bytes gone before the copy even starts.
	t.Run("new file whose staging path is a verified file", func(t *testing.T) {
		cfg, srcA, srcB, dst := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(srcA, "x.mov.vault-partial"), "archived under an unlucky name", t0)
		writeFile(t, filepath.Join(srcB, "x.mov"), "new clip", t0)
		if _, _, code := vault(t, cfg, "copy", "A", srcA, dst); code != 0 {
			t.Fatalf("copy A: exit %d", code)
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Fatalf("verify A: exit %d", code)
		}
		for _, policy := range []string{"skip", "rename-mtime-year"} {
			_, errOut, code := vault(t, cfg, "copy", "B", srcB, dst, "--on-collision", policy)
			if got := readFile(t, filepath.Join(dst, "x.mov.vault-partial")); got != "archived under an unlucky name" {
				t.Fatalf("%s: verified file truncated as a staging file: %q", policy, got)
			}
			if code != 1 || !strings.Contains(errOut, "verified row owns their destination path") {
				t.Errorf("%s: exit %d, stderr %q", policy, code, errOut)
			}
			if _, err := os.Stat(filepath.Join(dst, "x.mov")); err == nil {
				t.Errorf("%s: x.mov was written anyway", policy)
			}
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Errorf("verify A afterwards: exit %d", code)
		}
	})

	// A verified row whose file is missing still owns its path: writing new
	// bytes there would put a mismatch under a certified hash. The file is
	// absent, so the on-disk collision check sees nothing - only the
	// manifest knows.
	t.Run("new file at a verified row's missing destination", func(t *testing.T) {
		cfg, srcA, srcB, dst := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(srcA, "x.mov"), "the clip", t0)
		writeFile(t, filepath.Join(srcB, "x.mov"), "other bytes", t0)
		if _, _, code := vault(t, cfg, "copy", "A", srcA, dst); code != 0 {
			t.Fatalf("copy A: exit %d", code)
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Fatalf("verify A: exit %d", code)
		}
		if err := os.Remove(filepath.Join(dst, "x.mov")); err != nil {
			t.Fatal(err)
		}
		_, errOut, code := vault(t, cfg, "copy", "B", srcB, dst)
		if code != 1 || !strings.Contains(errOut, "verified row owns their destination path") {
			t.Errorf("exit %d, stderr %q", code, errOut)
		}
		if _, err := os.Stat(filepath.Join(dst, "x.mov")); err == nil {
			t.Errorf("x.mov was written under A's verified row")
		}
		if len(rowsOf(t, cfg, "B")) != 0 {
			t.Errorf("B got a row for a file that was not written")
		}
	})

	// The rename policy resolves an on-disk collision, but the renamed path
	// is checked against the manifest too. While A's x_2023.mov is on disk
	// the ordinary collision check refuses it; once it is missing, only the
	// verified row knows the path is spoken for.
	t.Run("renamed destination owned by a verified row", func(t *testing.T) {
		cfg, srcA, srcB, dst := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(srcA, "x_2023.mov"), "archived renamed", t0)
		writeFile(t, filepath.Join(srcB, "x.mov"), "new clip", t0)
		writeFile(t, filepath.Join(dst, "x.mov"), "foreign", t0)
		if _, _, code := vault(t, cfg, "copy", "A", srcA, dst); code != 0 {
			t.Fatalf("copy A: exit %d", code)
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Fatalf("verify A: exit %d", code)
		}
		_, errOut, code := vault(t, cfg, "copy", "B", srcB, dst, "--on-collision", "rename-mtime-year")
		if code != 1 || !strings.Contains(errOut, "unresolved destination collisions") {
			t.Errorf("with the file present: exit %d, stderr %q", code, errOut)
		}
		if err := os.Remove(filepath.Join(dst, "x_2023.mov")); err != nil {
			t.Fatal(err)
		}
		_, errOut, code = vault(t, cfg, "copy", "B", srcB, dst, "--on-collision", "rename-mtime-year")
		if code != 1 || !strings.Contains(errOut, "verified row owns their destination path") {
			t.Errorf("with the file missing: exit %d, stderr %q", code, errOut)
		}
		if _, err := os.Stat(filepath.Join(dst, "x_2023.mov")); err == nil {
			t.Errorf("x_2023.mov was written under A's verified row")
		}
		if len(rowsOf(t, cfg, "B")) != 0 {
			t.Errorf("B got a row for a file that was not written")
		}
	})
}

// TestVerifiedDestinationAliases is review #6: ownership is by physical
// location. A verified file reachable under another spelling - a case
// alias on a folding filesystem, a `..` in a routing rule - must still
// refuse the write. The plan's fold is unconditional, so the refusal is
// expected whatever the temp filesystem does; the writer's own Lstat is
// the second line on a folding filesystem (internal/copy tests it).
func TestVerifiedDestinationAliases(t *testing.T) {
	t.Run("case alias of a verified staging-named file", func(t *testing.T) {
		cfg, srcA, srcB, dst := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(srcA, "X.mov.vault-partial"), "archived, capital X", t0)
		writeFile(t, filepath.Join(srcB, "x.mov"), "new clip", t0)
		if _, _, code := vault(t, cfg, "copy", "A", srcA, dst); code != 0 {
			t.Fatalf("copy A: exit %d", code)
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Fatalf("verify A: exit %d", code)
		}
		for _, policy := range []string{"skip", "rename-mtime-year"} {
			out, errOut, code := vault(t, cfg, "copy", "B", srcB, dst, "--on-collision", policy)
			if got := readFile(t, filepath.Join(dst, "X.mov.vault-partial")); got != "archived, capital X" {
				t.Fatalf("%s: verified file destroyed through its case alias: %q", policy, got)
			}
			if code != 1 || !strings.Contains(errOut, "verified row owns their destination path") {
				t.Errorf("%s: exit %d, stderr %q", policy, code, errOut)
			}
			if !strings.Contains(out, "Dst owned:        1") {
				t.Errorf("%s: not reported as owned:\n%s", policy, out)
			}
			if _, err := os.Stat(filepath.Join(dst, "x.mov")); err == nil {
				t.Errorf("%s: x.mov written", policy)
			}
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Errorf("verify A afterwards: exit %d", code)
		}
	})

	// --rule vault-partial=../archive: A's file is stored as
	// ../archive/x.mov.vault-partial relative to base/archive, which is
	// base/archive/x.mov.vault-partial on disk - exactly B's staging path.
	t.Run("dot-dot in a routing rule", func(t *testing.T) {
		cfg, srcA, srcB, base := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		dst := filepath.Join(base, "archive")
		writeFile(t, filepath.Join(srcA, "x.mov.vault-partial"), "archived via a .. rule", t0)
		writeFile(t, filepath.Join(srcB, "x.mov"), "new clip", t0)
		if _, _, code := vault(t, cfg, "copy", "A", srcA, dst, "--rule", "vault-partial=../archive"); code != 0 {
			t.Fatalf("copy A: exit %d", code)
		}
		if got := rowsOf(t, cfg, "A")["x.mov.vault-partial"].DestPath; got != "../archive/x.mov.vault-partial" {
			t.Fatalf("A's stored dest_path = %q; the test needs the .. spelling", got)
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Fatalf("verify A: exit %d", code)
		}
		_, errOut, code := vault(t, cfg, "copy", "B", srcB, dst)
		if got := readFile(t, filepath.Join(dst, "x.mov.vault-partial")); got != "archived via a .. rule" {
			t.Fatalf("verified file destroyed through its .. spelling: %q", got)
		}
		if code != 1 || !strings.Contains(errOut, "verified row owns their destination path") {
			t.Errorf("exit %d, stderr %q", code, errOut)
		}
		if _, err := os.Stat(filepath.Join(dst, "x.mov")); err == nil {
			t.Errorf("x.mov written")
		}
	})

	// A leftover .vault-partial that no row owns: the plan lets the task
	// through, the writer refuses it as a FAIL naming the path, and the run
	// is INCOMPLETE. The leftover is not removed.
	t.Run("unowned leftover partial", func(t *testing.T) {
		cfg, src, dst := t.TempDir(), t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(src, "x.mov"), "new clip", t0)
		writeFile(t, filepath.Join(dst, "x.mov.vault-partial"), "half of somethi", t0)
		out, errOut, code := vault(t, cfg, "copy", "B", src, dst)
		if code != 1 || !strings.Contains(errOut, "INCOMPLETE: 1 file(s) failed to copy") || !strings.Contains(out, "interrupted run") {
			t.Errorf("exit %d, stderr %q\n%s", code, errOut, out)
		}
		if got := readFile(t, filepath.Join(dst, "x.mov.vault-partial")); got != "half of somethi" {
			t.Errorf("leftover touched: %q", got)
		}
		if len(rowsOf(t, cfg, "B")) != 0 {
			t.Errorf("row written for a refused file")
		}
	})
}

// TestDestinationThroughSymlink is review #9: a symlinked directory under
// the root makes two spellings one file. The Reviewer's case: A's verified
// real/x.mov, dst/alias -> real, B's deduped row for alias/x.mov whose
// source then changes - the recopy's key is dst/alias/x.mov, its bytes
// would land on dst/real/x.mov.
func TestDestinationThroughSymlink(t *testing.T) {
	t.Run("recopy through an aliased directory onto another disk's verified file", func(t *testing.T) {
		cfg, srcA, srcB, dst := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(srcA, "real", "x.mov"), "the clip", t0)
		writeFile(t, filepath.Join(srcB, "alias", "x.mov"), "the clip", t0)
		if _, _, code := vault(t, cfg, "copy", "A", srcA, dst); code != 0 {
			t.Fatalf("copy A: exit %d", code)
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Fatalf("verify A: exit %d", code)
		}
		if err := os.Symlink(filepath.Join(dst, "real"), filepath.Join(dst, "alias")); err != nil {
			t.Fatal(err)
		}
		out, _, code := vault(t, cfg, "copy", "B", srcB, dst, "--dedupe-content")
		if code != 0 || !strings.Contains(out, "Recorded 1 already-archived files") {
			t.Fatalf("copy B deduped: exit %d\n%s", code, out)
		}
		wantRow(t, rowsOf(t, cfg, "B"), "alias/x.mov", "real/x.mov", "the clip", "deduped")
		aBefore := rowsOf(t, cfg, "A")

		writeFile(t, filepath.Join(srcB, "alias", "x.mov"), "B re-exported, longer", t0.Add(time.Hour))
		for _, policy := range []string{"skip", "rename-mtime-year"} {
			out, errOut, code := vault(t, cfg, "copy", "B", srcB, dst, "--on-collision", policy)
			if got := readFile(t, filepath.Join(dst, "real", "x.mov")); got != "the clip" {
				t.Fatalf("%s: A's verified file overwritten through the alias: %q", policy, got)
			}
			if code != 1 || !strings.Contains(errOut, "1 file(s) skipped because their destination path passes through a symlink") {
				t.Errorf("%s: exit %d, stderr %q", policy, code, errOut)
			}
			if !strings.Contains(out, "Dst via symlink:  1") {
				t.Errorf("%s: not reported:\n%s", policy, out)
			}
			if got := rowsOf(t, cfg, "A"); !reflect.DeepEqual(got, aBefore) {
				t.Errorf("%s: A's row changed", policy)
			}
			noPartials(t, filepath.Join(dst, "real"))
		}
		out, _, code = vault(t, cfg, "copy", "B", srcB, dst, "--dry-run")
		if code != 0 || !strings.Contains(out, "alias/x.mov → alias/x.mov (alias is a symlink)") {
			t.Errorf("dry-run: exit %d\n%s", code, out)
		}
		if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
			t.Errorf("verify A afterwards: exit %d", code)
		}
	})

	// A brand-new file whose path passes through a link is refused too, at
	// any depth: the write would land somewhere other than its spelling.
	t.Run("new file through a nested aliased directory", func(t *testing.T) {
		cfg, src, dst, elsewhere := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(src, "DCIM", "100MSDCF", "y.mov"), "new clip", t0)
		if err := os.MkdirAll(filepath.Join(dst, "DCIM"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(dst, "DCIM", "100MSDCF")); err != nil {
			t.Fatal(err)
		}
		_, errOut, code := vault(t, cfg, "copy", "B", src, dst)
		if code != 1 || !strings.Contains(errOut, "passes through a symlink") {
			t.Errorf("exit %d, stderr %q", code, errOut)
		}
		if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
			t.Errorf("wrote through the link: %v", entries)
		}
		if len(rowsOf(t, cfg, "B")) != 0 {
			t.Errorf("row written for a refused file")
		}
	})
}

// TestRepairDest is B24 end to end: rows whose dest_path lost its directory
// (the 195 media-sonya6700 rows) are `missing` to verify; repair-dest finds
// the file one level down, proves it by size + sha256, rewrites only
// dest_path; verify then promotes and certify passes. --dry-run prints the
// same plan and writes nothing.
func TestRepairDest(t *testing.T) {
	cfg, src, dst := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "C0001.XML"), "clip metadata", t0)
	writeFile(t, filepath.Join(src, "DSC0001.JPG"), "a photo", t0)
	writeFile(t, filepath.Join(src, "DSC0002.JPG"), "photo two", t0)
	writeFile(t, filepath.Join(src, "OK.MOV"), "fine", t0)
	writeFile(t, filepath.Join(src, "DIR.MOV"), "became a dir", t0)
	if _, _, code := vault(t, cfg, "copy", "sony", src, dst); code != 0 {
		t.Fatalf("copy: exit %d", code)
	}
	// Reproduce the shape: the files live one directory down, the rows do
	// not know. DSC0002's bytes on disk differ from its row - not repairable.
	// DIR.MOV's destination is a directory now (review #7 finding 2): not
	// intact, not repairable, and the run must say so rather than exit 0.
	if err := os.Remove(filepath.Join(dst, "DIR.MOV")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dst, "DIR.MOV"), 0o755); err != nil {
		t.Fatal(err)
	}
	mv := func(name, sub string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dst, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(dst, name), filepath.Join(dst, sub, name)); err != nil {
			t.Fatal(err)
		}
	}
	mv("C0001.XML", "CLIP")
	mv("DSC0001.JPG", "DCIM")
	mv("DSC0002.JPG", "DCIM")
	writeFile(t, filepath.Join(dst, "DCIM", "DSC0002.JPG"), "photo TWO", t0)

	out, _, code := vault(t, cfg, "verify", "sony", dst)
	if code != 1 || !strings.Contains(out, "Missing: 3") {
		t.Fatalf("verify before repair: exit %d\n%s", code, out)
	}
	before := rowsOf(t, cfg, "sony")

	out, _, code = vault(t, cfg, "repair-dest", "sony", dst, "--dry-run")
	if code != 0 || !strings.Contains(out, "(dry-run; no manifest row written, and repair-dest never writes archive files)") {
		t.Fatalf("dry-run: exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"5 rows with a dest_path, 1 intact, 4 unresolved (0 rows have no dest_path — located by source_path, any status — and were not examined)",
		"REPAIR     C0001.XML : C0001.XML → CLIP/C0001.XML",
		"REPAIR     DSC0001.JPG : DSC0001.JPG → DCIM/DSC0001.JPG",
		"NOT FOUND  DSC0002.JPG : DSC0002.JPG",
		"NOT A FILE DIR.MOV : DIR.MOV is a directory",
		"Repairable: 2   Not found: 1   Ambiguous: 0   Owned: 0   Not a file: 1   Unsafe: 0   Conflict: 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
	}
	if got := rowsOf(t, cfg, "sony"); !reflect.DeepEqual(got, before) {
		t.Errorf("dry-run wrote rows:\n before %+v\n after  %+v", before, got)
	}

	out, errOut, code := vault(t, cfg, "repair-dest", "sony", dst)
	if code != 1 || !strings.Contains(errOut, "INCOMPLETE: 2 row(s) still without a destination this tool can back with a hash (1 not found, 0 ambiguous, 0 owned by another row, 1 not a file, 0 unsafe, 0 in conflict)") {
		t.Fatalf("repair: exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "repaired  C0001.XML : C0001.XML → CLIP/C0001.XML") || !strings.Contains(out, "Repaired 2 row(s). Status untouched") {
		t.Errorf("repair output:\n%s", out)
	}
	after := rowsOf(t, cfg, "sony")
	for src, b := range before {
		a := after[src]
		switch src {
		case "C0001.XML":
			b.DestPath = "CLIP/C0001.XML"
		case "DSC0001.JPG":
			b.DestPath = "DCIM/DSC0001.JPG"
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: row changed beyond dest_path:\n want %+v\n got  %+v", src, b, a)
		}
	}

	// A second run has nothing left to repair but the same unrepairable rows.
	_, errOut, code = vault(t, cfg, "repair-dest", "sony", dst)
	if code != 1 || !strings.Contains(errOut, "1 not found, 0 ambiguous, 0 owned by another row, 1 not a file, 0 unsafe, 0 in conflict") {
		t.Errorf("second repair: exit %d, stderr %q", code, errOut)
	}
	// verify now promotes the repaired rows; DSC0002 stays missing and the
	// directory is a read error.
	out, _, code = vault(t, cfg, "verify", "sony", dst)
	if code != 1 || !strings.Contains(out, "Verified: 3   Mismatch: 0   Missing: 1   Errors: 1") {
		t.Fatalf("verify after repair: exit %d\n%s", code, out)
	}
	// Restore both by hand (they were our own copies) and the disk certifies.
	writeFile(t, filepath.Join(dst, "DSC0002.JPG"), "photo two", t0)
	if err := os.Remove(filepath.Join(dst, "DIR.MOV")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dst, "DIR.MOV"), "became a dir", t0)
	if _, _, code = vault(t, cfg, "verify", "sony", dst); code != 0 {
		t.Fatalf("verify after restore: exit %d", code)
	}
	if _, _, code = vault(t, cfg, "certify", "sony"); code != 0 {
		t.Fatalf("certify: exit %d", code)
	}

	// Arity and flags.
	if _, _, code := vault(t, cfg, "repair-dest", "sony"); code != 2 {
		t.Errorf("wrong arity: exit %d, want 2", code)
	}
	if _, _, code := vault(t, cfg, "repair-dest", "sony", dst, "--bogus"); code != 1 {
		t.Errorf("unknown flag: exit %d, want 1", code)
	}
	if out, _, code := vault(t, cfg, "repair-dest", "nobody", dst); code != 0 || !strings.Contains(out, "0 rows with a dest_path") {
		t.Errorf("unknown disk: exit %d\n%s", code, out)
	}
}

// TestVerifyOnlyUnverified is the F4 "done when" list through main(): the
// flag anywhere among the positionals, wrong arity still 2, the skip
// announced with the newest single-row date (labeled as such), a bare
// verify still reading everything, and the incremental pass as the route
// to certify.
func TestVerifyOnlyUnverified(t *testing.T) {
	cfg, src, dst := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "a.mov"), "clip a", t0)
	writeFile(t, filepath.Join(src, "b.mov"), "clip bee", t0)
	if _, _, code := vault(t, cfg, "copy", "cam", src, dst); code != 0 {
		t.Fatalf("copy: exit %d", code)
	}
	// First pass with the flag on an all-copied disk: a full sweep, and
	// it says so.
	out, _, code := vault(t, cfg, "--only-unverified", "cam", dst)
	if code != 2 {
		t.Errorf("flag before the command name is not a command: exit %d, want 2", code)
	}
	out, _, code = vault(t, cfg, "verify", "--only-unverified", "cam", dst)
	if code != 0 || !strings.Contains(out, "(no verified rows to skip — this is a full sweep)") || !strings.Contains(out, "Verified: 2   Mismatch: 0   Missing: 0   Errors: 0   Bytes read: 14 B") {
		t.Fatalf("first incremental pass: exit %d\n%s", code, out)
	}
	first := rowsOf(t, cfg, "cam")
	newest := first["a.mov"].VerifiedAt
	if first["b.mov"].VerifiedAt > newest {
		newest = first["b.mov"].VerifiedAt
	}

	// New files arrive one at a time; each incremental pass reads only the
	// new one, and names what it skipped with the newest single row's
	// date, not a sweep date. The flag is accepted after or between the
	// positionals.
	writeFile(t, filepath.Join(dst, "a.mov"), "clip A", t0) // rot under a verified row, invisible to the incremental pass
	skipped := 2
	for i, args := range [][]string{
		{"verify", "cam", dst, "--only-unverified"},
		{"verify", "cam", "--only-unverified", dst},
	} {
		name := []string{"c.mov", "d.mov"}[i]
		writeFile(t, filepath.Join(src, name), "clip "+name[:1], t0)
		if _, _, code := vault(t, cfg, "copy", "cam", src, dst); code != 0 {
			t.Fatalf("copy %s: exit %d", name, code)
		}
		out, _, code := vault(t, cfg, args...)
		if code != 0 || !strings.Contains(out, "Verified: 1   Mismatch: 0   Missing: 0   Errors: 0   Bytes read: 6 B") {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
		for _, want := range []string{
			"Re-hashing ONLY unverified rows",
			fmt.Sprintf("skipping %d already-verified row(s) — NOT an integrity check.", skipped),
			"newest verified row: " + time.Unix(0, newest).Format("2006-01-02 15:04") + " (the newest single row, not a full-sweep date)",
			"verified   " + name,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%v: output missing %q:\n%s", args, want, out)
			}
		}
		if strings.Contains(out, "a.mov") || strings.Contains(out, "b.mov") {
			t.Errorf("%v: a verified row was listed:\n%s", args, out)
		}
		skipped++
		rows := rowsOf(t, cfg, "cam")
		for _, src := range []string{"a.mov", "b.mov"} {
			if !reflect.DeepEqual(rows[src], first[src]) {
				t.Errorf("incremental pass touched verified row %s", src)
			}
		}
		if rows[name].VerifiedAt > newest {
			newest = rows[name].VerifiedAt
		}
	}
	// The incremental pass promoted the last row: certify succeeds - and
	// attests a.mov's old hash, which is the documented cost of skipping.
	if _, _, code := vault(t, cfg, "certify", "cam"); code != 0 {
		t.Errorf("certify after the incremental pass: exit %d, want 0", code)
	}

	// Arity is still enforced with the flag present.
	if _, _, code := vault(t, cfg, "verify", "--only-unverified", "cam"); code != 2 {
		t.Errorf("wrong arity with the flag: exit %d, want 2", code)
	}
	if _, _, code := vault(t, cfg, "verify", "cam", dst, "extra", "--only-unverified"); code != 2 {
		t.Errorf("three positionals with the flag: exit %d, want 2", code)
	}

	// A bare verify reads all four and finds the rot.
	out, _, code = vault(t, cfg, "verify", "cam", dst)
	if code != 1 || !strings.Contains(out, "Re-hashing destination files") || !strings.Contains(out, "Verified: 3   Mismatch: 1   Missing: 0   Errors: 0   Bytes read: 26 B") || !strings.Contains(out, "MISMATCH   a.mov") {
		t.Fatalf("bare verify: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "skipping") {
		t.Errorf("bare verify claimed to skip something:\n%s", out)
	}
	// The mismatch row is in the next incremental pass (it is not
	// verified), so a fixed file gets promoted without a full sweep.
	writeFile(t, filepath.Join(dst, "a.mov"), "clip a", t0)
	out, _, code = vault(t, cfg, "verify", "cam", dst, "--only-unverified")
	if code != 0 || !strings.Contains(out, "skipping 3 already-verified row(s)") || !strings.Contains(out, "verified   a.mov") || !strings.Contains(out, "Bytes read: 6 B") {
		t.Fatalf("incremental pass over a mismatch row: exit %d\n%s", code, out)
	}
	if _, _, code := vault(t, cfg, "certify", "cam"); code != 0 {
		t.Errorf("certify after repairing the mismatch incrementally: exit %d", code)
	}
}

// TestDryRunTouchesNoConfig is B31: a --dry-run creates no config dir, no
// manifest file, and leaves an existing manifest byte-identical, for every
// command that takes the flag; it still plans against an empty manifest
// when none exists.
func TestDryRunTouchesNoConfig(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "a.mov"), "clip", t0)

	t.Run("no config yet: nothing is created", func(t *testing.T) {
		cfg := filepath.Join(t.TempDir(), "never-made")
		for _, args := range [][]string{
			{"copy", "cam", src, dst, "--dry-run"},
			{"repair-dest", "cam", dst, "--dry-run"},
			{"move", "cam", "cam2", src, dst, "--dry-run"},
		} {
			out, errOut, code := vault(t, cfg, args...)
			if code != 0 {
				t.Errorf("%v: exit %d\n%s%s", args, code, out, errOut)
			}
			if !strings.Contains(errOut, "planning against an empty one, creating nothing") {
				t.Errorf("%v: no notice that the manifest does not exist yet: %q", args, errOut)
			}
			if _, err := os.Stat(cfg); err == nil {
				t.Errorf("%v: config dir created", args)
			}
		}
		if out, _, code := vault(t, cfg, "copy", "cam", src, dst, "--dry-run"); code != 0 || !strings.Contains(out, "1 files would be copied") {
			t.Errorf("dry run against no manifest should plan the file as new: exit %d\n%s", code, out)
		}
		if _, err := os.Stat(filepath.Join(dst, "a.mov")); err == nil {
			t.Errorf("dry run copied the file")
		}
	})

	t.Run("existing manifest: read-only, byte-identical", func(t *testing.T) {
		cfg := t.TempDir()
		if _, _, code := vault(t, cfg, "copy", "cam", src, dst); code != 0 {
			t.Fatalf("copy: exit %d", code)
		}
		if _, _, code := vault(t, cfg, "verify", "cam", dst); code != 0 {
			t.Fatalf("verify: exit %d", code)
		}
		db := filepath.Join(cfg, "manifest.db")
		before := sha(readFile(t, db))
		beforeRows := rowsOf(t, cfg, "cam")
		writeFile(t, filepath.Join(src, "b.mov"), "new clip", t0)
		for _, args := range [][]string{
			{"copy", "cam", src, dst, "--dry-run"},
			{"copy", "cam", src, dst, "--dry-run", "--dedupe-content"},
			{"repair-dest", "cam", dst, "--dry-run"},
			{"move", "cam", "cam2", src, dst, "--dry-run"},
		} {
			if out, errOut, code := vault(t, cfg, args...); code != 0 {
				t.Errorf("%v: exit %d\n%s%s", args, code, out, errOut)
			}
		}
		if got := sha(readFile(t, db)); got != before {
			t.Errorf("manifest.db bytes changed under --dry-run")
		}
		if got := rowsOf(t, cfg, "cam"); !reflect.DeepEqual(got, beforeRows) {
			t.Errorf("rows changed under --dry-run")
		}
		// And a real run still opens it writable afterwards.
		if _, _, code := vault(t, cfg, "copy", "cam", src, dst); code != 0 {
			t.Errorf("real copy after dry runs: exit %d", code)
		}
	})

	t.Run("a command without the flag still initializes", func(t *testing.T) {
		cfg := filepath.Join(t.TempDir(), "made")
		if _, _, code := vault(t, cfg, "scan", "cam", src, dst); code != 0 {
			t.Fatalf("scan: exit %d", code)
		}
		if _, err := os.Stat(filepath.Join(cfg, "manifest.db")); err != nil {
			t.Errorf("scan did not create the manifest: %v", err)
		}
	})
}

// TestMoveNeverLandsOnAVerifiedDestination is B32: move consults the same
// owner index as copy. A verified row's missing file is still its path; a
// symlinked directory is not a destination.
func TestMoveNeverLandsOnAVerifiedDestination(t *testing.T) {
	cfg, srcA, srcB, dst := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(srcA, "x.mov"), "the clip", t0)
	if _, _, code := vault(t, cfg, "copy", "A", srcA, dst); code != 0 {
		t.Fatalf("copy A: exit %d", code)
	}
	if _, _, code := vault(t, cfg, "verify", "A", dst); code != 0 {
		t.Fatalf("verify A: exit %d", code)
	}
	if err := os.Remove(filepath.Join(dst, "x.mov")); err != nil { // the verified file is missing; the row still owns the path
		t.Fatal(err)
	}
	// B's rows: files under srcB, rows seeded as copied there.
	m, err := manifest.Open(filepath.Join(cfg, "manifest.db"))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"x.mov": "other bytes", "y.mov": "free bytes", "z.mov": "via link"} {
		writeFile(t, filepath.Join(srcB, name), content, t0)
		if err := m.Upsert(manifest.Entry{SourceDisk: "B", SourcePath: name, DestPath: name, Size: int64(len(content)),
			MtimeNs: t0.UnixNano(), SHA256: sha(content), CopiedAt: 1, Status: "copied"}); err != nil {
			t.Fatal(err)
		}
	}
	m.Close()
	if err := os.Symlink(t.TempDir(), filepath.Join(dst, "alias")); err != nil {
		t.Fatal(err)
	}
	aBefore := rowsOf(t, cfg, "A")

	out, _, _ := vault(t, cfg, "move", "B", "A", srcB, dst)
	if !strings.Contains(out, "dst-owned by verified row A:x.mov") {
		t.Errorf("x.mov should be refused as owned:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dst, "x.mov")); err == nil {
		t.Errorf("x.mov was written under A's verified row")
	}
	if got := readFile(t, filepath.Join(srcB, "x.mov")); got != "other bytes" {
		t.Errorf("source x.mov moved away: %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "y.mov")); got != "free bytes" {
		t.Errorf("y.mov should have moved:\n%s", out)
	}
	if got := rowsOf(t, cfg, "A"); !reflect.DeepEqual(got["x.mov"], aBefore["x.mov"]) {
		t.Errorf("A's verified row changed")
	}

	// Through a symlinked directory: refused.
	writeFile(t, filepath.Join(srcB, "z.mov"), "via link", t0)
	out, _, _ = vault(t, cfg, "move", "B", "A", srcB, dst, "--rule", "MOV=alias")
	if !strings.Contains(out, "dst through a symlink (alias)") {
		t.Errorf("z.mov should be refused through the link:\n%s", out)
	}
	if got := readFile(t, filepath.Join(srcB, "z.mov")); got != "via link" {
		t.Errorf("z.mov moved through the link: %q", got)
	}
}

// TestGap is B22's report through main(): the GAP line with its arithmetic,
// the TSV, a read-only manifest (byte-identical, no config created when
// absent), and the exit codes.
func TestGap(t *testing.T) {
	cfg, src, dst, ssd := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "DCIM", "a.ARW"), "archived raw", t0)
	writeFile(t, filepath.Join(src, "DCIM", "b.ARW"), "archived two", t0)
	if _, _, code := vault(t, cfg, "copy", "sony", src, dst); code != 0 {
		t.Fatalf("copy: exit %d", code)
	}
	if _, _, code := vault(t, cfg, "verify", "sony", dst); code != 0 {
		t.Fatalf("verify: exit %d", code)
	}
	writeFile(t, filepath.Join(ssd, "SonyA6700", "DCIM", "renamed.ARW"), "archived raw", t0) // archived by content
	writeFile(t, filepath.Join(ssd, "SonyA6700", "DCIM", "new.ARW"), "brand new photo", t0)
	writeFile(t, filepath.Join(ssd, "Backup", "db.bin"), "archived tw0", t0) // same size as b, other bytes
	db := filepath.Join(cfg, "manifest.db")
	before := sha(readFile(t, db))

	tsv := filepath.Join(t.TempDir(), "gap.tsv")
	out, _, code := vault(t, cfg, "gap", ssd, "--tsv", tsv)
	if code != 0 {
		t.Fatalf("gap: exit %d\n%s", code, out)
	}
	want := "GAP " + ssd + " needs archiving: yes, 2 files, 27 bytes (of 3 files / 39 bytes on the disk; 1 files / 12 bytes archived by content; 2 files / 24 bytes hashed to prove it; check 1+2=3)"
	if !strings.Contains(out, want) {
		t.Errorf("GAP line missing; want\n%s\ngot\n%s", want, out)
	}
	if !strings.Contains(out, "against 2 verified rows") {
		t.Errorf("row count missing:\n%s", out)
	}
	got := readFile(t, tsv)
	if got != "path\tsize\tsha256\nBackup/db.bin\t12\t"+sha("archived tw0")+"\nSonyA6700/DCIM/new.ARW\t15\t\n" {
		t.Errorf("tsv = %q", got)
	}
	if sha(readFile(t, db)) != before {
		t.Errorf("gap changed the manifest")
	}
	if entries, _ := os.ReadDir(filepath.Join(ssd, "SonyA6700", "DCIM")); len(entries) != 2 {
		t.Errorf("gap wrote into the source: %v", entries)
	}
	// Everything archived: "no", with the hashing cost stated.
	writeFile(t, filepath.Join(ssd, "Backup", "db.bin"), "archived two", t0)
	if err := os.Remove(filepath.Join(ssd, "SonyA6700", "DCIM", "new.ARW")); err != nil {
		t.Fatal(err)
	}
	if out, _, code := vault(t, cfg, "gap", ssd); code != 0 || !strings.Contains(out, "needs archiving: no, 0 files, 0 bytes (of 2 files / 24 bytes on the disk; 2 files / 24 bytes archived by content; 2 files / 24 bytes hashed to prove it; check 2+0=2)") {
		t.Errorf("all archived: exit %d\n%s", code, out)
	}
	// Read-only by construction: no config is created for a gap against nothing.
	none := filepath.Join(t.TempDir(), "never")
	if out, errOut, code := vault(t, none, "gap", ssd); code != 0 || !strings.Contains(out, "needs archiving: yes, 2 files") || !strings.Contains(errOut, "creating nothing") {
		t.Errorf("gap without a manifest: exit %d\n%s%s", code, out, errOut)
	}
	if _, err := os.Stat(none); err == nil {
		t.Errorf("gap created the config dir")
	}
	// Exit codes.
	if _, _, code := vault(t, cfg, "gap"); code != 2 {
		t.Errorf("no dir: exit %d, want 2", code)
	}
	if _, _, code := vault(t, cfg, "gap", ssd, "--bogus"); code != 1 {
		t.Errorf("unknown flag: exit %d, want 1", code)
	}
	if _, _, code := vault(t, cfg, "gap", filepath.Join(ssd, "nope")); code != 1 {
		t.Errorf("missing dir: exit %d, want 1", code)
	}
}
