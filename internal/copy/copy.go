package copy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

	// The writer's own containment: a relative path that climbs out of the
	// root, or an absolute one, names a file the root does not contain, and
	// every check below would be protecting the wrong tree. Refused here as
	// well as in the plan, because a manifest row can carry such a spelling
	// (review #24) and every writer feeds through this function. Kept for its
	// message; os.Root below enforces the same physically.
	if why := Escapes(dstRoot, dstRel); why != "" {
		return manifest.Entry{}, fmt.Errorf("refusing to write: %s", why)
	}

	// Anchor every filesystem operation below to an os.Root handle on dstRoot
	// (B43): resolution is pinned to this directory fd even if a parent is
	// renamed under us, and os.Root refuses outright any path component that
	// escapes the root — the atomic backstop behind the checks, closing the
	// check-then-write TOCTOU that a component walk alone leaves open. dstRoot
	// is the archive root the operator gave; created here if missing, exactly
	// as the old MkdirAll(parent) did.
	if err := os.MkdirAll(dstRoot, 0o755); err != nil {
		return manifest.Entry{}, fmt.Errorf("mkdir root: %w", err)
	}
	root, err := os.OpenRoot(dstRoot)
	if err != nil {
		return manifest.Entry{}, fmt.Errorf("refusing to write: %w", err)
	}
	defer root.Close()

	// No directory on the way may be a symlink: through one, this path and
	// another spelling are the same file. The walk is re-anchored to the Root
	// fd (SymlinkComponentRoot) and still refuses an in-root symlinked
	// component that exists at walk time — os.Root would otherwise follow it.
	// Residual (documented, B43): a parent swapped to an IN-ROOT symlink in the
	// window after this walk is followed by os.Root, which only blocks escapes;
	// an escaping swap is refused atomically by the operations below.
	if link, err := scan.SymlinkComponentRoot(root, dstRel); err != nil {
		return manifest.Entry{}, fmt.Errorf("refusing to write: %w", err)
	} else if link != "" {
		return manifest.Entry{}, fmt.Errorf("refusing to write through a symlink: %s is a symlink; destination directories must be real", filepath.Join(dstRoot, link))
	}

	if dir := filepath.Dir(dstRel); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return manifest.Entry{}, fmt.Errorf("mkdir: %w", err)
		}
	}

	// Nothing that exists is ever truncated or renamed over except a recopy's
	// own destination. The plan checked spellings against the manifest; this is
	// the writer checking the physical paths it is about to touch through the
	// Root fd, on the filesystem's own terms (case folding included).
	tmpRel := dstRel + ".vault-partial"
	if fi, err := root.Lstat(tmpRel); err == nil {
		// A leftover partial from a run that died mid-file looks exactly
		// like an archived file that happens to end in .vault-partial, and
		// the writer cannot tell them apart - so it refuses both and says
		// so. A leftover is removed by hand once someone has looked at it.
		return manifest.Entry{}, fmt.Errorf("refusing to write: %s already exists (%s); if it is a leftover .vault-partial from an interrupted run, remove it by hand and re-run",
			filepath.Join(dstRoot, tmpRel), describe(fi))
	} else if !os.IsNotExist(err) {
		return manifest.Entry{}, fmt.Errorf("refusing to write: %w", err)
	}
	if fi, err := root.Lstat(dstRel); err == nil {
		if !task.Replace {
			return manifest.Entry{}, fmt.Errorf("refusing to write: %s already exists (%s) and this file was planned as new", dstPath, describe(fi))
		}
		if !fi.Mode().IsRegular() {
			return manifest.Entry{}, fmt.Errorf("refusing to replace: %s is %s, not a regular file", dstPath, describe(fi))
		}
	} else if !os.IsNotExist(err) {
		return manifest.Entry{}, fmt.Errorf("refusing to write: %w", err)
	}

	in, err := os.Open(srcPath)
	if err != nil {
		return manifest.Entry{}, err
	}
	defer in.Close()

	// O_EXCL: the staging file must be created by this call. If it exists
	// after the Lstat above (a race, or a case alias Lstat missed), the open
	// fails and nothing is truncated - and nothing is removed either.
	out, err := root.OpenFile(tmpRel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return manifest.Entry{}, fmt.Errorf("refusing to write: %w", err)
	}
	cleanup := func() { root.Remove(tmpRel) }

	hasher := sha256.New()
	hashed := &byteCounter{}
	tee := io.TeeReader(&ctxReader{ctx: ctx, r: in}, io.MultiWriter(hasher, hashed))

	// The destination is fed from the tee, so the hasher and byteCounter see
	// exactly the bytes that land. teeBypass is a test-only seam (nil in
	// production) that lets a test feed io.Copy the raw source instead, proving
	// the hashCovers assertion below refuses a row whose landed bytes were never
	// counted through the tee (B38, review #64).
	var src io.Reader = tee
	if teeBypass != nil {
		src = teeBypass(&ctxReader{ctx: ctx, r: in})
	}
	written, err := io.Copy(out, src)
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
	// Invariant (B38): a row's sha is the hash of the bytes streamed from the
	// source through this writer — io.Copy fed the destination and the hasher
	// from one source reader (the tee), and `written == task.Size` above — so
	// it is the hash of exactly the source bytes, never a stat/scan of the
	// destination after the fact (the shape that produced the B39 empty-dest
	// rows and the B40 torn write). copy.File is the only path that mints a row;
	// refuse to return one whose hash was not computed here.
	sum := hex.EncodeToString(hasher.Sum(nil))
	if err := hashCovers(sum, hashed.n, written); err != nil {
		cleanup()
		return manifest.Entry{}, err
	}
	mt := time.Unix(0, task.MtimeNs)
	// Caveat (os.Root doc): on Unix Root.Chtimes has a documented
	// regular-file→symlink race on its target — if tmpRel were swapped for a
	// symlink between os.Root's internal lstat and the chtimes, the link's
	// target would be timestamped. tmpRel is a .vault-partial this call created
	// O_EXCL under the pinned root fd moments ago, a name nothing else holds, so
	// the swap is not a realistic threat; the race is the residual, noted, not
	// relied upon.
	if err := root.Chtimes(tmpRel, mt, mt); err != nil {
		cleanup()
		return manifest.Entry{}, fmt.Errorf("chtimes: %w", err)
	}
	if err := root.Rename(tmpRel, dstRel); err != nil {
		cleanup()
		return manifest.Entry{}, fmt.Errorf("rename: %w", err)
	}

	return manifest.Entry{
		SourceDisk: disk,
		SourcePath: task.RelPath, // src-relative — used by scan.Build to skip on resume
		DestPath:   dstRel,       // dst-relative — used by verify to find the file
		Size:       task.Size,
		MtimeNs:    task.MtimeNs,
		SHA256:     sum,
		CopiedAt:   time.Now().UnixNano(),
		Status:     "copied",
	}, nil
}

