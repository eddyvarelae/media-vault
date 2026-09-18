// Package repair fixes manifest rows whose dest_path no longer names the file
// that holds their bytes, without touching anything else about the row.
//
// The case it exists for (B24): 195 media-sonya6700 rows were copied with
// dest roots one level too deep (SonyA6700/CLIP, SonyA6700/DCIM), so their
// dest_path is `C2286M01_2025.XML` where the file is `CLIP/C2286M01_2025.XML`.
// The bytes are fine; the pointer is wrong; verify reports them missing and
// certify can never pass. A row is rewritten only when a regular file of the
// same basename one directory level below the row's directory has the row's
// exact size AND sha256 — the hash the row already carries is the proof —
// and only when that file is physically under the root, reached through
// real directories, and claimed by no other row.
package repair

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/eddyvarelae/media-vault/internal/manifest"
)

// Outcome says what Plan decided for one row whose dest_path is not a
// regular file under the root.
type Outcome string

const (
	Repairable Outcome = "REPAIR"     // exactly one candidate matches size and sha256 and nobody claims it
	NotFound   Outcome = "NOT FOUND"  // no candidate matches
	Ambiguous  Outcome = "AMBIGUOUS"  // more than one candidate matches
	Owned      Outcome = "OWNED"      // a matching candidate is already another row's dest_path
	NotAFile   Outcome = "NOT A FILE" // something is at dest_path, but not a regular file
	Unsafe     Outcome = "UNSAFE"     // dest_path leaves the root or passes through a symlink; not examined
	Conflict   Outcome = "CONFLICT"   // two rows of this run would repair to the same file
)

// Change is one unresolved row and what was found for it.
type Change struct {
	Row        manifest.Entry
	Outcome    Outcome
	NewDest    string   // set when Outcome == Repairable
	Candidates []string // every matching path, relative to the root (Ambiguous, Owned)
	Owner      string   // Owned: "<disk>:<source_path>" of the row that claims the candidate
	Detail     string   // NotAFile / Unsafe / Conflict: what was found instead
}

// Plan is the result of looking at every row of a disk.
type Plan struct {
	Checked     int // rows with a dest_path
	Intact      int // dest_path is a regular file under the root; not examined further
	NoDest      int // rows without a dest_path - located by source_path (verify's rule), whatever their status; not this tool's job
	Changes     []Change
	BytesHashed int64

	claims claimIndex // every row's dest_path by physical key, plus this plan's reservations
	root   string
}

