package copy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/eddyvarelae/media-vault/internal/manifest"
	"github.com/eddyvarelae/media-vault/internal/scan"
)

// File copies srcRoot/task.RelPath → dstRoot/task.DstRel, hashing the bytes
// in flight. The destination is written through a .vault-partial file and
// atomically renamed on success. mtime is preserved so future scans skip.
// task.DstRel may equal task.RelPath (no rules / prefix) or differ when
// routing rules redirected the file.
func File(ctx context.Context, srcRoot, dstRoot string, task scan.FileTask, disk string) (manifest.Entry, error) {
	srcPath := filepath.Join(srcRoot, task.RelPath)
	dstRel := task.DstRel
	if dstRel == "" {
		dstRel = task.RelPath
	}
	dstPath := filepath.Join(dstRoot, dstRel)

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return manifest.Entry{}, fmt.Errorf("mkdir: %w", err)
	}

	// Nothing that exists is ever truncated or renamed over except a
	// recopy's own destination. The plan checked spellings against the
	// manifest; this is the writer checking the physical paths it is about
	// to touch, on the filesystem's own terms (case folding included).
	tmpPath := dstPath + ".vault-partial"
	if fi, err := os.Lstat(tmpPath); err == nil {
		// A leftover partial from a run that died mid-file looks exactly
		// like an archived file that happens to end in .vault-partial, and
		// the writer cannot tell them apart - so it refuses both and says
		// so. A leftover is removed by hand once someone has looked at it.
		return manifest.Entry{}, fmt.Errorf("refusing to write: %s already exists (%s); if it is a leftover .vault-partial from an interrupted run, remove it by hand and re-run",
			tmpPath, describe(fi))
	} else if !os.IsNotExist(err) {
		return manifest.Entry{}, err
	}
	if fi, err := os.Lstat(dstPath); err == nil {
		if !task.Replace {
			return manifest.Entry{}, fmt.Errorf("refusing to write: %s already exists (%s) and this file was planned as new", dstPath, describe(fi))
		}
		if !fi.Mode().IsRegular() {
			return manifest.Entry{}, fmt.Errorf("refusing to replace: %s is %s, not a regular file", dstPath, describe(fi))
		}
	} else if !os.IsNotExist(err) {
		return manifest.Entry{}, err
	}

	in, err := os.Open(srcPath)
	if err != nil {
		return manifest.Entry{}, err
	}
	defer in.Close()

	// O_EXCL: the staging file must be created by this call. If it exists
	// after the Lstat above (a race, or a case alias Lstat missed), the open
	// fails and nothing is truncated - and nothing is removed either.
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return manifest.Entry{}, fmt.Errorf("refusing to write: %w", err)
	}
	cleanup := func() { os.Remove(tmpPath) }

	hasher := sha256.New()
	tee := io.TeeReader(&ctxReader{ctx: ctx, r: in}, hasher)

	written, err := io.Copy(out, tee)
	if err != nil {
		out.Close()
		cleanup()
		return manifest.Entry{}, fmt.Errorf("copy: %w", err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		cleanup()
		return manifest.Entry{}, fmt.Errorf("sync: %w", err)
	}
	if err := out.Close(); err != nil {
		cleanup()
		return manifest.Entry{}, fmt.Errorf("close: %w", err)
	}
	if written != task.Size {
		cleanup()
		return manifest.Entry{}, fmt.Errorf("short write: wrote %d, expected %d", written, task.Size)
	}
	mt := time.Unix(0, task.MtimeNs)
	if err := os.Chtimes(tmpPath, mt, mt); err != nil {
		cleanup()
		return manifest.Entry{}, fmt.Errorf("chtimes: %w", err)
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		cleanup()
		return manifest.Entry{}, fmt.Errorf("rename: %w", err)
	}

	return manifest.Entry{
		SourceDisk: disk,
		SourcePath: task.RelPath, // src-relative — used by scan.Build to skip on resume
		DestPath:   dstRel,       // dst-relative — used by verify to find the file
		Size:       task.Size,
		MtimeNs:    task.MtimeNs,
		SHA256:     hex.EncodeToString(hasher.Sum(nil)),
		CopiedAt:   time.Now().UnixNano(),
		Status:     "copied",
	}, nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func describe(fi os.FileInfo) string {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return "a symlink"
	case fi.IsDir():
		return "a directory"
	case fi.Mode().IsRegular():
		return fmt.Sprintf("a regular file, %d bytes", fi.Size())
	default:
		return fi.Mode().String()
	}
}
