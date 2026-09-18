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
// does may land under either, however the temp root is spelled.
var Roots = []string{"/volume1", "/mnt"}

// Require refuses to run the suite when any temp root the toolchain may use
// resolves under a forbidden root. It prints why and exits 1 so the refusal
// is visible in `go test` output rather than buried in a skipped test.
func Require() {
	if err := CheckEnv(Roots); err != nil {
		fmt.Fprintf(os.Stderr, "testguard: refusing to run: %v\n", err)
		os.Exit(1)
	}
}

// CheckEnv checks every directory a fixture may be created in. There are
// two, and they are independent: os.TempDir() (TMPDIR, else the platform
// default) is where os.MkdirTemp("", …) goes, and GOTMPDIR is what
// testing.T.TempDir uses directly when it is set — a safe TMPDIR proves
// nothing about it.
func CheckEnv(roots []string) error {
	if err := CheckUnder(os.TempDir(), roots); err != nil {
		return fmt.Errorf("TMPDIR: %w", err)
	}
	if d := os.Getenv("GOTMPDIR"); d != "" {
		if err := CheckUnder(d, roots); err != nil {
			return fmt.Errorf("GOTMPDIR: %w", err)
		}
	}
	return nil
}

// Check reports whether dir resolves under one of Roots.
func Check(dir string) error { return CheckUnder(dir, Roots) }

// CheckUnder is Check against an explicit root list, so the guard itself can
// be tested without a real /volume1 on the host. A dir that cannot be fully
// resolved — a component missing, a dangling link, a link loop — is refused
// too: a temp root the guard cannot see the end of is not one it can vouch
// for, and MkdirTemp would fail there anyway.
func CheckUnder(dir string, roots []string) error {
	resolved, err := resolve(dir)
	if err != nil {
		return fmt.Errorf("cannot resolve temp dir %q: %v", dir, err)
	}
	for _, root := range roots {
		// The root may itself be a symlink on some hosts; compare against
		// both spellings. A root that does not exist here has only one.
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

// resolve returns the symlink-free absolute form of p the way the kernel
// would reach it: one component at a time, following each symlink where it
// occurs, and applying ".." to the directory actually reached rather than to
// the spelling. filepath.Abs/Clean would collapse "link/../x" to "x" before
// the link is looked at, which is the bypass review #3 found; nothing here
// cleans a path before walking it.
func resolve(p string) (string, error) {
	hops := 0
	base := string(filepath.Separator)
	if !filepath.IsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		if base, err = walk(base, wd, &hops); err != nil {
			return "", err
		}
	}
	return walk(base, p, &hops)
}

// maxHops bounds symlink following so a link cycle refuses instead of
// hanging the suite; the kernel's own limit is 40.
const maxHops = 40

// walk resolves the components of p against base, which is already resolved.
func walk(base, p string, hops *int) (string, error) {
	for _, c := range strings.Split(p, string(filepath.Separator)) {
		switch c {
		case "", ".":
			continue
		case "..":
			base = filepath.Dir(base)
			continue
		}
		next := base + string(filepath.Separator) + c
		if base == string(filepath.Separator) {
			next = base + c
		}
		fi, err := os.Lstat(next)
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			base = next
			continue
		}
		*hops++
		if *hops > maxHops {
			return "", fmt.Errorf("too many symlinks at %q", next)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", err
		}
		from := base
		if filepath.IsAbs(target) {
			from = string(filepath.Separator)
		}
		if base, err = walk(from, target, hops); err != nil {
			return "", err
		}
	}
	return base, nil
}
