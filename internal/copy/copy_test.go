package copy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eddyvarelae/media-vault/internal/scan"
	"github.com/eddyvarelae/media-vault/internal/testguard"
)

func TestMain(m *testing.M) {
	testguard.Require() // never write fixtures under /volume1 or /mnt
	os.Exit(m.Run())
}

// noPartials fails the test if any *.vault-partial is left anywhere under
// dst. A surviving partial is a half-written file the next scan would
// mistake for a collision, so every failure path must remove it.
func noPartials(t *testing.T, dst string) {
	t.Helper()
	err := filepath.WalkDir(dst, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".vault-partial") {
			t.Errorf("leftover partial: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

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

func TestFileCopiesAtomicallyAndPreservesMtime(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	mtime := time.Date(2024, 3, 9, 10, 0, 0, 0, time.UTC)
	writeFile(t, filepath.Join(src, "DCIM", "clip.mp4"), "hello footage", mtime)

	task := scan.FileTask{RelPath: "DCIM/clip.mp4", DstRel: "Videos/clip.mp4", Size: 13, MtimeNs: mtime.UnixNano()}
	e, err := File(context.Background(), src, dst, task, "diskA")
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "Videos", "clip.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello footage" {
		t.Fatalf("dest content = %q", got)
	}
	sum := sha256.Sum256([]byte("hello footage"))
	if e.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha = %s, want %s", e.SHA256, hex.EncodeToString(sum[:]))
	}
	if e.Status != "copied" || e.DestPath != "Videos/clip.mp4" || e.SourcePath != "DCIM/clip.mp4" || e.Size != 13 {
		t.Errorf("entry = %+v", e)
	}
	info, err := os.Stat(filepath.Join(dst, "Videos", "clip.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(mtime) {
		t.Errorf("dest mtime = %v, want %v", info.ModTime(), mtime)
	}
	noPartials(t, dst)
}

func TestFileRemovesPartialOnShortWrite(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	mtime := time.Now()
	writeFile(t, filepath.Join(src, "a.mov"), "short", mtime)

	// The plan recorded a bigger size than what is on disk now (the source
	// changed under us). The copy must fail and leave no partial behind.
	task := scan.FileTask{RelPath: "a.mov", DstRel: "a.mov", Size: 1 << 20, MtimeNs: mtime.UnixNano()}
	_, err := File(context.Background(), src, dst, task, "diskA")
	if err == nil || !strings.Contains(err.Error(), "short write") {
		t.Fatalf("err = %v, want short write", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a.mov")); !os.IsNotExist(err) {
		t.Errorf("final dest must not exist after a failed copy, stat err = %v", err)
	}
	noPartials(t, dst)
}

func TestFileRemovesPartialOnCancel(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	mtime := time.Now()
	writeFile(t, filepath.Join(src, "a.mov"), strings.Repeat("x", 4096), mtime)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the first Read fails, mid-copy
	task := scan.FileTask{RelPath: "a.mov", DstRel: "a.mov", Size: 4096, MtimeNs: mtime.UnixNano()}
	_, err := File(ctx, src, dst, task, "diskA")
	if err == nil {
		t.Fatal("expected an error from a cancelled copy")
	}
	if _, err := os.Stat(filepath.Join(dst, "a.mov")); !os.IsNotExist(err) {
		t.Errorf("final dest must not exist after a cancelled copy, stat err = %v", err)
	}
	noPartials(t, dst)
}

func TestFileMissingSourceLeavesNothing(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	task := scan.FileTask{RelPath: "gone.mov", DstRel: "gone.mov", Size: 1, MtimeNs: time.Now().UnixNano()}
	if _, err := File(context.Background(), src, dst, task, "diskA"); err == nil {
		t.Fatal("expected an error for a missing source")
	}
	entries, err := os.ReadDir(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("dst should be empty, has %d entries", len(entries))
	}
	noPartials(t, dst)
}

// caseInsensitive reports whether dir's filesystem folds case: a file made
// as "probe" is found as "PROBE". APFS defaults to insensitive; the test
// that depends on it adapts rather than assuming.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	writeFile(t, filepath.Join(dir, "probe"), "p", time.Now())
	_, err := os.Lstat(filepath.Join(dir, "PROBE"))
	return err == nil
}

// TestFileRefusesWhateverExists is review #6: the writer checks the
// physical paths it touches. Nothing at the staging path is ever
// truncated (Lstat, then O_EXCL); the final path may hold only a recopy's
// own regular file.
func TestFileRefusesWhateverExists(t *testing.T) {
	mtime := time.Date(2024, 3, 9, 10, 0, 0, 0, time.UTC)
	newTask := func(name string) scan.FileTask {
		return scan.FileTask{RelPath: name, DstRel: name, Size: 8, MtimeNs: mtime.UnixNano()}
	}

	t.Run("staging path holds a file", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(src, "x.mov"), "new clip", mtime)
		writeFile(t, filepath.Join(dst, "x.mov.vault-partial"), "archived under an unlucky name", mtime)
		_, err := File(context.Background(), src, dst, newTask("x.mov"), "B")
		if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "interrupted run") {
			t.Fatalf("err = %v, want a refusal that names the leftover-partial possibility", err)
		}
		if got, _ := os.ReadFile(filepath.Join(dst, "x.mov.vault-partial")); string(got) != "archived under an unlucky name" {
			t.Errorf("staging-path file touched: %q", got)
		}
		if _, err := os.Stat(filepath.Join(dst, "x.mov")); err == nil {
			t.Errorf("x.mov written anyway")
		}
	})

	t.Run("staging path holds a symlink", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(src, "x.mov"), "new clip", mtime)
		writeFile(t, filepath.Join(dst, "elsewhere"), "precious", mtime)
		if err := os.Symlink(filepath.Join(dst, "elsewhere"), filepath.Join(dst, "x.mov.vault-partial")); err != nil {
			t.Fatal(err)
		}
		if _, err := File(context.Background(), src, dst, newTask("x.mov"), "B"); err == nil || !strings.Contains(err.Error(), "a symlink") {
			t.Fatalf("err = %v, want refusal naming the symlink", err)
		}
		if got, _ := os.ReadFile(filepath.Join(dst, "elsewhere")); string(got) != "precious" {
			t.Errorf("symlink target truncated through the staging path: %q", got)
		}
	})

	t.Run("final path exists for a file planned as new", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(src, "x.mov"), "new clip", mtime)
		writeFile(t, filepath.Join(dst, "x.mov"), "already here", mtime)
		if _, err := File(context.Background(), src, dst, newTask("x.mov"), "B"); err == nil || !strings.Contains(err.Error(), "planned as new") {
			t.Fatalf("err = %v, want refusal", err)
		}
		if got, _ := os.ReadFile(filepath.Join(dst, "x.mov")); string(got) != "already here" {
			t.Errorf("final overwritten: %q", got)
		}
		noPartials(t, dst)
	})

	t.Run("recopy replaces its own regular file", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(src, "x.mov"), "new clip", mtime)
		writeFile(t, filepath.Join(dst, "x.mov"), "old copy", mtime)
		task := newTask("x.mov")
		task.Replace = true
		if _, err := File(context.Background(), src, dst, task, "B"); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(filepath.Join(dst, "x.mov")); string(got) != "new clip" {
			t.Errorf("recopy did not replace: %q", got)
		}
		noPartials(t, dst)
	})

	t.Run("recopy refuses a non-regular final path", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(src, "x.mov"), "new clip", mtime)
		writeFile(t, filepath.Join(dst, "real"), "precious", mtime)
		if err := os.Symlink(filepath.Join(dst, "real"), filepath.Join(dst, "x.mov")); err != nil {
			t.Fatal(err)
		}
		task := newTask("x.mov")
		task.Replace = true
		if _, err := File(context.Background(), src, dst, task, "B"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("err = %v, want refusal", err)
		}
		if got, _ := os.ReadFile(filepath.Join(dst, "real")); string(got) != "precious" {
			t.Errorf("symlink target replaced: %q", got)
		}
		noPartials(t, dst)
	})

	// The Reviewer's case-insensitive example at the writer: X.mov.vault-partial
	// is on disk, the task wants x.mov. On a folding filesystem Lstat sees
	// it and refuses; on a case-sensitive one the two names are two files
	// and the copy proceeds beside it, which is also correct.
	t.Run("case alias of the staging path", func(t *testing.T) {
		src, dst := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(src, "x.mov"), "new clip", mtime)
		writeFile(t, filepath.Join(dst, "X.mov.vault-partial"), "archived, capital X", mtime)
		folds := caseInsensitive(t, dst)
		t.Logf("temp filesystem case-insensitive: %v", folds)
		_, err := File(context.Background(), src, dst, newTask("x.mov"), "B")
		if got, _ := os.ReadFile(filepath.Join(dst, "X.mov.vault-partial")); string(got) != "archived, capital X" {
			t.Fatalf("archived file destroyed through its case alias: %q", got)
		}
		if folds && (err == nil || !strings.Contains(err.Error(), "already exists")) {
			t.Errorf("folding filesystem: err = %v, want refusal", err)
		}
		if !folds && err != nil {
			t.Errorf("case-sensitive filesystem: err = %v, want success beside the other file", err)
		}
	})
}
