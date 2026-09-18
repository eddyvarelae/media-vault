package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
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

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seed writes a row for disk "cam" and, unless content is "", the file it
// points at under root. verified_at is set for verified rows only.
func seed(t *testing.T, m *manifest.Manifest, root, src, dest, content, status string, verifiedAt int64) {
	t.Helper()
	rel := dest
	if rel == "" {
		rel = src
	}
	if content != "" {
		write(t, filepath.Join(root, rel), content)
	}
	if err := m.Upsert(manifest.Entry{SourceDisk: "cam", SourcePath: src, DestPath: dest,
		Size: int64(len(content)), MtimeNs: 1, SHA256: sha(content), CopiedAt: 2,
		VerifiedAt: verifiedAt, Status: status}); err != nil {
		t.Fatal(err)
	}
}

func rows(t *testing.T, m *manifest.Manifest) map[string]manifest.Entry {
	t.Helper()
	list, err := m.ListByDisk("cam")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]manifest.Entry{}
	for _, e := range list {
		out[e.SourcePath] = e
	}
	return out
}

func run(t *testing.T, m *manifest.Manifest, root string, only bool) (*Result, map[string]string) {
	t.Helper()
	seen := map[string]string{}
	res, err := RunWithOptions(context.Background(), m, "cam", root, only, func(src, dst, status string) {
		seen[src] = status
	})
	if err != nil {
		t.Fatal(err)
	}
	return res, seen
}

// TestOnlyUnverifiedHashesEveryNonVerifiedRowAndNothingElse is the F4
// "done when" list plus review #1's falsifiers, at the package level:
// every non-verified status is in the incremental pass, no verified row
// is, and no row is promoted without its bytes being read.
func TestOnlyUnverifiedHashesEveryNonVerifiedRowAndNothingElse(t *testing.T) {
	m := openManifest(t)
	root := t.TempDir()
	seed(t, m, root, "ok.mov", "ok.mov", "verified before", "verified", 1000)
	seed(t, m, root, "rotten.mov", "rotten.mov", "verified but rotten", "verified", 2000)
	write(t, filepath.Join(root, "rotten.mov"), "verified but ROTTEN") // bit-rot under a verified row
	seed(t, m, root, "new.mov", "new.mov", "just copied", "copied", 0)
	seed(t, m, root, "fixed.mov", "fixed.mov", "was mismatch, now fine", "mismatch", 500)
	seed(t, m, root, "still-bad.mov", "still-bad.mov", "was mismatch, still", "mismatch", 600)
	write(t, filepath.Join(root, "still-bad.mov"), "was mismatch, STILL")
	seed(t, m, root, "dup.mov", "ok.mov", "verified before", "deduped", 0)   // by-reference to ok.mov's bytes
	seed(t, m, root, "inv/x.mov", "", "inventoried bytes", "inventoried", 0) // no dest: hashed at source_path
	seed(t, m, root, "gone.mov", "gone.mov", "", "copied", 0)                // file absent
	before := rows(t, m)

	res, seen := run(t, m, root, true)
	wantSeen := map[string]string{
		"new.mov":       "verified",
		"fixed.mov":     "verified",
		"still-bad.mov": "MISMATCH",
		"dup.mov":       "verified",
		"inv/x.mov":     "verified",
		"gone.mov":      "missing",
	}
	if !reflect.DeepEqual(seen, wantSeen) {
		t.Errorf("incremental pass touched %v, want %v", seen, wantSeen)
	}
	if res.Verified != 4 || res.Mismatch != 1 || res.Missing != 1 || res.Errors != 0 {
		t.Errorf("result = %+v", res)
	}
	// Bytes read = every file the pass hashed, once each: new, fixed,
	// still-bad, dup's target (ok.mov), inv/x.mov. Not rotten.mov.
	want := int64(len("just copied") + len("was mismatch, now fine") + len("was mismatch, STILL") + len("verified before") + len("inventoried bytes"))
	if res.BytesRead != want {
		t.Errorf("BytesRead = %d, want %d", res.BytesRead, want)
	}
	after := rows(t, m)
	for _, src := range []string{"ok.mov", "rotten.mov"} {
		if !reflect.DeepEqual(after[src], before[src]) {
			t.Errorf("verified row %s touched by the incremental pass:\n before %+v\n after  %+v", src, before[src], after[src])
		}
	}
	if after["rotten.mov"].Status != "verified" {
		t.Errorf("rotten.mov should still say verified - the incremental pass does not look, which is exactly why the output must say so")
	}
	if after["gone.mov"].Status != "copied" || after["gone.mov"].VerifiedAt != 0 {
		t.Errorf("missing file's row moved: %+v", after["gone.mov"])
	}
	if after["still-bad.mov"].Status != "mismatch" || after["still-bad.mov"].VerifiedAt <= 600 {
		t.Errorf("still-bad.mov should be re-stamped mismatch: %+v", after["still-bad.mov"])
	}
	for _, src := range []string{"new.mov", "fixed.mov", "dup.mov", "inv/x.mov"} {
		if after[src].Status != "verified" || after[src].VerifiedAt == 0 || after[src].SHA256 != before[src].SHA256 {
			t.Errorf("%s not promoted cleanly: %+v", src, after[src])
		}
	}

	// A bare pass reads everything and finds the rot the incremental pass
	// could not see.
	full, seenFull := run(t, m, root, false)
	if seenFull["rotten.mov"] != "MISMATCH" || seenFull["ok.mov"] != "verified" || len(seenFull) != 8 {
		t.Errorf("full pass: %v", seenFull)
	}
	// Everything the incremental pass read, plus rotten.mov; ok.mov is one
	// path for two rows and is read once per run.
	if full.BytesRead != want+int64(len("verified but ROTTEN")) {
		t.Errorf("full BytesRead = %d, want %d", full.BytesRead, want+int64(len("verified but ROTTEN")))
	}
}

