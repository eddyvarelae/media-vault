// Package testguard keeps the test suite off the NAS.
//
// Every writing test package calls Require from its TestMain before any
// fixture is created. Go builds one test binary per package, so a guard in
// cmd/vault alone protects nothing else — this package is the one place the
// rule lives. It is imported only from _test.go files.
package testguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Roots are the production filesystems on the NAS: the archive and the
// manifest under /volume1, the source SSDs under /mnt/@usb. Nothing a test
// does may land under either, however TMPDIR is spelled.
var Roots = []string{"/volume1", "/mnt"}

// Require refuses to run the suite when the temp root resolves under a
// forbidden root. It prints why and exits 1 so the refusal is visible in
// `go test` output rather than buried in a skipped test.
func Require() {
	if err := Check(os.TempDir()); err != nil {
		fmt.Fprintf(os.Stderr, "testguard: refusing to run: %v\n", err)
		os.Exit(1)
	}
}

// Check reports whether dir resolves under one of Roots.
func Check(dir string) error { return CheckUnder(dir, Roots) }

// CheckUnder is Check against an explicit root list, so the guard itself can
// be tested without a real /volume1 on the host.
func CheckUnder(dir string, roots []string) error {
	resolved, err := resolve(dir)
	if err != nil {
		return fmt.Errorf("cannot resolve temp dir %q: %v", dir, err)
	}
	for _, root := range roots {
		// The root may itself be a symlink on some hosts; compare against
		// both spellings. A root that does not exist resolves to itself.
		candidates := []string{filepath.Clean(root)}
		if r, err := resolve(root); err == nil && r != candidates[0] {
			candidates = append(candidates, r)
		}
		for _, c := range candidates {
			if resolved == c || strings.HasPrefix(resolved, c+string(filepath.Separator)) {
				return fmt.Errorf("temp dir %q resolves to %q, under forbidden root %s", dir, resolved, root)
			}
		}
	}
	return nil
}

// resolve returns the absolute, symlink-free form of p. Where p does not
// fully exist yet, the longest existing ancestor is resolved and the rest
// re-appended, and a dangling symlink is followed by hand — so a TMPDIR that
// points at a not-yet-created directory under a forbidden root still trips
// the guard instead of slipping through as its own name.
func resolve(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	cur, rest := abs, ""
	for i := 0; i < 64; i++ { // bound: a symlink cycle must not hang the suite
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest), nil
		}
		if target, err := os.Readlink(cur); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(cur), target)
			}
			cur = filepath.Clean(target)
			continue
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
	return "", fmt.Errorf("too many symlinks under %q", abs)
}
