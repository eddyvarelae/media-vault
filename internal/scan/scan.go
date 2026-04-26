package scan

import (
	"context"
	"io/fs"
	"path/filepath"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

type FileTask struct {
	RelPath string
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

func Build(ctx context.Context, m *manifest.Manifest, disk, srcRoot, dstRoot string) (*Plan, error) {
	p := &Plan{}
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == ".DS_Store" || name == ".localized" {
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

		task := FileTask{
			RelPath: rel,
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
