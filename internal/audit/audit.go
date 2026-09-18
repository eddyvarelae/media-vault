// Package audit is a report-only content-plausibility pass over a disk's rows
// (B38). verify and certify cannot detect a torn write that was hashed after
// the fact — a hash of a truncated file is still a hash — so audit reads the
// tail of each archived file and judges it against a per-type expectation,
// separating a genuine torn write from the camera-native padding and
// pre-allocation that is normal for the type. It changes nothing: it opens the
// manifest read-only and only reads file tails.
//
// The heuristics are seeded from the Tester's archive-wide tail scan (#25),
// which found exactly one torn file among 145 tail hits — the other 144 were
// camera-native and byte-identical to their sources.
package audit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/eddyvarelae/media-vault/internal/copy"
	"github.com/eddyvarelae/media-vault/internal/manifest"
	"github.com/eddyvarelae/media-vault/internal/scan"
)

// TailBytes is how much of each file's end audit reads — the same 64 KiB window
// the Tester's scan used. A valid JPEG's EOI, and any camera zero-padding, sit
// well within it.
const TailBytes = 64 * 1024

// Verdict is audit's judgement of one file.
type Verdict string

const (
	Plausible Verdict = "PLAUSIBLE" // consistent with a whole file of this type
	Suspect   Verdict = "SUSPECT"   // content-implausible — a likely torn write
	Review    Verdict = "REVIEW"    // off the known signature, not proven torn
	Skipped   Verdict = "SKIPPED"   // type audit does not check
	ErrorV    Verdict = "ERROR"     // the file could not be read for auditing
)

// videoExts are the extensions whose presence makes a file a plausible twin of
// a sidecar (an empty .SRT telemetry track belongs to one of these).
var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".avi": true, ".mts": true, ".m2ts": true,
	".m4v": true, ".lrv": true, ".insv": true, ".mkv": true,
}

// Finding is one row's verdict and why.
type Finding struct {
	Entry   manifest.Entry
	Rel     string // the path checked, relative to the root
	Verdict Verdict
	Reason  string
}

// Result tallies a run. Findings holds the SUSPECT / REVIEW / ERROR rows — the
// ones worth listing; PLAUSIBLE and SKIPPED are counted, not listed. SkippedExt
// breaks the SKIPPED count down by extension so the report can name what it did
// not check.
type Result struct {
	Rows       int
	Plausible  int
	Suspect    int
	Review     int
	Skipped    int
	Errors     int
	Findings   []Finding
	SkippedExt map[string]int
}

// Classify judges a file from its name, size, tail (the last TailBytes, or the
// whole file when shorter) and whether a media twin exists (only consulted for
// an empty .SRT). It is pure — no I/O — so the type rules are unit-testable on
// bytes alone.
func Classify(rel string, size int64, tail []byte, hasTwin bool) (Verdict, string) {
	base := filepath.Base(rel)
	ext := strings.ToLower(filepath.Ext(rel))
	switch {
	case ext == ".jpg" || ext == ".jpeg":
		if size == 0 {
			return Suspect, "zero-length JPEG"
		}
		// A whole JPEG ends in the EOI marker FF D9, optionally followed by only
		// zero padding to EOF (cameras/DJI pad, DJI to 4 KiB multiples). The
		// tail ends at EOF, so the bytes after the LAST EOI in the tail are the
		// bytes from the EOI to EOF. The torn file had no EOI at all — just
		// zeros; a file with an EOI but non-zero data after it is not a clean end.
		i := bytes.LastIndex(tail, []byte{0xFF, 0xD9})
		if i < 0 {
			return Suspect, "no JPEG EOI (FF D9) in the last 64 KiB — possible torn write"
		}
		for _, b := range tail[i+2:] {
			if b != 0x00 {
				return Suspect, "JPEG EOI is followed by non-zero data before EOF — not a clean end"
			}
		}
		return Plausible, "JPEG EOI (FF D9) then only zero padding to EOF"
	case ext == ".arw":
		// Sony pre-allocates raw at fixed sizes — every whole ARW the scan saw
		// was an exact number of MiB (27/28/29/30). A short one would not be.
		if size > 0 && size%(1<<20) == 0 {
			return Plausible, "Sony raw, size is an exact multiple of 1 MiB"
		}
		return Review, "Sony raw size is not a whole number of MiB — off the pre-allocation signature"
	case ext == ".rsv":
		return Plausible, "Sony reserve file (pre-allocated, uniform padding by design)"
	case strings.EqualFold(base, "DATABASE.BIN"):
		return Plausible, "Sony camera database (zero-padded by design)"
	case ext == ".srt":
		// An empty telemetry/subtitle track is a DJI aborted-recording twin —
		// but only if the media it belongs to is actually here. A lone empty
		// .SRT is worth a look, not a pass.
		if size == 0 {
			if hasTwin {
				return Plausible, "empty SRT with a media twin (aborted/short recording)"
			}
			return Review, "empty SRT with no media twin on the disk"
		}
		return Skipped, "SRT text not audited"
	default:
		return Skipped, "type not audited"
	}
}

