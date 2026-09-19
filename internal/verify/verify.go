package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

type Result struct {
	Verified  int
	Mismatch  int
	Missing   int
	Errors    int
	Deduped   int // by-reference rows skipped (B51): not hashed, status unchanged
	BytesRead int64
}

// Run re-reads every file at dstRoot listed in the manifest for `disk`,
// re-hashes it, and compares to the recorded sha256.
//   - hash matches → mark verified
//   - hash differs → mark mismatch
//   - file missing → counted; manifest left untouched (so a later copy can fix)
func Run(ctx context.Context, m *manifest.Manifest, disk, dstRoot string, onFile func(sourcePath, destPath, status string)) (*Result, error) {
	return RunWithOptions(ctx, m, disk, dstRoot, false, onFile)
}

// RunWithOptions is Run plus onlyUnverified, which restricts the sweep to rows
// that are not yet `verified`.
//
// This is the fast route to a certifiable disk, not a way around certification:
// certify still requires every row verified. But it deliberately skips the rows
// that a full pass would re-read, and a full pass is the archive's only bit-rot
// check — so the caller must make the skip visible. Opt-in; a bare verify still
// reads everything.
func RunWithOptions(ctx context.Context, m *manifest.Manifest, disk, dstRoot string, onlyUnverified bool, onFile func(sourcePath, destPath, status string)) (*Result, error) {
	list := m.ListByDisk
	if onlyUnverified {
		list = m.ListByDiskUnverified
	}
	entries, err := list(disk)
	if err != nil {
		return nil, fmt.Errorf("list manifest: %w", err)
	}

	// `deduped` rows are by-reference: several rows in one disk can share one
	// dest_path, and hashing per row would re-read the same file once per
	// referencing row. Memoize per resolved path for this invocation.
	//
	// Misses and errors are cached too — otherwise N rows pointing at one
	// missing file each pay a failed open. Cache is per-invocation on purpose:
	// verify runs per-disk, so this covers repeats within a disk (the
	// intra-run dedupe case); cross-disk duplicates are separate runs and
	// should be re-read.
	type hashResult struct {
		sum string
		n   int64
		err error
	}
	seen := map[string]hashResult{}

	res := &Result{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}

		// `deduped` rows are by reference (B51): the bytes live on a verified
		// row of some disk, and this row's dest_path is the OWNER's, resolved
		// under the owner's root — not necessarily under this disk's dstRoot.
		// verify must not hash it (it would read the wrong root and count a
		// spurious Missing) nor promote it (that destroys the by-reference
		// provenance). certify checks these against the owner's verified row.
		if e.Status == "deduped" {
			res.Deduped++
			if onFile != nil {
				onFile(e.SourcePath, e.DestPath, "deduped")
			}
			continue
		}

		// Inventory-only entries have an empty DestPath — fall back to
		// SourcePath, which is the file's location relative to the
		// inventory root.
		rel := e.DestPath
		if rel == "" {
			rel = e.SourcePath
		}
		full := filepath.Join(dstRoot, rel)
		hr, cached := seen[full]
		if !cached {
			sum, n, err := hashFile(ctx, full)
			hr = hashResult{sum: sum, n: n, err: err}
			seen[full] = hr
			// Count bytes only on a real read, so BytesRead keeps meaning
			// actual I/O rather than the sum of row sizes.
			if err == nil {
				res.BytesRead += n
			}
		}
		got, err := hr.sum, hr.err
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				res.Missing++
				if onFile != nil {
					onFile(e.SourcePath, e.DestPath, "missing")
				}
				continue
			}
			res.Errors++
			if onFile != nil {
				onFile(e.SourcePath, e.DestPath, "error: "+err.Error())
			}
			continue
		}

		now := time.Now().UnixNano()
		if got == e.SHA256 {
			if err := m.MarkVerified(disk, e.SourcePath, now); err != nil {
				return res, fmt.Errorf("mark verified: %w", err)
			}
			res.Verified++
			if onFile != nil {
				onFile(e.SourcePath, e.DestPath, "verified")
			}
		} else {
			if err := m.MarkMismatch(disk, e.SourcePath, now); err != nil {
				return res, fmt.Errorf("mark mismatch: %w", err)
			}
			res.Mismatch++
			if onFile != nil {
				onFile(e.SourcePath, e.DestPath, "MISMATCH")
			}
		}
	}
	return res, nil
}

func hashFile(ctx context.Context, path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, &ctxReader{ctx: ctx, r: f})
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
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
