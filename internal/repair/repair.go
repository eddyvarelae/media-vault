// Package repair fixes manifest rows whose dest_path no longer names the file
// that holds their bytes, without touching anything else about the row.
//
// The case it exists for (B24): 195 media-sonya6700 rows were copied with
// dest roots one level too deep (SonyA6700/CLIP, SonyA6700/DCIM), so their
// dest_path is `C2286M01_2025.XML` where the file is `CLIP/C2286M01_2025.XML`.
// The bytes are fine; the pointer is wrong; verify reports them missing and
// certify can never pass. A row is rewritten only when a file of the same
// basename one directory level below the row's directory has the row's
// exact size AND sha256 — the hash the row already carries is the proof.
package repair

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

// Outcome says what Plan decided for one row whose dest_path is missing.
type Outcome string

const (
	Repairable Outcome = "REPAIR"    // exactly one candidate matches size and sha256
	NotFound   Outcome = "NOT FOUND" // no candidate matches
	Ambiguous  Outcome = "AMBIGUOUS" // more than one candidate matches
)

// Change is one missing-dest row and what was found for it.
type Change struct {
	Row        manifest.Entry
	Outcome    Outcome
	NewDest    string   // set when Outcome == Repairable
	Candidates []string // every matching path, relative to the root (Ambiguous)
}

// Plan is the result of looking at every row of a disk.
type Plan struct {
	Checked     int // rows with a dest_path
	Intact      int // dest_path exists under the root; not examined further
	NoDest      int // rows without a dest_path (inventoried); not this tool's job
	Changes     []Change
	BytesHashed int64
}

// Counts returns how many changes fell in each outcome.
func (p *Plan) Counts() (repairable, notFound, ambiguous int) {
	for _, c := range p.Changes {
		switch c.Outcome {
		case Repairable:
			repairable++
		case NotFound:
			notFound++
		case Ambiguous:
			ambiguous++
		}
	}
	return
}

// Build examines every row of disk against root. It reads candidate files
// (size first, hash only on a size match) and writes nothing.
func Build(ctx context.Context, m *manifest.Manifest, disk, root string) (*Plan, error) {
	rows, err := m.ListByDisk(disk)
	if err != nil {
		return nil, err
	}
	p := &Plan{}
	for _, row := range rows {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if row.DestPath == "" {
			p.NoDest++
			continue
		}
		p.Checked++
		if _, err := os.Stat(filepath.Join(root, row.DestPath)); err == nil {
			p.Intact++
			continue
		}
		c, err := locate(ctx, root, row, &p.BytesHashed)
		if err != nil {
			return nil, err
		}
		p.Changes = append(p.Changes, c)
	}
	return p, nil
}

// locate looks one directory level below the row's own directory for a file
// with the row's basename, and keeps only those whose size and sha256 match.
func locate(ctx context.Context, root string, row manifest.Entry, hashed *int64) (Change, error) {
	c := Change{Row: row, Outcome: NotFound}
	dir := filepath.Dir(row.DestPath)
	base := filepath.Base(row.DestPath)
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil // the row's own directory is gone too; nothing to look in
		}
		return c, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := filepath.Join(dir, e.Name(), base)
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil || info.IsDir() || info.Size() != row.Size {
			continue
		}
		sum, err := hashFile(ctx, filepath.Join(root, rel))
		if err != nil {
			return c, err
		}
		*hashed += info.Size()
		if sum == row.SHA256 {
			c.Candidates = append(c.Candidates, rel)
		}
	}
	switch len(c.Candidates) {
	case 0:
	case 1:
		c.Outcome, c.NewDest = Repairable, c.Candidates[0]
	default:
		c.Outcome = Ambiguous
	}
	return c, nil
}

// Apply rewrites dest_path for every Repairable change, one row at a time,
// calling report after each successful write. Nothing else about the row
// moves: status stays as it was, so `verify` still has to promote it.
func Apply(m *manifest.Manifest, p *Plan, report func(Change)) (int, error) {
	n := 0
	for _, c := range p.Changes {
		if c.Outcome != Repairable {
			continue
		}
		if err := m.UpdateDestPath(c.Row.SourceDisk, c.Row.SourcePath, c.NewDest); err != nil {
			return n, fmt.Errorf("%s: %w", c.Row.SourcePath, err)
		}
		n++
		if report != nil {
			report(c)
		}
	}
	return n, nil
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
