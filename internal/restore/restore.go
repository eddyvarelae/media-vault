// Package restore replaces one row's destination file on purpose.
//
// The copy guard exists to make sure a `verified` destination is never
// overwritten. B40 is the case where that is exactly what has to happen: a
// torn write was hashed after the fact, the manifest and a certificate
// attest 4 MiB of zeros, and the intact original sits on another disk.
// This is the deliberate path: one named row, a replacement whose sha256
// the operator states up front, every fact printed and checked before a
// byte moves, and the row set back to `copied` so that only `verify` can
// promote it again - by hashing.
package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eddyvarelae/media-vault/internal/copy"
	"github.com/eddyvarelae/media-vault/internal/manifest"
	"github.com/eddyvarelae/media-vault/internal/scan"
)

// ErrRefused wraps every reason restore will not proceed. The message says
// which; nothing has been written when it is returned.
var ErrRefused = errors.New("restore refused")

// Plan is every fact about one restore, gathered before anything is written.
type Plan struct {
	Row          manifest.Entry // the row as it is now
	DestRel      string         // dest_path, or source_path when dest_path is empty (verify's rule)
	DestFull     string
	CurrentSHA   string // of the bytes at DestFull now
	CurrentSize  int64
	RowAttests   bool // CurrentSHA == Row.SHA256
	Claimants    []manifest.Entry
	Replacement  string
	ReplaceSHA   string
	ReplaceSize  int64
	ReplaceMtime int64
	ExpectSHA    string
	AlreadyThere bool // the destination already holds the expected bytes
}

// Build gathers and checks. It reads the destination and the replacement in
// full and writes nothing. A refusal is ErrRefused with the reason.
func Build(ctx context.Context, m *manifest.Manifest, disk, sourcePath, replacement, destRoot, expectSHA string) (*Plan, error) {
	expectSHA = strings.ToLower(strings.TrimSpace(expectSHA))
	if len(expectSHA) != 64 {
		return nil, fmt.Errorf("%w: --expect-sha must be a full 64-hex sha256, got %q", ErrRefused, expectSHA)
	}
	row, err := m.Lookup(disk, sourcePath)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("%w: no row for %s:%s", ErrRefused, disk, sourcePath)
	}
	p := &Plan{Row: *row, Replacement: replacement, ExpectSHA: expectSHA}
	p.DestRel = row.DestPath
	if p.DestRel == "" {
		p.DestRel = row.SourcePath
	}
	p.DestFull = filepath.Join(destRoot, p.DestRel)

	// The destination, physically: inside the root (a row path spelled
	// `../x` or absolute names a file the root does not contain - refused
	// before anything is read), reached through real directories, a
	// regular file, its bytes hashed and recorded whatever they are.
	if why := copy.Escapes(destRoot, p.DestRel); why != "" {
		return nil, fmt.Errorf("%w: %s", ErrRefused, why)
	}
	if link, err := scan.SymlinkComponent(destRoot, p.DestRel); err != nil {
		return nil, err
	} else if link != "" {
		return nil, fmt.Errorf("%w: destination path passes through a symlink (%s)", ErrRefused, filepath.Join(destRoot, link))
	}
	fi, err := os.Lstat(p.DestFull)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s does not exist - a missing destination is `vault copy`'s case, not restore's", ErrRefused, p.DestFull)
		}
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is %s, not a regular file", ErrRefused, p.DestFull, describe(fi))
	}
	if !copy.Under(destRoot, p.DestFull) {
		return nil, fmt.Errorf("%w: %s resolves outside %s", ErrRefused, p.DestFull, destRoot)
	}
	if p.CurrentSHA, p.CurrentSize, err = hashFile(ctx, p.DestFull); err != nil {
		return nil, err
	}
	p.RowAttests = p.CurrentSHA == row.SHA256

	// Other rows resolving to the same physical file would attest old bytes
	// under a path holding new ones. "Same file" is decided by identity
	// (os.SameFile on the path each row names, symlinks followed): a
	// directory symlink alias, a leaf symlink, a hard link and a case alias
	// all count. A row whose file cannot be stat'ed is compared by the
	// textual key instead, so a missing alias is still a claimant when its
	// spelling says so - the safe mistake, and the refusal names the rows.
	all, err := m.AllRows()
	if err != nil {
		return nil, err
	}
	target := physKey(destRoot, p.DestRel)
	for _, e := range all {
		if e.SourceDisk == row.SourceDisk && e.SourcePath == row.SourcePath {
			continue
		}
		rel := e.DestPath
		if rel == "" {
			rel = e.SourcePath
		}
		if other, err := os.Stat(filepath.Join(destRoot, rel)); err == nil {
			if os.SameFile(fi, other) {
				p.Claimants = append(p.Claimants, e)
			}
			continue
		}
		// The file is not there to compare by identity. Fall back to the
		// spelling key ONLY for a row of the same disk as the target: it
		// shares this destRoot, so a folded-key match is a real (missing)
		// sibling. A row of another disk is relative to a root we do not
		// record; flagging it on a folded-key match falsely refuses a valid
		// restore when that row's file lives under a different root (review
		// #30). Its aliases into THIS root are caught by the stat+SameFile
		// path above.
		if e.SourceDisk == row.SourceDisk && physKey(destRoot, rel) == target {
			p.Claimants = append(p.Claimants, e)
		}
	}
	if len(p.Claimants) > 0 {
		names := make([]string, 0, len(p.Claimants))
		for _, c := range p.Claimants {
			names = append(names, c.SourceDisk+":"+c.SourcePath+" ("+c.Status+")")
		}
		return p, fmt.Errorf("%w: %d other row(s) resolve to %s and would attest the old bytes: %s. Restore each of them with its own invocation first, or decide", ErrRefused, len(p.Claimants), p.DestFull, strings.Join(names, ", "))
	}

	// The replacement must be the file the operator says it is.
	rfi, err := os.Lstat(replacement)
	if err != nil {
		if os.IsNotExist(err) {
			return p, fmt.Errorf("%w: replacement %s does not exist", ErrRefused, replacement)
		}
		return p, err
	}
	if !rfi.Mode().IsRegular() {
		return p, fmt.Errorf("%w: replacement %s is %s, not a regular file", ErrRefused, replacement, describe(rfi))
	}
	p.ReplaceMtime = rfi.ModTime().UnixNano()
	if p.ReplaceSHA, p.ReplaceSize, err = hashFile(ctx, replacement); err != nil {
		return p, err
	}
	if p.ReplaceSHA != expectSHA {
		return p, fmt.Errorf("%w: replacement hashes to %s, --expect-sha says %s - not the file you verified", ErrRefused, p.ReplaceSHA, expectSHA)
	}
	p.AlreadyThere = p.CurrentSHA == expectSHA
	return p, nil
}

