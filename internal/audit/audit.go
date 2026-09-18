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

// Finding is one row's verdict and why.
type Finding struct {
	Entry   manifest.Entry
	Rel     string // the path checked, relative to the root
	Verdict Verdict
	Reason  string
}

// Result tallies a run. Findings holds the SUSPECT / REVIEW / ERROR rows — the
// ones worth listing; PLAUSIBLE and SKIPPED are counted, not listed.
type Result struct {
	Rows      int
	Plausible int
	Suspect   int
	Review    int
	Skipped   int
	Errors    int
	Findings  []Finding
}

// Classify judges a file from its name, size and tail (the last TailBytes, or
// the whole file when shorter). It is pure — no I/O — so the type rules are
// unit-testable on bytes alone.
func Classify(rel string, size int64, tail []byte) (Verdict, string) {
	base := filepath.Base(rel)
	ext := strings.ToLower(filepath.Ext(rel))
	switch {
	case ext == ".jpg" || ext == ".jpeg":
		if size == 0 {
			return Suspect, "zero-length JPEG"
		}
		// A whole JPEG ends in the EOI marker FF D9, optionally followed by
		// camera/DJI zero padding (DJI pads to 4 KiB multiples). The one torn
		// file had no EOI anywhere in its last 64 KiB — just zeros.
		if bytes.Contains(tail, []byte{0xFF, 0xD9}) {
			return Plausible, "JPEG EOI (FF D9) present in the tail"
		}
		return Suspect, "no JPEG EOI (FF D9) in the last 64 KiB — possible torn write"
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
		// An empty subtitle/telemetry track is a DJI aborted-recording twin,
		// not a torn write (a torn write is full-size with a zero tail).
		if size == 0 {
			return Plausible, "empty SRT (aborted/short recording twin)"
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
	res := &Result{}
	for _, e := range rows {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Rows++
		rel := e.DestPath
		if rel == "" {
			rel = e.SourcePath // verify's rule: an empty dest_path falls back to source_path
		}
		record := func(v Verdict, reason string) {
			f := Finding{Entry: e, Rel: rel, Verdict: v, Reason: reason}
			switch v {
			case Plausible:
				res.Plausible++
			case Suspect:
				res.Suspect++
			case Review:
				res.Review++
			case Skipped:
				res.Skipped++
			case ErrorV:
				res.Errors++
			}
			if v != Plausible && v != Skipped {
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
		fi, err := os.Lstat(full)
		if err != nil {
			record(ErrorV, fmt.Sprintf("cannot stat: %v", err))
			continue
		}
		if !fi.Mode().IsRegular() {
			record(ErrorV, "not a regular file")
			continue
		}
		if !copy.Under(dstRoot, full) {
			record(ErrorV, "resolves outside the root")
			continue
		}
		tail, err := readTail(full, fi.Size())
		if err != nil {
			record(ErrorV, fmt.Sprintf("cannot read tail: %v", err))
			continue
		}
		v, reason := Classify(rel, fi.Size(), tail)
		record(v, reason)
	}
	return res, nil
}

// readTail returns the last TailBytes of the file (or the whole file when it is
// shorter). Read-only.
func readTail(path string, size int64) ([]byte, error) {
	n := int64(TailBytes)
	if n > size {
		n = size
	}
	if n == 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, size-n); err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}
