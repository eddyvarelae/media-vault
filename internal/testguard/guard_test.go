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
// refusal is exercised without a real NAS path ever being touched.
func TestCheckUnderRefusesResolvedPaths(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "volume1")
	real := filepath.Join(root, "docker", "tmp")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "innocent-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(base, "dangling-link")
	if err := os.Symlink(filepath.Join(root, "not-yet-created"), dangling); err != nil {
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
		"dangling symlink into root": dangling,
		"non-existent tail":          filepath.Join(root, "new", "deeper"),
		"root spelled with ..":       filepath.Join(base, "x", "..", "volume1", "docker"),
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
		})
	}
}

func TestCheckUnderAllowsLookalikes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "volume1")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	allow := map[string]string{
		"sibling":               filepath.Join(base, "elsewhere"),
		"prefix, not a child":   filepath.Join(base, "volume10"),
		"root name deeper down": filepath.Join(base, "ok", "volume1", "x"), // substring is not enough
		"the temp root itself":  base,
	}
	for name, dir := range allow {
		t.Run(name, func(t *testing.T) {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := CheckUnder(dir, []string{root}); err != nil {
				t.Errorf("CheckUnder(%q) refused a path outside the root: %v", dir, err)
			}
		})
	}
}

// The real roots, against the real temp dir: this is what Require checks in
// every package's TestMain, and it must pass on a sane host.
func TestCheckAcceptsTheHostTempDir(t *testing.T) {
	if err := Check(os.TempDir()); err != nil {
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