// Run audits every row of disk whose file resolves under dstRoot, calling
// onFinding for each SUSPECT / REVIEW / ERROR. It is report-only: read-only
// manifest, tails only, nothing written.
func Run(ctx context.Context, m *manifest.Manifest, disk, dstRoot string, onFinding func(Finding)) (*Result, error) {
	rows, err := m.ListByDisk(disk)
	if err != nil {
		return nil, err
	}
	// A first pass records the stem of every video file that ACTUALLY resolves
	// to a regular file under the root — so an empty .SRT is judged a twin only
	// against media that is really archived and readable, not against a mere
	// manifest row for a missing/outside-root/symlinked file (review #63).
	videoStems := map[string]bool{}
	for _, e := range rows {
		rel := relOf(e)
		if !videoExts[strings.ToLower(filepath.Ext(rel))] {
			continue
		}
		if _, reason, _ := resolve(dstRoot, rel); reason == "" {
			videoStems[stem(rel)] = true
		}
	}

	res := &Result{SkippedExt: map[string]int{}}
	for _, e := range rows {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Rows++
		rel := relOf(e)
		record := func(v Verdict, reason string) {
			switch v {
			case Plausible:
				res.Plausible++
			case Suspect:
				res.Suspect++
			case Review:
				res.Review++
			case Skipped:
				res.Skipped++
				ext := strings.ToLower(filepath.Ext(rel))
				if ext == "" {
					ext = "(no ext)"
				}
				res.SkippedExt[ext]++
			case ErrorV:
				res.Errors++
			}
			if v != Plausible && v != Skipped {
				f := Finding{Entry: e, Rel: rel, Verdict: v, Reason: reason}
				res.Findings = append(res.Findings, f)
				onFinding(f)
			}
		}

		full, reason, li := resolve(dstRoot, rel)
		if reason != "" {
			record(ErrorV, reason)
			continue
		}
		tail, size, err := readTail(full, li)
		if err != nil {
			record(ErrorV, fmt.Sprintf("cannot read tail: %v", err))
			continue
		}
		v, why := Classify(rel, size, tail, videoStems[stem(rel)])
		record(v, why)
	}
	return res, nil
}

func relOf(e manifest.Entry) string {
	if e.DestPath != "" {
		return e.DestPath
	}
	return e.SourcePath // verify's rule: an empty dest_path falls back to source_path
}

// resolve validates rel against dstRoot the way every reader here must: inside
// the root (a row path can be `../x` or absolute — review #24), reached through
// real directories (no symlinked component), an existing regular file, and
// physically under the resolved root. It returns the full path and the file's
// Lstat info on success (empty reason), or a reason for the ERROR/skip.
//
// Residual (as in certify): the O_NOFOLLOW open below binds only the leaf, and
// this component walk is a check-then-use — a PARENT directory swapped to a
// symlink between the walk and the open is not caught. Closing that needs
// os.Root (Go 1.25); backlog.
func resolve(dstRoot, rel string) (full, reason string, li os.FileInfo) {
	if why := copy.Escapes(dstRoot, rel); why != "" {
		return "", "outside the root: " + why, nil
	}
	if link, err := scan.SymlinkComponent(dstRoot, rel); err != nil {
		return "", fmt.Sprintf("cannot resolve path: %v", err), nil
	} else if link != "" {
		return "", "path passes through a symlinked directory (" + link + ")", nil
	}
	full = filepath.Join(dstRoot, rel)
	fi, err := os.Lstat(full)
	if err != nil {
		return "", fmt.Sprintf("cannot stat: %v", err), nil
	}
	if !fi.Mode().IsRegular() {
		return "", "not a regular file", nil
	}
	if !copy.Under(dstRoot, full) {
		return "", "resolves outside the root", nil
	}
	return full, "", fi
}

// stem is the lowercased basename without its extension, so a .SRT and its
// media twin (DJI_..._D.SRT / DJI_..._D.MP4) share one key.
func stem(rel string) string {
	b := filepath.Base(rel)
	return strings.ToLower(strings.TrimSuffix(b, filepath.Ext(b)))
}

// readTail returns the last TailBytes of the file (or the whole file when it is
// shorter) and the size read from the OPEN fd. It opens O_NOFOLLOW (the leaf
// must not be a symlink), confirms via SameFile that the opened file is the one
// Lstat saw (no swap between stat and open), and sizes the read from the fd.
// Read-only.
func readTail(path string, lstatInfo os.FileInfo) ([]byte, int64, error) {
	if hookBeforeOpen != nil {
		hookBeforeOpen(path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !os.SameFile(lstatInfo, fi) {
		return nil, 0, fmt.Errorf("file changed between stat and open")
	}
	size := fi.Size()
	if hookAfterStat != nil {
		hookAfterStat(path)
	}
	tail, err := tailFrom(f, size)
	return tail, size, err
}

// hookBeforeOpen and hookAfterStat are test-only seams (nil in production) that
// reproduce, THROUGH Run, the two races readTail defends against: hookBeforeOpen
// fires after resolve's Lstat and before the O_NOFOLLOW open, so a test can
// substitute the leaf (a symlink → O_NOFOLLOW refuses it; a swapped inode →
// SameFile refuses it); hookAfterStat fires after the size is taken from the fd
// and before the tail ReadAt, so a test can truncate the file and prove a short
// read is an ERROR (review #64). Both leave production untouched.
var (
	hookBeforeOpen func(path string)
	hookAfterStat  func(path string)
)

// tailFrom reads the last min(TailBytes, size) bytes at the fixed offset and
// treats a short read as an error — a file that shrank mid-read is not audited
// on a partial tail (review #62/#63). Split out so the short-read path is
// testable with a ReaderAt that returns fewer bytes than asked.
func tailFrom(r io.ReaderAt, size int64) ([]byte, error) {
	n := int64(TailBytes)
	if n > size {
		n = size
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]byte, n)
	k, err := r.ReadAt(buf, size-n)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if int64(k) != n {
		return nil, fmt.Errorf("short tail read: got %d of %d bytes", k, n)
	}
	return buf, nil
}
