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
	BytesRead int64
}

// Run re-reads every file at dstRoot listed in the manifest for `disk`,
// re-hashes it, and compares to the recorded sha256.
//   - hash matches → mark verified
//   - hash differs → mark mismatch
//   - file missing → counted; manifest left untouched (so a later copy can fix)
func Run(ctx context.Context, m *manifest.Manifest, disk, dstRoot string, onFile func(path, status string)) (*Result, error) {
	entries, err := m.ListByDisk(disk)
	if err != nil {
		return nil, fmt.Errorf("list manifest: %w", err)
	}

	res := &Result{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}

		// Inventory-only entries have an empty DestPath — fall back to
		// SourcePath, which is the file's location relative to the
		// inventory root.
		rel := e.DestPath
		if rel == "" {
			rel = e.SourcePath
		}
		full := filepath.Join(dstRoot, rel)
		got, n, err := hashFile(ctx, full)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				res.Missing++
				if onFile != nil {
					onFile(e.DestPath, "missing")
				}
				continue
			}
			res.Errors++
			if onFile != nil {
				onFile(e.DestPath, "error: "+err.Error())
			}
			continue
		}
		res.BytesRead += n

		now := time.Now().UnixNano()
		if got == e.SHA256 {
			if err := m.MarkVerified(disk, e.SourcePath, now); err != nil {
				return res, fmt.Errorf("mark verified: %w", err)
			}
			res.Verified++
			if onFile != nil {
				onFile(e.DestPath, "verified")
			}
		} else {
			if err := m.MarkMismatch(disk, e.SourcePath, now); err != nil {
				return res, fmt.Errorf("mark mismatch: %w", err)
			}
			res.Mismatch++
			if onFile != nil {
				onFile(e.DestPath, "MISMATCH")
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
