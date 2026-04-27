package move

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

// Rule maps a file extension (no leading dot, case-insensitive) to a
// destination subdirectory under the dst root. When a file's extension
// matches, the file is flattened into that subdir (its original sub-path is
// dropped — only the basename survives). Files whose extension matches no
// rule keep their relative path under the dst root.
type Rule struct {
	Extension string // "MP4", "JPG", etc.
	Subdir    string // "Videos", "FlightLogs", etc.
}

type Plan struct {
	Moves   []Move
	Skips   []Skip
	dstRoot string // kept for collision-retry path resolution
}

type Move struct {
	Disk        string // source disk in manifest
	SrcRel      string // path relative to srcRoot
	DstRel      string // path relative to dstRoot
	SrcAbs      string // resolved host path
	DstAbs      string // resolved host path
	Size        int64
	MtimeNs     int64
	SHA256      string
	RuleApplied string // empty if no rule matched
}

// CollisionStrategy controls behavior when the destination path already
// holds a file whose hash differs from the source's.
type CollisionStrategy int

const (
	// CollisionSkip: leave the destination alone, leave the source
	// in place, count as skipped. Default.
	CollisionSkip CollisionStrategy = iota
	// CollisionRenameMtimeYear: rename the incoming file to include
	// `_<source-mtime-year>` before the extension, then move. Used to
	// merge two card-dumps that share filenames after a card format.
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

type Skip struct {
	SrcRel string
	Reason string
}

type Result struct {
	Moved      int
	Skipped    int
	Errors     int
	BytesMoved int64
}

// Build assembles the move plan without touching anything on disk.
//
// srcRoot is the inventory root — i.e. the path that the manifest's
// source_path values are relative to. prefix optionally narrows the move to
// files under a sub-path of srcDisk (and strips that prefix when computing
// the destination path).
func Build(m *manifest.Manifest, srcDisk, srcRoot, prefix, dstRoot string, rules []Rule) (*Plan, error) {
	entries, err := m.ListByDisk(srcDisk)
	if err != nil {
		return nil, err
	}
	prefix = strings.TrimRight(prefix, "/")

	plan := &Plan{dstRoot: dstRoot}
	for _, e := range entries {
		rel := e.SourcePath
		if prefix != "" {
			if !strings.HasPrefix(rel, prefix+"/") && rel != prefix {
				continue
			}
			rel = strings.TrimPrefix(rel, prefix+"/")
		}
		dstRel, applied := route(rel, rules)
		mv := Move{
			Disk:        srcDisk,
			SrcRel:      e.SourcePath,
			DstRel:      dstRel,
			SrcAbs:      filepath.Join(srcRoot, e.SourcePath),
			DstAbs:      filepath.Join(dstRoot, dstRel),
			Size:        e.Size,
			MtimeNs:     e.MtimeNs,
			SHA256:      e.SHA256,
			RuleApplied: applied,
		}
		plan.Moves = append(plan.Moves, mv)
	}
	return plan, nil
}

// renameWithMtimeYear inserts _<year> before the file extension, derived
// from the file's mtime. e.g. DCIM/DSC00012.JPG (2024) → DCIM/DSC00012_2024.JPG.
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

// Execute applies the plan: rename each file (mv), then update manifest. Run
// per-file so a partial failure doesn't lose progress already committed.
func Execute(ctx context.Context, m *manifest.Manifest, plan *Plan, dstDisk string, onCollision CollisionStrategy, onFile func(mv Move, status string)) (*Result, error) {
	res := &Result{}
	for _, mv := range plan.Moves {
		if err := ctx.Err(); err != nil {
			return res, err
		}

		if _, err := os.Stat(mv.SrcAbs); err != nil {
			if os.IsNotExist(err) {
				res.Skipped++
				if onFile != nil {
					onFile(mv, "src-missing")
				}
				continue
			}
			res.Errors++
			if onFile != nil {
				onFile(mv, "stat-error: "+err.Error())
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(mv.DstAbs), 0o755); err != nil {
			res.Errors++
			if onFile != nil {
				onFile(mv, "mkdir-error: "+err.Error())
			}
			continue
		}

		// Wrap the dst-exists check so collision handling can retry with
		// a new destination path (e.g. mtime-year suffix).
	collisionRetry:
		if _, err := os.Stat(mv.DstAbs); err == nil {
			// Destination already has a file. Look it up in the manifest
			// to decide if this is a safe duplicate or a genuine collision.
			existing, _ := m.Lookup(dstDisk, mv.DstRel)
			if existing != nil && existing.SHA256 == mv.SHA256 {
				// Same bytes already at dst — drop the source as a
				// verified duplicate.
				if err := os.Remove(mv.SrcAbs); err != nil {
					res.Errors++
					if onFile != nil {
						onFile(mv, "dedup-rm-error: "+err.Error())
					}
					continue
				}
				if err := m.DeleteEntry(mv.Disk, mv.SrcRel); err != nil {
					res.Errors++
					if onFile != nil {
						onFile(mv, "dedup-manifest-error: "+err.Error())
					}
					continue
				}
				res.Skipped++
				if onFile != nil {
					onFile(mv, "deduped")
				}
				continue
			}
			// Genuine collision (different hash) or unmanaged file.
			if onCollision == CollisionRenameMtimeYear && existing != nil {
				newRel := renameWithMtimeYear(mv.DstRel, mv.MtimeNs)
				if newRel != mv.DstRel {
					mv.DstRel = newRel
					mv.DstAbs = filepath.Join(plan.dstRoot, newRel)
					if onFile != nil {
						onFile(mv, "renamed-on-collision → "+newRel)
					}
					goto collisionRetry
				}
			}
			res.Skipped++
			if onFile != nil {
				if existing != nil {
					onFile(mv, "collision (different hash at dst)")
				} else {
					onFile(mv, "dst-exists (unmanaged)")
				}
			}
			continue
		}

		if err := os.Rename(mv.SrcAbs, mv.DstAbs); err != nil {
			res.Errors++
			if onFile != nil {
				onFile(mv, "rename-error: "+err.Error())
			}
			continue
		}

		if err := m.MoveEntry(mv.Disk, mv.SrcRel, dstDisk, mv.DstRel); err != nil {
			// host file moved but manifest didn't — try to roll back the rename
			_ = os.Rename(mv.DstAbs, mv.SrcAbs)
			res.Errors++
			if onFile != nil {
				onFile(mv, fmt.Sprintf("manifest-error (rolled back rename): %v", err))
			}
			continue
		}

		res.Moved++
		res.BytesMoved += mv.Size
		if onFile != nil {
			onFile(mv, "moved")
		}
	}
	return res, nil
}

// route picks the destination relative path for a source relative path.
// If a rule matches the file's extension, the file is flattened into the
// rule's subdir (basename only). Otherwise the original sub-path is preserved.
func route(srcRel string, rules []Rule) (dst string, applied string) {
	ext := strings.TrimPrefix(strings.ToUpper(filepath.Ext(srcRel)), ".")
	for _, r := range rules {
		if strings.EqualFold(r.Extension, ext) {
			return filepath.Join(r.Subdir, filepath.Base(srcRel)), r.Extension + "=" + r.Subdir
		}
	}
	return srcRel, ""
}

// ParseRules parses ["MP4=Videos", "SRT=FlightLogs"] into Rule values.
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
