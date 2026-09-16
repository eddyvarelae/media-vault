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
)

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