// Apply writes the replacement over the row's file through the ordinary
// writer (staging O_EXCL, fsync, chtimes, rename over the regular file the
// plan checked) and sets the row back to copied with the new bytes' facts.
// The landed hash is compared to the expectation once more: after the
// rename nothing can be un-written, so this is the last line, not the first.
func Apply(ctx context.Context, m *manifest.Manifest, p *Plan, destRoot string) (manifest.Entry, error) {
	task := scan.FileTask{
		RelPath: filepath.Base(p.Replacement),
		DstRel:  p.DestRel,
		Size:    p.ReplaceSize,
		MtimeNs: p.ReplaceMtime,
		Replace: true,
	}
	landed, err := copy.File(ctx, filepath.Dir(p.Replacement), destRoot, task, p.Row.SourceDisk)
	if err != nil {
		return manifest.Entry{}, err
	}
	if landed.SHA256 != p.ExpectSHA || landed.Size != p.ReplaceSize {
		return manifest.Entry{}, fmt.Errorf("landed bytes hash to %s (%d B), expected %s (%d B) - the file at %s is now in doubt; verify it by hand",
			landed.SHA256, landed.Size, p.ExpectSHA, p.ReplaceSize, p.DestFull)
	}
	row := p.Row
	row.SHA256, row.Size, row.MtimeNs = landed.SHA256, landed.Size, landed.MtimeNs
	row.CopiedAt, row.VerifiedAt, row.Status = time.Now().UnixNano(), 0, "copied"
	if err := m.Upsert(row); err != nil {
		return manifest.Entry{}, fmt.Errorf("file replaced but the row could not be updated: %w", err)
	}
	return row, nil
}

func physKey(root, rel string) string {
	return strings.ToLower(filepath.Clean(filepath.Join(root, rel)))
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

func hashFile(ctx context.Context, path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var n int64
	for {
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
		k, err := f.Read(buf)
		if k > 0 {
			h.Write(buf[:k])
			n += int64(k)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
