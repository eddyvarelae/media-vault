// Package gap answers one question about a directory - usually an attached
// source SSD: what on it is NOT in the archive, by content?
//
// `vault scan` keys on (source_disk, source_path), so footage archived from
// one physical disk looks entirely new when the same files turn up on
// another; a path-based scan of "Eddy's Media Vault" proposed 9,339 files
// where only 673 were absent. This walks the directory, matches every file
// against the verified rows by size first and by sha256 only when a size
// matches, and reports per top-level folder: files and bytes on the disk,
// archived by content, needs archiving - with the arithmetic on the line.
// It reads the source and the manifest; it writes nothing and copies
// nothing (B22: report-only, decided 2026-09-15).
package gap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

// File is one file on the source that the archive does not hold.
type File struct {
	Rel    string // relative to the root
	Size   int64
	SHA256 string // set when the file was hashed (its size matched a verified row)
}

// Folder is the report for one top-level directory of the root (files
// directly under the root fall under ".").
type Folder struct {
	Name          string
	Files         int
	Bytes         int64
	Archived      int // matched a verified row by size and sha256
	ArchivedBytes int64
	Absent        int
	AbsentBytes   int64
}

type Report struct {
	Root          string
	VerifiedRows  int // verified rows the index was built from
	Folders       []Folder
	Files         int
	Bytes         int64
	Archived      int
	ArchivedBytes int64
	Absent        int
	AbsentBytes   int64
	Hashed        int   // files read in full because their size matched
	HashedBytes   int64 // what "by content" cost
	AbsentFiles   []File
}

// Run walks root against the verified rows of m. The size index is built
// once; a file is hashed only when some verified row has its size.
func Run(ctx context.Context, m *manifest.Manifest, root string) (*Report, error) {
	rows, err := m.VerifiedRows()
	if err != nil {
		return nil, err
	}
	bySize := map[int64]map[string]bool{}
	for _, e := range rows {
		if e.SHA256 == "" {
			continue
		}
		set := bySize[e.Size]
		if set == nil {
			set = map[string]bool{}
			bySize[e.Size] = set
		}
		set[e.SHA256] = true
	}
	r := &Report{Root: root, VerifiedRows: len(rows)}
	folders := map[string]*Folder{}
	folder := func(rel string) *Folder {
		name := "."
		if i := strings.IndexByte(rel, filepath.Separator); i >= 0 {
			name = rel[:i]
		}
		f := folders[name]
		if f == nil {
			f = &Folder{Name: name}
			folders[name] = f
		}
		return f
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if skipFile(d.Name()) || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		f := folder(rel)
		f.Files++
		f.Bytes += info.Size()
		r.Files++
		r.Bytes += info.Size()
		sum := ""
		if set := bySize[info.Size()]; len(set) > 0 {
			sum, err = hashFile(ctx, path)
			if err != nil {
				return err
			}
			r.Hashed++
			r.HashedBytes += info.Size()
			if set[sum] {
				f.Archived++
				f.ArchivedBytes += info.Size()
				r.Archived++
				r.ArchivedBytes += info.Size()
				return nil
			}
		}
		f.Absent++
		f.AbsentBytes += info.Size()
		r.Absent++
		r.AbsentBytes += info.Size()
		r.AbsentFiles = append(r.AbsentFiles, File{Rel: rel, Size: info.Size(), SHA256: sum})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, f := range folders {
		r.Folders = append(r.Folders, *f)
	}
	sort.Slice(r.Folders, func(i, j int) bool { return r.Folders[i].Name < r.Folders[j].Name })
	sort.Slice(r.AbsentFiles, func(i, j int) bool { return r.AbsentFiles[i].Rel < r.AbsentFiles[j].Rel })
	return r, nil
}

// The same things scan leaves out: platform junk, AppleDouble files, the
// tagger's reports/ output, and the NAS trash.
func skipDir(name string) bool {
	switch name {
	case ".Spotlight-V100", ".fseventsd", ".Trashes", ".TemporaryItems", "#recycle", "reports", ".AppleDouble":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func skipFile(name string) bool {
	return name == ".DS_Store" || name == ".localized" || strings.HasPrefix(name, "._")
}

func hashFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
