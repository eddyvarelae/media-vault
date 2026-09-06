package scan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

// Rule maps an extension (no leading dot, case-insensitive) to a destination
// subdirectory under the dst root. When a file's extension matches, it is
// flattened into that subdir (basename only). Files matching no rule keep
// their relative path.
type Rule struct {
	Extension string
	Subdir    string
}

func ParseRules(raw []string) ([]Rule, error) {
	out := make([]Rule, 0, len(raw))
	for _, s := range raw {
		i := strings.IndexByte(s, '=')
		if i < 1 || i == len(s)-1 {
			return nil, fmt.Errorf("invalid rule %q (expected EXT=SUBDIR)", s)
		}
		out = append(out, Rule{Extension: s[:i], Subdir: s[i+1:]})
	}
	return out, nil
}

type FileTask struct {
	RelPath string // path relative to srcRoot (where the file lives in source)
	DstRel  string // path relative to dstRoot (where the file should land)
	Size    int64
	MtimeNs int64
}

// DedupeHit records a source file whose bytes are already archived.
//
// Two kinds, and the difference matters for when the manifest row may be
// written. An inter-disk hit references a row that is already `verified`, so
// its destination is known good and the row can be written immediately. An
// intra-run hit references another file in THIS run that has not been copied
// yet: its final DstRel is not settled until the collision policy has run, and
// the copy can still fail. Writing that row up front is how a `deduped` row
// ends up pointing at a foreign file, a renamed-away path, or nothing.
type DedupeHit struct {
	Task     FileTask
	Existing manifest.Entry // inter-disk: the verified row already holding these bytes
	IntraRun bool           // if set, Existing is a placeholder; RefRelPath is the truth
	RefRel   string         // intra-run: source-relative path of the file being referenced
}

type Plan struct {
	ToCopy           []FileTask
	ToRecopy         []FileTask
	SkipCount        int
	Deduped          []DedupeHit // content already archived elsewhere
	BytesSkipContent int64
	HashedFiles      int        // files read to resolve a size collision
	DedupeEligible   int        // verified rows content dedup could match against
	DstCollisions    []FileTask // dst file already exists (would overwrite)
	BytesToCopy      int64
	BytesToRecopy    int64
}

type CollisionStrategy int

const (
	CollisionSkip CollisionStrategy = iota
	CollisionRenameMtimeYear
)

func ParseCollision(s string) (CollisionStrategy, error) {
	switch s {
	case "", "skip":
		return CollisionSkip, nil
	case "rename-mtime-year":
		return CollisionRenameMtimeYear, nil
	default:
		return 0, fmt.Errorf("unknown --on-collision value %q (want: skip | rename-mtime-year)", s)
	}
}

// Build walks srcRoot, applies prefix filtering and routing rules, and groups
// files into copy / recopy / skip / collision buckets based on the manifest
// and the destination filesystem state. onCollision controls how dst files
// that already exist (with no manifest entry to match) are handled.
func Build(ctx context.Context, m *manifest.Manifest, disk, srcRoot, dstRoot, prefix string, rules []Rule, onCollision CollisionStrategy) (*Plan, error) {
	return BuildWithOptions(ctx, m, disk, srcRoot, dstRoot, prefix, rules, onCollision, false)
}

