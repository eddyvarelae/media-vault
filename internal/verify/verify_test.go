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
		"dup.mov":       "deduped", // B51: by-reference, skipped (not hashed, not promoted)
		"inv/x.mov":     "verified",
		"gone.mov":      "missing",
	}
	if !reflect.DeepEqual(seen, wantSeen) {
		t.Errorf("incremental pass touched %v, want %v", seen, wantSeen)
	}
	if res.Verified != 3 || res.Mismatch != 1 || res.Missing != 1 || res.Errors != 0 || res.Deduped != 1 {
		t.Errorf("result = %+v", res)
	}
	// Bytes read = every file the pass hashed, once each: new, fixed,
	// still-bad, inv/x.mov. Not rotten.mov (verified, skipped), and NOT dup's
	// target (dup is a deduped row — skipped, not hashed, B51).
	want := int64(len("just copied") + len("was mismatch, now fine") + len("was mismatch, STILL") + len("inventoried bytes"))
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
	for _, src := range []string{"new.mov", "fixed.mov", "inv/x.mov"} {
		if after[src].Status != "verified" || after[src].VerifiedAt == 0 || after[src].SHA256 != before[src].SHA256 {
			t.Errorf("%s not promoted cleanly: %+v", src, after[src])
		}
	}
	// B51: the deduped row is left exactly as it was — not hashed, not promoted.
	if !reflect.DeepEqual(after["dup.mov"], before["dup.mov"]) || after["dup.mov"].Status != "deduped" {
		t.Errorf("dup.mov (deduped) touched by verify:\n before %+v\n after  %+v", before["dup.mov"], after["dup.mov"])
	}

	// A bare pass reads everything and finds the rot the incremental pass
	// could not see.
	full, seenFull := run(t, m, root, false)
	if seenFull["rotten.mov"] != "MISMATCH" || seenFull["ok.mov"] != "verified" || len(seenFull) != 8 {
		t.Errorf("full pass: %v", seenFull)
	}
	// Everything the incremental pass read, plus rotten.mov and ok.mov (both
	// verified rows a full pass re-reads). dup.mov is deduped → still skipped.
	wantFull := want + int64(len("verified but ROTTEN")+len("verified before"))
	if full.BytesRead != wantFull {
		t.Errorf("full BytesRead = %d, want %d", full.BytesRead, wantFull)
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

// TestDedupedRowNotHashedUnderThisRoot is the B51 reproducer: a deduped row's
// dest_path is the OWNER's, resolved under the owner's root. verify of THIS disk
// must not hash it under this root (where the file is absent) and report a
// spurious Missing, and must not touch its status. (Before B51 this was
// Missing: 1, exit 1 — kipp-backup's one deduped STATUS.BIN.)
func TestDedupedRowNotHashedUnderThisRoot(t *testing.T) {
	m := openManifest(t)
	root := t.TempDir() // this disk's root; the owner's file lives elsewhere → absent here
	dedup := manifest.Entry{SourceDisk: "cam", SourcePath: "PRIVATE/M4ROOT/STATUS.BIN", DestPath: "STATUS.BIN",
		Size: 7, MtimeNs: 1, SHA256: "beefcafe", CopiedAt: 2, Status: "deduped"}
	if err := m.Upsert(dedup); err != nil {
		t.Fatal(err)
	}
	before := rows(t, m)
	for _, only := range []bool{false, true} {
		res, seen := run(t, m, root, only)
		if res.Deduped != 1 || res.Missing != 0 || res.Verified != 0 || res.Mismatch != 0 || res.BytesRead != 0 {
			t.Errorf("only=%v: result = %+v, want Deduped 1 and nothing else", only, res)
		}
		if seen["PRIVATE/M4ROOT/STATUS.BIN"] != "deduped" {
			t.Errorf("only=%v: row not reported as deduped: %v", only, seen)
		}
		if !reflect.DeepEqual(rows(t, m), before) {
			t.Errorf("only=%v: verify changed the deduped row", only)
		}
	}
}
