package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

type Result struct {
	Hashed     int
	Skipped    int
	BytesRead  int64
	Errors     int
}

// junk file names we never inventory
var junkNames = map[string]bool{
	".DS_Store":     true,
	".localized":    true,
	".AppleDouble":  true,
	".fseventsd":    true,
	".Spotlight-V100": true,
	".TemporaryItems": true,
	".Trashes":      true,
}

// directory names whose entire subtree we skip — these blow up inventory
// time with millions of tiny files and never belong in a media archive.
var skipDirs = map[string]bool{
	"node_modules":   true,
	".git":           true,
	".svn":           true,
	".hg":            true,
	"__pycache__":    true,
	".pytest_cache":  true,
	".tox":           true,
	".venv":          true,
	"venv":           true,
	".gradle":        true,
	".m2":            true,
	"target":         true, // Rust / Java
	".next":          true,
	".nuxt":          true,
	".turbo":         true,
	".pnpm-store":    true,
	"bower_components": true,
	".terraform":     true,
}

func isJunk(name string) bool {
	if junkNames[name] {
		return true
	}
	// macOS resource fork files (._GX010007.MP4 etc.)
	if len(name) > 2 && name[0] == '.' && name[1] == '_' {
		return true
	}
	return false
}

// Run walks `root` and writes an inventory entry (sha256 + size + mtime) for
// every regular file into the manifest under `disk`. Files already in the
// manifest with matching size + mtime are skipped (resumable).
func Run(ctx context.Context, m *manifest.Manifest, disk, root string, onFile func(rel, status string)) (*Result, error) {
	res := &Result{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			res.Errors++
			if onFile != nil {
				onFile(path, "walk-error: "+err.Error())
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if isJunk(d.Name()) || skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if isJunk(d.Name()) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			res.Errors++
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if existing, _ := m.Lookup(disk, rel); existing != nil {
			if existing.Size == info.Size() && existing.MtimeNs == info.ModTime().UnixNano() {
				res.Skipped++
				if onFile != nil {
					onFile(rel, "skip")
				}
				return nil
			}
		}

		hash, err := hashFile(ctx, path)
		if err != nil {
			res.Errors++
			if onFile != nil {
				onFile(rel, "hash-error: "+err.Error())
			}
			return nil
		}

		entry := manifest.Entry{
			SourceDisk: disk,
			SourcePath: rel,
			DestPath:   "",
			Size:       info.Size(),
			MtimeNs:    info.ModTime().UnixNano(),
			SHA256:     hash,
			CopiedAt:   time.Now().UnixNano(),
			Status:     "inventoried",
		}
		if err := m.Upsert(entry); err != nil {
			res.Errors++
			return fmt.Errorf("upsert %s: %w", rel, err)
		}
		res.Hashed++
		res.BytesRead += info.Size()
		if onFile != nil {
			onFile(rel, "hashed")
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	return res, nil
}

func hashFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