// BuildWithOptions is Build plus dedupeContent.
//
// The manifest keys on (source_disk, source_path), so the same footage arriving
// from a second physical disk looks entirely new: a plain scan of a 9,827-file
// directory whose contents were already archived from another disk proposed
// copying 9,339 of them. With --on-collision rename-mtime-year that does not
// overwrite anything, it just fills the archive with renamed duplicates.
//
// dedupeContent closes that hole by asking whether this file's CONTENT is
// already archived anywhere, using the sha256 the manifest already stores. It
// is opt-in because it reads candidate files, and because "already archived
// elsewhere" is a judgement some workflows may not want made for them.
func BuildWithOptions(ctx context.Context, m *manifest.Manifest, disk, srcRoot, dstRoot, prefix string, rules []Rule, onCollision CollisionStrategy, dedupeContent bool) (*Plan, error) {
	p := &Plan{}
	seenThisRun := map[string]string{} // sha256 -> source-relative path queued to copy
	sizeCount := map[int64]int{}
	prefix = strings.TrimRight(prefix, "/")
	if dedupeContent {
		n, err := m.CountVerifiedHashable()
		if err != nil {
			return nil, err
		}
		p.DedupeEligible = n
		// Cheap stat-only pre-pass. A file whose size is unique within this
		// source AND absent from the archive cannot be a duplicate of
		// anything, so it never needs hashing. Without this count, two
		// identical NEW files both get queued: neither is in the archive, so
		// neither would be hashed, so nothing detects that they match.
		if err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if isJunkDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if isJunkFile(d.Name()) {
				return nil
			}
			rel, relErr := filepath.Rel(srcRoot, path)
			if relErr != nil {
				return relErr
			}
			if prefix != "" && !strings.HasPrefix(rel, prefix+"/") && rel != prefix {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			sizeCount[info.Size()]++
			return nil
		}); err != nil {
			return nil, err
		}
	}
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := d.Name()
		if d.IsDir() {
			if isJunkDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if isJunkFile(name) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if prefix != "" {
			if !strings.HasPrefix(rel, prefix+"/") && rel != prefix {
				return nil
			}
		}

		pendingHash := ""
		task := FileTask{
			RelPath: rel,
			DstRel:  route(stripPrefix(rel, prefix), rules),
			Size:    info.Size(),
			MtimeNs: info.ModTime().UnixNano(),
		}

		entry, err := m.Lookup(disk, rel)
		if err != nil {
			return err
		}
		if entry != nil {
			if entry.Size == task.Size && entry.MtimeNs == task.MtimeNs {
				p.SkipCount++
				return nil
			}
			p.ToRecopy = append(p.ToRecopy, task)
			p.BytesToRecopy += task.Size
			return nil
		}

		// New according to (disk, path) — but the same bytes may already be
		// archived from another disk, possibly under another name.
		if dedupeContent {
			known, hErr := m.VerifiedBySize(task.Size)
			if hErr != nil {
				return hErr
			}
			// Also catch two identical NEW files within this one source: the
			// plan is built up front, so without this both get queued and we
			// reproduce the duplicate problem intra-disk.
			if len(known) > 0 || sizeCount[task.Size] > 1 {
				sum, sErr := hashFile(ctx, path)
				if sErr != nil {
					return sErr
				}
				p.HashedFiles++
				if e, dup := known[sum]; dup {
					p.Deduped = append(p.Deduped, DedupeHit{Task: task, Existing: e})
					p.BytesSkipContent += task.Size
					return nil
				}
				if ref, dup := seenThisRun[sum]; dup {
					p.Deduped = append(p.Deduped, DedupeHit{
						Task:     task,
						IntraRun: true,
						RefRel:   ref,
					})
					p.BytesSkipContent += task.Size
					return nil
				}
				// Do NOT register as a reference target yet — this file may
				// still be renamed by the collision policy, or dropped into
				// DstCollisions and never copied at all. Registration happens
				// below, once it is committed to ToCopy with a final DstRel.
				pendingHash = sum
			}
		}

		// New file according to the manifest. Before queueing it, make
		// sure the destination path isn't already occupied — refuse to
		// overwrite without explicit handling.
		dstFull := filepath.Join(dstRoot, task.DstRel)
		if _, statErr := os.Stat(dstFull); statErr == nil {
			if onCollision == CollisionRenameMtimeYear {
				task.DstRel = renameWithMtimeYear(task.DstRel, task.MtimeNs)
				dstFull = filepath.Join(dstRoot, task.DstRel)
				if _, again := os.Stat(dstFull); again == nil {
					p.DstCollisions = append(p.DstCollisions, task)
					return nil
				}
			} else {
				p.DstCollisions = append(p.DstCollisions, task)
				return nil
			}
		}

		if pendingHash != "" {
			seenThisRun[pendingHash] = task.RelPath
		}
		p.ToCopy = append(p.ToCopy, task)
		p.BytesToCopy += task.Size
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func renameWithMtimeYear(rel string, mtimeNs int64) string {
	year := time.Unix(0, mtimeNs).Year()
	dir := filepath.Dir(rel)
	base := filepath.Base(rel)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	newBase := fmt.Sprintf("%s_%d%s", stem, year, ext)
	if dir == "." || dir == "" {
		return newBase
	}
	return filepath.Join(dir, newBase)
}

func stripPrefix(rel, prefix string) string {
	if prefix == "" {
		return rel
	}
	return strings.TrimPrefix(rel, prefix+"/")
}

func route(rel string, rules []Rule) string {
	if len(rules) == 0 {
		return rel
	}
	ext := strings.TrimPrefix(strings.ToUpper(filepath.Ext(rel)), ".")
	for _, r := range rules {
		if strings.EqualFold(r.Extension, ext) {
			return filepath.Join(r.Subdir, filepath.Base(rel))
		}
	}
	return rel
}

func isJunkFile(name string) bool {
	if name == ".DS_Store" || name == ".localized" {
		return true
	}
	if len(name) > 2 && name[0] == '.' && name[1] == '_' {
		return true
	}
	return false
}

// Code/dev directories never belong in a media archive.
func isJunkDir(name string) bool {
	switch name {
	case "node_modules", ".git", ".svn", ".hg", "__pycache__",
		".pytest_cache", ".tox", ".venv", "venv", ".gradle", ".m2",
		"target", ".next", ".nuxt", ".turbo", ".pnpm-store",
		"bower_components", ".terraform",
		".DS_Store", ".AppleDouble", ".fseventsd",
		".Spotlight-V100", ".TemporaryItems", ".Trashes":
		return true
	}
	return false
}

// hashFile returns the hex sha256 of a file's contents. Honours ctx mid-file:
// the only other cancellation check is per-WalkDir-entry, so without this a
// Ctrl-C during a 50 GB hash waits for the whole file.
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
