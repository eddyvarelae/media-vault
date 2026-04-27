package move

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	Moves []Move
	Skips []Skip
}

type Move struct {
	Disk        string // source disk in manifest
	SrcRel      string // path relative to srcRoot
	DstRel      string // path relative to dstRoot
	SrcAbs      string // resolved host path
	DstAbs      string // resolved host path
	Size        int64
	SHA256      string
	RuleApplied string // empty if no rule matched
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

	plan := &Plan{}
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
			SHA256:      e.SHA256,
			RuleApplied: applied,
		}
		plan.Moves = append(plan.Moves, mv)
	}
	return plan, nil
}

// Execute applies the plan: rename each file (mv), then update manifest. Run
// per-file so a partial failure doesn't lose progress already committed.
func Execute(ctx context.Context, m *manifest.Manifest, plan *Plan, dstDisk string, onFile func(mv Move, status string)) (*Result, error) {
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

		if _, err := os.Stat(mv.DstAbs); err == nil {
			res.Skipped++
			if onFile != nil {
				onFile(mv, "dst-exists")
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
