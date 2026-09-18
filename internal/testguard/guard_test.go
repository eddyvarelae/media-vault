package testguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	Require()
	os.Exit(m.Run())
}

// A fake root under t.TempDir() stands in for /volume1, so the guard's
// refusal is exercised without a real NAS path ever being touched. Paths are
// spelled with string concatenation on purpose: filepath.Join would clean a
// ".." away before the guard sees it, and the kernel does not clean.
func TestCheckUnderRefusesResolvedPaths(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "volume1")
	real := filepath.Join(root, "docker", "tmp")
	for _, d := range []string{real, filepath.Join(root, "tmp"), filepath.Join(base, "tmp"), filepath.Join(base, "sub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "innocent-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	// A symlink whose own target climbs with "..": resolving the link must
	// apply the target's ".." after the target's symlinks, not before.
	relTarget := filepath.Join(base, "sub", "rel-link")
	if err := os.Symlink("../volume1/docker/tmp", relTarget); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(mustGetwd(t), link)
	if err != nil {
		t.Fatal(err)
	}

	roots := []string{root}
	refuse := map[string]string{
		"exact root":                 root,
		"direct child":               real,
		"symlink into root":          link,
		"relative symlink into root": rel,
		"symlink with .. in target":  relTarget,
		// Review #3 finding 1: lexically this is base/tmp (harmless, exists);
		// the kernel follows the link first and lands in volume1/tmp.
		"link/../x into root":  link + "/../../tmp",
		"root spelled with ..": base + "/tmp/../volume1/docker",
	}
	for name, dir := range refuse {
		t.Run(name, func(t *testing.T) {
			err := CheckUnder(dir, roots)
			if err == nil {
				t.Fatalf("CheckUnder(%q) allowed a path under the forbidden root", dir)
			}
			if !strings.Contains(err.Error(), "forbidden root") {
				t.Errorf("error should name the root: %v", err)
			}
			t.Logf("refused: %v", err)
		})
	}
}

// A temp root that does not fully exist cannot be checked, so it is refused
// outright rather than guessed at - whether or not the existing part looks
// safe. (MkdirTemp would fail there anyway; the guard says why first.)
func TestCheckUnderRefusesUnresolvable(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "volume1")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(base, "dangling-link")
	if err := os.Symlink(filepath.Join(root, "not-yet-created"), dangling); err != nil {
		t.Fatal(err)
	}
	loopA, loopB := filepath.Join(base, "loop-a"), filepath.Join(base, "loop-b")
	if err := os.Symlink(loopB, loopA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loopA, loopB); err != nil {
		t.Fatal(err)
	}
	refuse := map[string]string{
		"dangling symlink into root":  dangling,
		"non-existent tail in root":   filepath.Join(root, "new", "deeper"),
		"non-existent tail elsewhere": filepath.Join(base, "nope", "deeper"),
		"missing then ..":             base + "/nope/../volume1",
		"symlink loop":                loopA,
	}
	for name, dir := range refuse {
		t.Run(name, func(t *testing.T) {
			err := CheckUnder(dir, []string{root})
			if err == nil {
				t.Fatalf("CheckUnder(%q) allowed a path it cannot resolve", dir)
			}
			t.Logf("refused: %v", err)
		})
	}
}

func TestCheckUnderAllowsLookalikes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "volume1")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// A link that leaves the root: lexically root/escape-link/../x stays in
	// the root, but the kernel ends up at base/x. The guard follows the kernel.
	escape := filepath.Join(root, "escape-link")
	if err := os.Symlink(filepath.Join(base, "elsewhere"), escape); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(base, "elsewhere"), filepath.Join(base, "x")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	allow := map[string]string{
		"sibling":               filepath.Join(base, "elsewhere"),
		"prefix, not a child":   filepath.Join(base, "volume10"),
		"root name deeper down": filepath.Join(base, "ok", "volume1", "x"), // substring is not enough
		"the temp root itself":  base,
		"link/../x out of root": escape + "/../x",
		"trailing slash and .":  base + "/elsewhere/./",
	}
	for name, dir := range allow {
		t.Run(name, func(t *testing.T) {
			// MkdirAll would trip over the symlink case; its dirs exist above.
			if !strings.Contains(dir, "escape-link") {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := CheckUnder(dir, []string{root}); err != nil {
				t.Errorf("CheckUnder(%q) refused a path outside the root: %v", dir, err)
			}
		})
	}
}

// Review #3 finding 2: t.TempDir() creates under GOTMPDIR when it is set, not
// under os.TempDir(), so the guard has to check whichever of the two the
// toolchain will use. t.Setenv is process-wide, so the fixture dirs are
// taken from t.TempDir() before either variable moves and no t.TempDir() is
// called afterwards in this test.
func TestCheckEnvCoversBothTempRoots(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "volume1")
	bad := filepath.Join(root, "review-tmp")
	safe := filepath.Join(base, "safe")
	for _, d := range []string{bad, safe} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	roots := []string{root}
	cases := []struct {
		name, tmpdir, gotmpdir, wantErr string
	}{
		{"both safe", safe, safe, ""},
		{"GOTMPDIR unset", safe, "", ""},
		{"TMPDIR bad", bad, safe, "TMPDIR"},
		{"GOTMPDIR bad", safe, bad, "GOTMPDIR"},
		{"GOTMPDIR bad, TMPDIR unset", "", bad, "GOTMPDIR"},
		{"both bad", bad, bad, "forbidden root"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TMPDIR", c.tmpdir)
			t.Setenv("GOTMPDIR", c.gotmpdir)
			err := CheckEnv(roots)
			switch {
			case c.wantErr == "" && err != nil:
				t.Errorf("CheckEnv refused a safe environment: %v", err)
			case c.wantErr != "" && err == nil:
				t.Errorf("CheckEnv allowed TMPDIR=%q GOTMPDIR=%q", c.tmpdir, c.gotmpdir)
			case c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr):
				t.Errorf("error should mention %s: %v", c.wantErr, err)
			case err != nil:
				t.Logf("refused: %v", err)
			}
		})
	}
}

// The real roots, against the real temp dirs: this is what Require checks in
// every package's TestMain, and it must pass on a sane host.
func TestCheckAcceptsTheHostTempDir(t *testing.T) {
	if err := CheckEnv(Roots); err != nil {
		t.Fatal(err)
	}
	if err := Check(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// And the real roots refuse themselves, whether or not they exist here.
	for _, root := range Roots {
		if err := Check(filepath.Join(root, "review-tmp")); err == nil {
			t.Errorf("Check(%s/review-tmp) should refuse", root)
		}
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}