// hashCovers guards the row-minting invariant (B38): the tee fed the hasher and
// the destination from one source reader, so the bytes the hasher saw (hashed)
// must equal the bytes written; if they ever diverge, the sha is not the hash of
// what landed and no row may be recorded. Split out so a tee-bypass is testable.
func hashCovers(sum string, hashed, written int64) error {
	if sum == "" || hashed != written {
		return fmt.Errorf("refusing to record a row: hashed %d bytes but wrote %d", hashed, written)
	}
	return nil
}

// teeBypass is a test-only seam (nil in production): when set, io.Copy reads the
// reader it returns instead of the tee, so the hasher and byteCounter see none
// of the landed bytes. It exists only so a test can prove the real File refuses
// to return a row when the assertion is violated (review #64).
var teeBypass func(in io.Reader) io.Reader

// byteCounter counts the bytes written through it — the tee writes every byte
// it reads here as well as to the hasher, so it counts exactly what was hashed.
type byteCounter struct{ n int64 }

func (c *byteCounter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

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

// Escapes reports why rel, joined to root, would name a file the root does
// not contain: an absolute path, or one that climbs out with "..". "" when
// it stays inside. Lexical only; Under is the physical half.
func Escapes(root, rel string) string {
	if filepath.IsAbs(rel) {
		return fmt.Sprintf("destination %q is absolute; it must be relative to %s", rel, root)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Sprintf("destination %q climbs out of %s", rel, root)
	}
	return ""
}

// Under reports whether path's directory, resolved (symlinks followed on
// the part that exists), lies under the resolved root. Both are made
// absolute first so "." and "/" roots work.
func Under(root, path string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	if rr, err := filepath.EvalSymlinks(r); err == nil {
		r = rr
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	dir, rest := filepath.Dir(p), filepath.Base(p)
	for {
		if rd, err := filepath.EvalSymlinks(dir); err == nil {
			p = filepath.Join(rd, rest)
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = filepath.Dir(dir)
	}
	rel, err := filepath.Rel(r, p)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