// Counts returns how many changes are repairable, how many are not, and
// the breakdown by outcome. Everything that is not Repairable is unresolved.
func (p *Plan) Counts() (repairable, unresolved int, byOutcome map[Outcome]int) {
	byOutcome = map[Outcome]int{}
	for _, c := range p.Changes {
		byOutcome[c.Outcome]++
		if c.Outcome == Repairable {
			repairable++
		} else {
			unresolved++
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
	claims, err := buildClaims(m, root)
	if err != nil {
		return nil, err
	}
	p := &Plan{claims: claims, root: root}
	reserved := map[string]int{} // physical key of a planned NewDest -> index in p.Changes
	for _, row := range rows {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if row.DestPath == "" {
			p.NoDest++
			continue
		}
		p.Checked++
		// The same discipline before "intact" as before a repair: the row's
		// directory must be real directories under the root, else Lstat on
		// the leaf is looking through a link at some other place.
		exists, why := containedDir(root, filepath.Dir(row.DestPath))
		if why != "" {
			p.Changes = append(p.Changes, Change{Row: row, Outcome: Unsafe, Detail: why})
			continue
		}
		full := filepath.Join(root, row.DestPath)
		if !exists {
			// Nothing to look at and nothing to look in: NOT FOUND.
			p.Changes = append(p.Changes, Change{Row: row, Outcome: NotFound})
			continue
		}
		if fi, err := os.Lstat(full); err == nil {
			// Anything but a regular file is not the archived copy, however
			// it got there; the row stays unbacked and says so.
			if !fi.Mode().IsRegular() {
				p.Changes = append(p.Changes, Change{Row: row, Outcome: NotAFile, Detail: describe(fi)})
				continue
			}
			if !under(root, full) {
				p.Changes = append(p.Changes, Change{Row: row, Outcome: Unsafe, Detail: "resolves outside the root"})
				continue
			}
			p.Intact++
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		c, err := locate(ctx, root, row, claims, &p.BytesHashed)
		if err != nil {
			return nil, err
		}
		if c.Outcome == Repairable {
			// Reserve the file for this row. A second row wanting the same
			// file is a conflict for both: the manifest would otherwise end
			// up with two rows claiming one archived copy, and which of them
			// is the real one is not this tool's call.
			k := physKey(root, c.NewDest)
			if i, taken := reserved[k]; taken {
				other := &p.Changes[i]
				other.Outcome, other.NewDest, other.Detail = Conflict, "", "also wanted by "+row.SourceDisk+":"+row.SourcePath
				c.Outcome, c.NewDest, c.Detail = Conflict, "", "also wanted by "+other.Row.SourceDisk+":"+other.Row.SourcePath
			} else {
				reserved[k] = len(p.Changes)
			}
		}
		p.Changes = append(p.Changes, c)
	}
	return p, nil
}

// locate looks one directory level below the row's own directory for a
// regular file with the row's basename, and keeps only those whose size and
// sha256 match. Every directory on the way must be a real directory under
// the root - a symlink anywhere in the path could point the search, and the
// pointer it would write, at bytes outside the archive.
func locate(ctx context.Context, root string, row manifest.Entry, claims claimIndex, hashed *int64) (Change, error) {
	c := Change{Row: row, Outcome: NotFound}
	dir := filepath.Dir(row.DestPath)
	base := filepath.Base(row.DestPath)
	if exists, why := containedDir(root, dir); !exists || why != "" {
		return c, nil // Build already handled a missing or unsafe directory; this is a guard
	}
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return c, err
	}
	for _, e := range entries {
		if !e.IsDir() { // a DirEntry's type is from lstat: a symlink to a directory is not a directory here
			continue
		}
		rel := filepath.Join(dir, e.Name(), base)
		full := filepath.Join(root, rel)
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() || info.Size() != row.Size {
			continue
		}
		if !under(root, full) {
			continue
		}
		sum, err := hashFile(ctx, full)
		if err != nil {
			return c, err
		}
		*hashed += info.Size()
		if sum != row.SHA256 {
			continue
		}
		c.Candidates = append(c.Candidates, rel)
		if owner, taken := claims[physKey(root, rel)]; taken {
			c.Outcome, c.Owner = Owned, owner.SourceDisk+":"+owner.SourcePath
		}
	}
	if c.Outcome == Owned {
		return c, nil // another row already says these bytes are its archived copy; a human decides
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

// containedDir checks that rel names a real directory under root reached
// only through real directories: rel must not climb out lexically, and no
// component may be a symlink. "." is the root itself. exists is false when
// some component is simply absent (nothing to look in - NOT FOUND); unsafe
// is non-empty when a component is a symlink, not a directory, or the path
// leaves the root - such a row is never examined at all.
func containedDir(root, rel string) (exists bool, unsafe string) {
	rel = filepath.Clean(rel)
	if filepath.IsAbs(rel) {
		return false, "dest_path is absolute"
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, "dest_path leaves the root"
	}
	cur, sofar := root, ""
	if rel != "." {
		for _, comp := range strings.Split(rel, string(filepath.Separator)) {
			cur, sofar = filepath.Join(cur, comp), filepath.Join(sofar, comp)
			fi, err := os.Lstat(cur)
			if err != nil {
				return false, ""
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				return false, "directory " + sofar + " is a symlink"
			}
			if !fi.IsDir() {
				return false, sofar + " is not a directory"
			}
		}
	}
	return true, ""
}

// under proves containment the other way round: the resolved candidate lies
// under the resolved root, compared component-wise on absolute paths so a
// root of "." or "/" works. The component walk above already refuses
// symlinks; this is the check that does not depend on that walk being right.
func under(root, full string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	if r, err = filepath.EvalSymlinks(r); err != nil {
		return false
	}
	f, err := filepath.Abs(full)
	if err != nil {
		return false
	}
	if f, err = filepath.EvalSymlinks(f); err != nil {
		return false
	}
	rel, err := filepath.Rel(r, f)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// claimIndex maps the physical location of every row's file (dest_path,
// or source_path when that is empty; any disk, any status) to that row, so
// a candidate some row already claims is never handed to a second one.
// Physical as in scan: root-joined, cleaned, case-folded.
type claimIndex map[string]manifest.Entry

func buildClaims(m *manifest.Manifest, root string) (claimIndex, error) {
	rows, err := m.AllRows()
	if err != nil {
		return nil, err
	}
	idx := make(claimIndex, len(rows))
	for _, e := range rows {
		// An empty dest_path locates its file by source_path (verify's
		// rule; restore applies the same), so that file is claimed too.
		rel := e.DestPath
		if rel == "" {
			rel = e.SourcePath
		}
		k := physKey(root, rel)
		if _, dup := idx[k]; !dup {
			idx[k] = e
		}
	}
	return idx, nil
}

func physKey(root, rel string) string {
	return strings.ToLower(filepath.Clean(filepath.Join(root, rel)))
}

// Apply rewrites dest_path for every Repairable change, one row at a time,
// calling report after each successful write. Nothing else about the row
// moves: status stays as it was, so `verify` still has to promote it.
func Apply(m *manifest.Manifest, p *Plan, report func(Change)) (int, error) {
	n := 0
	written := map[string]string{}
	for _, c := range p.Changes {
		if c.Outcome != Repairable {
			continue
		}
		// Planning reserved every NewDest; this is the same rule enforced
		// at the moment of writing, so no sequence of writes can leave two
		// rows on one file even if the plan was built by someone else.
		k := physKey(p.root, c.NewDest)
		if owner, taken := p.claims[k]; taken {
			return n, fmt.Errorf("%s: %s is already the dest_path of %s:%s", c.Row.SourcePath, c.NewDest, owner.SourceDisk, owner.SourcePath)
		}
		if prev, taken := written[k]; taken {
			return n, fmt.Errorf("%s: %s was just given to %s", c.Row.SourcePath, c.NewDest, prev)
		}
		if err := m.UpdateDestPath(c.Row.SourceDisk, c.Row.SourcePath, c.NewDest); err != nil {
			return n, fmt.Errorf("%s: %w", c.Row.SourcePath, err)
		}
		written[k] = c.Row.SourcePath
		n++
		if report != nil {
			report(c)
		}
	}
	return n, nil
}

func describe(fi os.FileInfo) string {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return "a symlink"
	case fi.IsDir():
		return "a directory"
	default:
		return fi.Mode().String()
	}
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
