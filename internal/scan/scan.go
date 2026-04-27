package scan

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

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

type Plan struct {
	ToCopy        []FileTask
	ToRecopy      []FileTask
	SkipCount     int
	BytesToCopy   int64
	BytesToRecopy int64
}

// Build walks srcRoot, applies prefix filtering and routing rules, and groups
// files into copy / recopy / skip buckets based on the manifest. prefix and
// rules may both be empty for the simplest case (preserve sub-paths, no
// filter).
func Build(ctx context.Context, m *manifest.Manifest, disk, srcRoot, dstRoot, prefix string, rules []Rule) (*Plan, error) {
	p := &Plan{}
	prefix = strings.TrimRight(prefix, "/")
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
		if entry == nil {
			p.ToCopy = append(p.ToCopy, task)
			p.BytesToCopy += task.Size
			return nil
		}
		if entry.Size != task.Size || entry.MtimeNs != task.MtimeNs {
			p.ToRecopy = append(p.ToRecopy, task)
			p.BytesToRecopy += task.Size
			return nil
		}
		p.SkipCount++
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p, nil
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
