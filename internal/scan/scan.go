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
		if err := checkSubdir(s[i+1:]); err != nil {
			return nil, fmt.Errorf("invalid rule %q: %v", s, err)
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
	// Replace is set for a recopy: the row is not verified and its own
	// destination file is expected to exist and be renamed over. For a new
	// file the writer refuses anything at the final path.
	Replace bool
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

	// VerifiedChanged holds files whose row is `verified` but whose source
	// now carries different content. They are never copied: the archived
	// bytes are what a certificate attested, and the manifest keys on
	// (source_disk, source_path), so there is no second row for the new
	// bytes to live in. Retouched counts the harmless cousin - same content,
	// new mtime - which is skipped like an unchanged file.
	VerifiedChanged      []FileTask
	BytesVerifiedChanged int64
	Retouched            int

	// DstOwned holds files whose destination - the final path or the
	// .vault-partial staging path copy.File creates first - is the
	// dest_path of a verified row, of any disk. That row may not be this
	// file's own (a deduped row points at another disk's file; a stray name
	// can match an archived file), so the check is by destination, not by
	// source row, and by physical location, not spelling. Never written
	// under any policy.
	DstOwned []OwnedTask

	// DstThroughLink holds files whose destination path passes through a
	// symlinked directory component under the root. Two spellings then
	// name one physical file, which neither the ownership key nor a leaf
	// Lstat can see; the writer refuses such a path too. Never written.
	DstThroughLink []LinkedTask
}

// LinkedTask is a file refused because a directory on its destination path
// is a symlink.
type LinkedTask struct {
	Task FileTask
	Link string // the symlink component, relative to the root
}