// A disk whose rows are all `copied` verifies identically under both modes.
func TestAllCopiedDiskIsIdenticalUnderBothModes(t *testing.T) {
	for _, only := range []bool{false, true} {
		m := openManifest(t)
		root := t.TempDir()
		seed(t, m, root, "a.mov", "a.mov", "aaaa", "copied", 0)
		seed(t, m, root, "b.mov", "b.mov", "bbbbbb", "copied", 0)
		seed(t, m, root, "c.mov", "c.mov", "cc", "copied", 0)
		write(t, filepath.Join(root, "c.mov"), "CC")
		res, seen := run(t, m, root, only)
		if res.Verified != 2 || res.Mismatch != 1 || res.BytesRead != 12 || len(seen) != 3 {
			t.Errorf("only=%v: result %+v seen %v", only, res, seen)
		}
	}
}

// CountVerifiedInDisk is what the incremental output reports: the number
// of rows it will skip and the newest single verified_at among them.
func TestCountVerifiedInDisk(t *testing.T) {
	m := openManifest(t)
	root := t.TempDir()
	if n, newest, err := m.CountVerifiedInDisk("cam"); err != nil || n != 0 || newest != 0 {
		t.Errorf("empty disk: %d %d %v", n, newest, err)
	}
	seed(t, m, root, "a.mov", "a.mov", "a", "verified", 5000)
	seed(t, m, root, "b.mov", "b.mov", "b", "verified", 9000)
	seed(t, m, root, "c.mov", "c.mov", "c", "mismatch", 99999) // not verified: its stamp does not count
	seed(t, m, root, "d.mov", "d.mov", "d", "copied", 0)
	if err := m.Upsert(manifest.Entry{SourceDisk: "other", SourcePath: "z", DestPath: "z", Size: 1, SHA256: "x", CopiedAt: 1, VerifiedAt: 77777, Status: "verified"}); err != nil {
		t.Fatal(err)
	}
	n, newest, err := m.CountVerifiedInDisk("cam")
	if err != nil || n != 2 || newest != 9000 {
		t.Errorf("CountVerifiedInDisk = %d, %d, %v; want 2, 9000", n, newest, err)
	}
	// The count and the list agree: what is counted is exactly what the
	// incremental pass will not list.
	unverified, err := m.ListByDiskUnverified("cam")
	if err != nil || len(unverified) != 2 {
		t.Errorf("ListByDiskUnverified = %d rows, %v; want 2", len(unverified), err)
	}
	all, _ := m.ListByDisk("cam")
	if len(all) != n+len(unverified) {
		t.Errorf("all %d != verified %d + unverified %d", len(all), n, len(unverified))
	}
}
