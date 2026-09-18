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
	// A first pass over the rows records the stem of every video file, so an
	// empty .SRT can be judged against whether its media is actually archived.
	videoStems := map[string]bool{}
	for _, e := range rows {
		rel := relOf(e)
		if videoExts[strings.ToLower(filepath.Ext(rel))] {
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

		// The file must be inside the root, reached through real directories —
		// a row path can be spelled `../x` or absolute (review #24), or pass
		// through a symlinked directory. Same guards as restore/repair-dest.
		if why := copy.Escapes(dstRoot, rel); why != "" {
			record(ErrorV, "outside the root: "+why)
			continue
		}
		if link, err := scan.SymlinkComponent(dstRoot, rel); err != nil {
			record(ErrorV, fmt.Sprintf("cannot resolve path: %v", err))
			continue
		} else if link != "" {
			record(ErrorV, "path passes through a symlinked directory ("+link+")")
			continue
		}
		full := filepath.Join(dstRoot, rel)
		li, err := os.Lstat(full)
		if err != nil {
			record(ErrorV, fmt.Sprintf("cannot stat: %v", err))
			continue
		}
		if !li.Mode().IsRegular() {
			record(ErrorV, "not a regular file")
			continue
		}
		if !copy.Under(dstRoot, full) {
			record(ErrorV, "resolves outside the root")
			continue
		}
		tail, size, err := readTail(full, li)
		if err != nil {
			record(ErrorV, fmt.Sprintf("cannot read tail: %v", err))
			continue
		}
		v, reason := Classify(rel, size, tail, videoStems[stem(rel)])
		record(v, reason)
	}
	return res, nil
}

func relOf(e manifest.Entry) string {
	if e.DestPath != "" {
		return e.DestPath
	}
	return e.SourcePath // verify's rule: an empty dest_path falls back to source_path
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
// Lstat saw (no swap between stat and open), sizes the read from the fd, and
// treats a short read as an error — a file that shrank mid-read is not audited
// on a partial tail. Read-only.
func readTail(path string, lstatInfo os.FileInfo) ([]byte, int64, error) {
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
	n := int64(TailBytes)
	if n > size {
		n = size
	}
	if n == 0 {
		return nil, size, nil
	}
	buf := make([]byte, n)
	k, err := f.ReadAt(buf, size-n)
	if err != nil && err != io.EOF {
		return nil, size, err
	}
	if int64(k) != n {
		return nil, size, fmt.Errorf("short tail read: got %d of %d bytes", k, n)
	}
	return buf, size, nil
}