// OwnedTask is a file refused because a verified row owns its destination.
type OwnedTask struct {
	Task  FileTask
	Path  string         // the path that is owned: DstRel or DstRel + ".vault-partial"
	Owner manifest.Entry // the verified row that references it
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
	owners, err := VerifiedOwners(m, dstRoot)
	if err != nil {
		return nil, err
	}
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
	err = filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
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
			if entry.Status == "verified" {
				// A verified destination is never overwritten (B23(b): a
				// second card carried different photos under the same DCIM
				// names, and recopy replaced 2,668 certified files in place).
				// Same size is not proof of change - a touched file hashes
				// equal and is simply skipped - but any difference in bytes
				// is a collision, and one the collision policy cannot rename
				// its way out of: the row for this (disk, path) is taken.
				if entry.Size == task.Size {
					sum, hErr := hashFile(ctx, path)
					if hErr != nil {
						return hErr
					}
					if sum == entry.SHA256 {
						p.Retouched++
						return nil
					}
				}
				p.VerifiedChanged = append(p.VerifiedChanged, task)
				p.BytesVerifiedChanged += task.Size
				return nil
			}
			task.Replace = true
			if owned := owners.claims(dstRoot, task); owned != nil {
				p.DstOwned = append(p.DstOwned, *owned)
				return nil
			}
			if link, err := SymlinkComponent(dstRoot, task.DstRel); err != nil {
				return err
			} else if link != "" {
				p.DstThroughLink = append(p.DstThroughLink, LinkedTask{Task: task, Link: link})
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

		// The destination is free on disk; make sure the manifest agrees.
		// A verified row whose file is missing still owns the path (verify
		// reports it missing; writing other bytes there would make it a
		// mismatch under a certified hash), and the staging path may be an
		// archived file that merely ends in .vault-partial.
		if owned := owners.claims(dstRoot, task); owned != nil {
			p.DstOwned = append(p.DstOwned, *owned)
			return nil
		}
		if link, err := SymlinkComponent(dstRoot, task.DstRel); err != nil {
			return err
		} else if link != "" {
			p.DstThroughLink = append(p.DstThroughLink, LinkedTask{Task: task, Link: link})
			return nil
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

// OwnerIndex maps the physical location of every verified destination to
// its row. Physical means what the writer will touch: joined to this run's
// root and cleaned, so a `..` in a routing rule collapses to where it
// really lands, and case-folded, so `X.mov` and `x.mov` are one key. The
// fold is unconditional rather than probed per filesystem: on a
// case-sensitive root it can only refuse a write that differs from a
// certified file by case alone, which is the safe mistake. (Unicode
// normalization aliases are not folded; camera names are ASCII.) A row
// with an empty dest_path locates its file by source_path (verify's rule)
// and is indexed there. copy and move both consult it.
type OwnerIndex map[string]manifest.Entry

// VerifiedOwners builds the index for dstRoot from every verified row.
func VerifiedOwners(m *manifest.Manifest, dstRoot string) (OwnerIndex, error) {
	rows, err := m.VerifiedRows()
	if err != nil {
		return nil, err
	}
	idx := make(OwnerIndex, len(rows))
	for _, e := range rows {
		rel := e.DestPath
		if rel == "" {
			rel = e.SourcePath
		}
		if _, dup := idx[physKey(dstRoot, rel)]; !dup {
			idx[physKey(dstRoot, rel)] = e
		}
	}
	return idx, nil
}

// physKey is the identity two spellings share when they name one file.
func physKey(root, rel string) string {
	return strings.ToLower(filepath.Clean(filepath.Join(root, rel)))
}

// Owner reports the verified row, if any, whose file is at rel under
// dstRoot or at rel's .vault-partial staging name - the two paths a writer
// touches - and which of the two it was.
func (idx OwnerIndex) Owner(dstRoot, rel string) (owner manifest.Entry, path string, ok bool) {
	for _, p := range []string{rel + ".vault-partial", rel} {
		if o, found := idx[physKey(dstRoot, p)]; found {
			return o, p, true
		}
	}
	return manifest.Entry{}, "", false
}

// claims is Owner for a task, as a refusal record.
func (idx OwnerIndex) claims(dstRoot string, task FileTask) *OwnedTask {
	if owner, path, ok := idx.Owner(dstRoot, task.DstRel); ok {
		return &OwnedTask{Task: task, Path: path, Owner: owner}
	}
	return nil
}

// SymlinkComponent walks the directories of rel under root, one Lstat per
// component, and returns the first one that is a symlink (relative to
// root), or "" when every existing component is a real directory. A
// component that does not exist yet ends the walk: nothing below it can be
// a link, and the writer's MkdirAll will create real directories. Called by
// Build before admitting a task and by copy.File before writing, because a
// symlinked directory makes two spellings one file and no per-path check
// can tell.
func SymlinkComponent(root, rel string) (string, error) {
	dir := filepath.Dir(filepath.Clean(rel))
	if dir == "." {
		return "", nil
	}
	cur := root
	for i, c := range strings.Split(dir, string(filepath.Separator)) {
		cur = filepath.Join(cur, c)
		fi, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return filepath.Join(strings.Split(dir, string(filepath.Separator))[:i+1]...), nil
		}
	}
	return "", nil
}

// SymlinkComponentRoot is SymlinkComponent re-anchored to an os.Root handle
// (B43): it walks rel's directory components via root.Lstat, relative to the
// root fd, and returns the first that is a symlink (relative, forward slashes)
// or "" when every existing component is a real directory. A component that
// does not exist yet ends the walk. Anchoring to the Root fd means this walk
// and the caller's subsequent os.Root operation resolve against the same pinned
// directory even if a parent is renamed under them, and os.Root refuses any
// component that escapes the root outright. This still refuses an in-root
// symlinked directory that exists at walk time (os.Root would otherwise follow
// it, aliasing two spellings to one file); the narrower residual it cannot
// close is a parent swapped to an in-root symlink in the window after the walk
// (os.Root follows in-root links) — see the callers' docs.
func SymlinkComponentRoot(root *os.Root, rel string) (string, error) {
	dir := filepath.Dir(filepath.Clean(rel))
	if dir == "." {
		if testAfterWalk != nil {
			testAfterWalk()
		}
		return "", nil
	}
	parts := strings.Split(dir, string(filepath.Separator))
	cur := ""
	for i, c := range parts {
		if cur == "" {
			cur = c
		} else {
			cur += "/" + c
		}
		fi, err := root.Lstat(cur)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return strings.Join(parts[:i+1], "/"), nil
		}
	}
	if testAfterWalk != nil {
		testAfterWalk()
	}
	return "", nil
}

// testAfterWalk is a test-only seam (nil in production): SymlinkComponentRoot
// calls it after a clean walk and before returning "", so a test can mutate the
// tree in the window between the check and the caller's os.Root operation —
// swapping a parent for a symlink — to exercise os.Root as the atomic escape
// backstop deterministically. Set and cleared by the binding sites' tests
// (copy/audit/certify/restore) via SetTestAfterWalk; nil restores production.
var testAfterWalk func()

// SetTestAfterWalk installs (or, with nil, clears) the after-walk test seam.
// It exists only for the B43 parent-swap regressions in the binding packages,
// which cannot reach an unexported var across package boundaries.
func SetTestAfterWalk(f func()) { testAfterWalk = f }

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
	// The video tagger writes its per-clip JSON under a `reports/` directory
	// beside the footage (GoPro/Videos/reports/, iPhone/Videos/reports/).
	// Those files are derived, have no manifest row, and are not footage to
	// archive: a scan that sees them plans copies of tagger output. Decided
	// 2026-09-17 (B20): skipped at any depth, by name.
	case "reports":
		return true
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

// checkSubdir keeps a routing rule inside the destination root. A subdir
// with a ".." component or an absolute path routes writes outside the tree
// the run was pointed at (B34) - the ownership guard sees through it, but a
// rule that escapes its root is wrong on its own.
func checkSubdir(sub string) error {
	if filepath.IsAbs(sub) {
		return fmt.Errorf("subdir must be relative to the destination root")
	}
	for _, c := range strings.Split(filepath.ToSlash(sub), "/") {
		if c == ".." {
			return fmt.Errorf("subdir must not contain \"..\"")
		}
	}
	return nil
}
