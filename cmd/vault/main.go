package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/eddyvarelae/media-vault/internal/certify"
	"github.com/eddyvarelae/media-vault/internal/copy"
	"github.com/eddyvarelae/media-vault/internal/dedup"
	"github.com/eddyvarelae/media-vault/internal/importer"
	"github.com/eddyvarelae/media-vault/internal/inventory"
	"github.com/eddyvarelae/media-vault/internal/manifest"
	mvpkg "github.com/eddyvarelae/media-vault/internal/move"
	"github.com/eddyvarelae/media-vault/internal/repair"
	"github.com/eddyvarelae/media-vault/internal/scan"
	"github.com/eddyvarelae/media-vault/internal/verify"
)

const usage = `vault — auditable media archive

Usage:
  vault scan       <source-disk-name> <source-dir> <dest-dir>
                   [--prefix SUB/] [--rule EXT=SUBDIR ...]
                   [--on-collision skip|rename-mtime-year]
  vault copy       <source-disk-name> <source-dir> <dest-dir>
                   [--prefix SUB/] [--rule EXT=SUBDIR ...]
                   [--on-collision skip|rename-mtime-year] [--dry-run]
                   [--dedupe-content]   (scan and copy: skip files whose CONTENT
                                         is already archived under any disk)
  vault verify     <source-disk-name> <dest-dir> [--only-unverified]
  vault certify    <source-disk-name> [out.json]
  vault repair-dest <source-disk-name> <dest-dir> [--dry-run]
                   (rows whose dest_path is missing: point them at the same
                    basename one directory down, only on a size+sha256 match)
  vault inventory  <source-disk-name> <dir>
  vault dedup      [--min-size <bytes>]
  vault unique     <source-disk-name>
  vault tag        <source-disk-name> <path-pattern> <tag>
  vault untag      <source-disk-name> <path-pattern> <tag>
  vault tagged     <tag>
  vault tags       <source-disk-name> <path>
  vault symlinks   <tag> <source-disk>=<host-path>... <output-dir>
  vault hardlinks  <tag> <source-disk>=<host-path>... <output-dir>
  vault import-tags <reports-dir> <source-disk-name>
  vault move       <src-disk> <dst-disk> <src-host-root> <dst-host-root>
                   [--prefix SUB/] [--rule EXT=SUBDIR ...]
                   [--on-collision skip|rename-mtime-year] [--dry-run]

Manifest and signing key are stored under $VAULT_CONFIG
(default: ./vault-config/).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	configDir := os.Getenv("VAULT_CONFIG")
	if configDir == "" {
		configDir = "./vault-config"
	}
	dbPath := filepath.Join(configDir, "manifest.db")
	var m *manifest.Manifest
	var err error
	if hasDryRun(args) {
		// A dry run writes nothing - not an archive file, not a row, and
		// (B31) not the config dir or the manifest file either: the
		// manifest is opened read-only, or planned against an empty
		// in-memory one when none exists yet.
		if _, statErr := os.Stat(dbPath); statErr == nil {
			m, err = manifest.OpenReadOnly(dbPath)
		} else if os.IsNotExist(statErr) {
			fmt.Fprintf(os.Stderr, "(dry-run: no manifest at %s yet; planning against an empty one, creating nothing)\n", dbPath)
			m, err = manifest.OpenEmpty()
		} else {
			err = statErr
		}
	} else {
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			die("create config dir: %v", err)
		}
		m, err = manifest.Open(dbPath)
	}
	if err != nil {
		die("open manifest: %v", err)
	}
	defer m.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "scan":
		runScan(ctx, m, args)
	case "copy":
		// runCopy returns its status rather than exiting so the F3 exit
		// decision is testable in-process. Same exit codes as before.
		if code := runCopy(ctx, m, args); code != 0 {
			os.Exit(code)
		}
	case "verify":
		runVerify(ctx, m, args)
	case "certify":
		runCertify(m, configDir, args)
	case "repair-dest":
		if code := runRepairDest(ctx, m, args); code != 0 {
			os.Exit(code)
		}
	case "inventory":
		runInventory(ctx, m, args)
	case "dedup":
		runDedup(m, args)
	case "unique":
		runUnique(m, args)
	case "tag":
		runTag(m, args)
	case "untag":
		runUntag(m, args)
	case "tagged":
		runTagged(m, args)
	case "tags":
		runTags(m, args)
	case "symlinks":
		runLinks(m, args, os.Symlink, "symlink")
	case "hardlinks":
		runLinks(m, args, os.Link, "hardlink")
	case "move":
		runMove(ctx, m, args)
	case "import-tags":
		runImportTags(ctx, m, args)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func parseScanFlags(args []string) (positional []string, prefix string, rules []scan.Rule, collision scan.CollisionStrategy, dryRun bool, dedupeContent bool) {
	collisionRaw := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--dedupe-content":
			dedupeContent = true
		case "--prefix":
			if i+1 >= len(args) {
				die("--prefix needs a value")
			}
			prefix = args[i+1]
			i++
		case "--rule":
			if i+1 >= len(args) {
				die("--rule needs a value")
			}
			r, err := scan.ParseRules([]string{args[i+1]})
			if err != nil {
				die("rule: %v", err)
			}
			rules = append(rules, r...)
			i++
		case "--on-collision":
			if i+1 >= len(args) {
				die("--on-collision needs a value")
			}
			collisionRaw = args[i+1]
			i++
		default:
			positional = append(positional, args[i])
		}
	}
	c, err := scan.ParseCollision(collisionRaw)
	if err != nil {
		die("%v", err)
	}
	collision = c
	return
}

func runScan(ctx context.Context, m *manifest.Manifest, args []string) {
	pos, prefix, rules, collision, _, dedupeContent := parseScanFlags(args)
	if len(pos) != 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, src, dst := pos[0], pos[1], pos[2]

	plan, err := scan.BuildWithOptions(ctx, m, disk, src, dst, prefix, rules, collision, dedupeContent)
	if err != nil {
		die("scan: %v", err)
	}

	fmt.Printf("Source disk:  %s\n", disk)
	fmt.Printf("Source dir:   %s\n", src)
	fmt.Printf("Dest dir:     %s\n", dst)
	if prefix != "" {
		fmt.Printf("Prefix:       %s\n", prefix)
	}
	if len(rules) > 0 {
		fmt.Printf("Rules:        %d\n", len(rules))
	}
	fmt.Println()
	fmt.Printf("Files to copy:    %d  (%s)\n", len(plan.ToCopy), human(plan.BytesToCopy))
	fmt.Printf("Files to skip:    %d  (in manifest, unchanged)\n", plan.SkipCount)
	reportDedupe(plan, dedupeContent)
	fmt.Printf("Files to recopy:  %d  (%s, source size or mtime changed, row not verified)\n",
		len(plan.ToRecopy), human(plan.BytesToRecopy))
	fmt.Printf("Dst collisions:   %d  (dst path already exists, would overwrite)\n", len(plan.DstCollisions))
	reportVerifiedChanged(plan)
}

// reportVerifiedChanged prints the B23(b) bucket for both scan and copy. The
// count line always prints so a zero is visible; the explanation only when
// it is non-zero, because the way out is not a flag on this command.
func reportVerifiedChanged(plan *scan.Plan) {
	fmt.Printf("Verified, changed: %d  (%s, source differs from the verified archive copy; never overwritten)\n",
		len(plan.VerifiedChanged), human(plan.BytesVerifiedChanged))
	if plan.Retouched > 0 {
		fmt.Printf("Retouched:        %d  (verified, mtime changed, content identical; skipped)\n", plan.Retouched)
	}
	if len(plan.VerifiedChanged) > 0 {
		fmt.Println("  note: these are different files under a source path this disk already")
		fmt.Println("        archived and verified. The archived copy is kept. If they come from")
		fmt.Println("        another card, copy that card under its own <disk> name with")
		fmt.Println("        --on-collision rename-mtime-year so they land beside the originals.")
	}
	fmt.Printf("Dst owned:        %d  (destination or its .vault-partial path belongs to a verified row; never written)\n", len(plan.DstOwned))
	fmt.Printf("Dst via symlink:  %d  (a directory on the destination path is a symlink; never written)\n", len(plan.DstThroughLink))
	if len(plan.DstOwned) > 0 {
		fmt.Println("  note: a verified row of some disk records that path as its archived copy.")
		fmt.Println("        Nothing is written there by any policy. Copy under a --rule or --prefix")
		fmt.Println("        that routes elsewhere, or resolve the owning row first.")
	}
}

// reportDedupe prints the dedupe summary for both scan and copy. Always prints
// when the flag is on, including the zero case: "found no duplicates" and "had
// nothing to compare against" are different facts, and the second is the normal
// state until `vault verify` has run.
func reportDedupe(plan *scan.Plan, on bool) {
	if !on {
		return
	}
	fmt.Printf("Already archived: %d  (%s, same content under another disk/name; %d hashed, %d verified rows eligible)\n",
		len(plan.Deduped), human(plan.BytesSkipContent), plan.HashedFiles, plan.DedupeEligible)
	if plan.DedupeEligible == 0 {
		// Scoped to cross-disk: intra-run dedupe compares files within this
		// source and fires regardless of what is verified in the archive.
		fmt.Println("  note: no verified rows to match against, so no CROSS-DISK dedupe is")
		fmt.Println("        possible — run `vault verify` on the already-archived disks first.")
	}
}

// runCopy returns the process exit status: 0 when every planned file is
// archived (or nothing needed doing, or --dry-run), 1 when the run finished
// INCOMPLETE. Usage and fatal errors still exit directly via die/usage.
func runCopy(ctx context.Context, m *manifest.Manifest, args []string) int {
	pos, prefix, rules, collision, dryRun, dedupeContent := parseScanFlags(args)
	if len(pos) != 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, src, dst := pos[0], pos[1], pos[2]

	plan, err := scan.BuildWithOptions(ctx, m, disk, src, dst, prefix, rules, collision, dedupeContent)
	if err != nil {
		die("scan: %v", err)
	}
	reportDedupe(plan, dedupeContent)
	// Always, and before the no-op return: a zero is a statement too, and
	// a run of nothing but retouched files must still say what it saw.
	reportVerifiedChanged(plan)

	todo := append(plan.ToCopy, plan.ToRecopy...)
	if len(todo) == 0 && len(plan.DstCollisions) == 0 && len(plan.Deduped) == 0 && len(plan.VerifiedChanged) == 0 && len(plan.DstOwned) == 0 && len(plan.DstThroughLink) == 0 {
		fmt.Println("Nothing to copy. Manifest is up to date.")
		return 0
	}

	totalBytes := plan.BytesToCopy + plan.BytesToRecopy
	fmt.Printf("Copying %d files (%s) from %s → %s\n", len(todo), human(totalBytes), src, dst)
	if len(plan.DstCollisions) > 0 {
		fmt.Printf("(%d files SKIPPED — dst path already exists; use --on-collision rename-mtime-year to disambiguate)\n", len(plan.DstCollisions))
	}
	if dryRun {
		for _, f := range todo {
			fmt.Printf("  %s → %s\n", f.RelPath, f.DstRel)
		}
		if len(plan.DstCollisions) > 0 {
			fmt.Println("\nCollisions (skipped):")
			for _, f := range plan.DstCollisions {
				fmt.Printf("  %s → %s (already exists)\n", f.RelPath, f.DstRel)
			}
		}
		if len(plan.VerifiedChanged) > 0 {
			fmt.Println("\nVerified, changed (never overwritten):")
			for _, f := range plan.VerifiedChanged {
				fmt.Printf("  %s → %s (archived copy kept)\n", f.RelPath, f.DstRel)
			}
		}
		if len(plan.DstOwned) > 0 {
			fmt.Println("\nDestination owned by a verified row (never written):")
			for _, o := range plan.DstOwned {
				fmt.Printf("  %s → %s (owned by %s:%s)\n", o.Task.RelPath, o.Path, o.Owner.SourceDisk, o.Owner.SourcePath)
			}
		}
		if len(plan.DstThroughLink) > 0 {
			fmt.Println("\nDestination through a symlink (never written):")
			for _, l := range plan.DstThroughLink {
				fmt.Printf("  %s → %s (%s is a symlink)\n", l.Task.RelPath, l.Task.DstRel, l.Link)
			}
		}
		if len(plan.Deduped) > 0 {
			fmt.Println("\nAlready archived (would be recorded as deduped, not copied):")
			for _, d := range plan.Deduped {
				if d.IntraRun {
					fmt.Printf("  %s → duplicate of %s (this run)\n", d.Task.RelPath, d.RefRel)
				} else {
					fmt.Printf("  %s → already at %s (disk %s)\n", d.Task.RelPath, d.Existing.DestPath, d.Existing.SourceDisk)
				}
			}
		}
		fmt.Printf("\n(dry-run; %d files would be copied, %d recorded as deduped, %d collisions skipped, %d verified kept, %d owned destinations skipped, %d through symlinks skipped)\n",
			len(todo), len(plan.Deduped), len(plan.DstCollisions), len(plan.VerifiedChanged), len(plan.DstOwned), len(plan.DstThroughLink))
		return 0
	}

	// Record the content-dupes before copying anything. Without a row this
	// disk has no provenance in an "auditable media archive" (vault unique
	// would claim it holds nothing), and every future scan re-hashes the whole
	// card because Lookup finds nothing to short-circuit on.
	//
	// Status "deduped" is to "copied" what by-reference is to by-value: the
	// bytes are archived, at Existing.DestPath, but this row has not itself
	// been verified against the destination. `vault verify` promotes it like
	// any other row, and `certify` keeps refusing until it does — which is the
	// same contract copied rows already have.
	if !dryRun {
		n := 0
		for _, d := range plan.Deduped {
			if d.IntraRun {
				continue // deferred: the file it references has not copied yet
			}
			if err := m.Upsert(manifest.Entry{
				SourceDisk: disk,
				SourcePath: d.Task.RelPath,
				DestPath:   d.Existing.DestPath,
				Size:       d.Task.Size,
				MtimeNs:    d.Task.MtimeNs,
				SHA256:     d.Existing.SHA256,
				CopiedAt:   time.Now().UnixNano(),
				Status:     "deduped",
			}); err != nil {
				die("record dedupe %s: %v", d.Task.RelPath, err)
			}
			n++
		}
		if n > 0 {
			fmt.Printf("Recorded %d already-archived files in the manifest (status deduped).\n", n)
		}
	}

	landed := map[string]manifest.Entry{} // source rel path -> the row that was written
	var copied, copiedBytes int64
	failed := 0
	for i, f := range todo {
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "interrupted")
			return 1
		}
		fmt.Printf("  [%d/%d] %s ... ", i+1, len(todo), f.RelPath)
		entry, err := copy.File(ctx, src, dst, f, disk)
		if err != nil {
			fmt.Printf("FAIL: %v\n", err)
			failed++
			continue
		}
		if err := m.Upsert(entry); err != nil {
			fmt.Printf("FAIL (manifest): %v\n", err)
			failed++
			continue
		}
		landed[f.RelPath] = entry
		copied++
		copiedBytes += entry.Size
		fmt.Printf("ok (sha %s…)\n", entry.SHA256[:12])
	}

	// Intra-run dupes are recorded only now, against the file they reference
	// as it ACTUALLY landed — final DstRel, and only if the copy succeeded.
	// Writing these before the loop is how a row ends up asserting content is
	// archived at a path holding foreign bytes, or nothing at all.
	var intra, orphaned int
	for _, d := range plan.Deduped {
		if !d.IntraRun {
			continue
		}
		ref, ok := landed[d.RefRel]
		if !ok {
			// The file it deduped against never made it: collision-skipped or
			// a failed copy. Record nothing — with no manifest row the next
			// run re-plans this file and copies it for real.
			fmt.Fprintf(os.Stderr,
				"  WARNING: %s deduped against %s, which was not copied — not recorded; re-run to archive it\n",
				d.Task.RelPath, d.RefRel)
			orphaned++
			continue
		}
		if err := m.Upsert(manifest.Entry{
			SourceDisk: disk,
			SourcePath: d.Task.RelPath,
			DestPath:   ref.DestPath,
			Size:       d.Task.Size,
			MtimeNs:    d.Task.MtimeNs,
			SHA256:     ref.SHA256,
			CopiedAt:   time.Now().UnixNano(),
			Status:     "deduped",
		}); err != nil {
			die("record intra-run dedupe %s: %v", d.Task.RelPath, err)
		}
		intra++
	}
	if intra > 0 {
		fmt.Printf("Recorded %d intra-run duplicates (status deduped).\n", intra)
	}
	if orphaned > 0 {
		fmt.Fprintf(os.Stderr, "%d duplicate(s) left unarchived because their reference did not copy.\n", orphaned)
	}

	fmt.Printf("\nDone. Copied %d/%d files, %s.\n", copied, len(todo), human(copiedBytes))
	if failed > 0 || orphaned > 0 || len(plan.DstCollisions) > 0 || len(plan.VerifiedChanged) > 0 || len(plan.DstOwned) > 0 || len(plan.DstThroughLink) > 0 {
		// Exit non-zero so callers can tell. scripts/nas-tars-copy-all.sh runs
		// four cards sequentially and unattended, branching on this status —
		// exiting 0 after a partial copy made it log "done" for a card that
		// had failures, which is the only signal Eddy gets. Matches runVerify,
		// which already exits 1 on mismatch/missing/errors.
		//
		// DstCollisions counts here because reaching it already means the
		// collision policy had its say and the file is STILL not archived:
		// under rename-mtime-year a task only lands there if the renamed path
		// also exists. Ordinary renames fall through to ToCopy, so a run that
		// renames files and archives them all reports zero collisions and
		// passes. A collision-skipped path gets no manifest row, and an
		// unrecorded file is the thing this archive must not hold quietly.
		//
		// VerifiedChanged counts for the opposite reason: the file on the
		// source is NOT archived and never will be under this disk name,
		// and a silent 0 here is how 2,668 photos were lost (B23(b)).
		reasons := make([]string, 0, 6)
		if failed > 0 {
			reasons = append(reasons, fmt.Sprintf("%d file(s) failed to copy", failed))
		}
		if len(plan.DstCollisions) > 0 {
			reasons = append(reasons, fmt.Sprintf("%d file(s) skipped on unresolved destination collisions", len(plan.DstCollisions)))
		}
		if len(plan.VerifiedChanged) > 0 {
			reasons = append(reasons, fmt.Sprintf("%d file(s) skipped because their verified archive copy holds different content (kept)", len(plan.VerifiedChanged)))
		}
		if len(plan.DstOwned) > 0 {
			reasons = append(reasons, fmt.Sprintf("%d file(s) skipped because a verified row owns their destination path", len(plan.DstOwned)))
		}
		if len(plan.DstThroughLink) > 0 {
			reasons = append(reasons, fmt.Sprintf("%d file(s) skipped because their destination path passes through a symlink", len(plan.DstThroughLink)))
		}
		if orphaned > 0 {
			reasons = append(reasons, fmt.Sprintf("%d duplicate(s) left unarchived", orphaned))
		}
		fmt.Fprintf(os.Stderr, "\nINCOMPLETE: %s.\n", strings.Join(reasons, "; "))
		return 1
	}
	return 0
}

func runVerify(ctx context.Context, m *manifest.Manifest, args []string) {
	onlyUnverified := false
	var pos []string
	for _, a := range args {
		if a == "--only-unverified" {
			onlyUnverified = true
			continue
		}
		pos = append(pos, a)
	}
	if len(pos) != 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, dst := pos[0], pos[1]

	if onlyUnverified {
		// Say what is NOT being checked, as loudly as what is. A full verify
		// is the archive's only bit-rot check and this pass skips it; nobody
		// should mistake an incremental run for an integrity sweep.
		skipped, newest, err := m.CountVerifiedInDisk(disk)
		if err != nil {
			die("count verified: %v", err)
		}
		fmt.Printf("Re-hashing ONLY unverified rows for disk %q at %s\n", disk, dst)
		if skipped > 0 {
			when := "unknown"
			if newest > 0 {
				when = time.Unix(0, newest).Format("2006-01-02 15:04")
			}
			fmt.Printf("  skipping %d already-verified row(s) — NOT an integrity check.\n", skipped)
			fmt.Printf("  newest verified row: %s (the newest single row, not a full-sweep date). Run without --only-unverified for a full sweep.\n\n", when)
		} else {
			fmt.Printf("  (no verified rows to skip — this is a full sweep)\n\n")
		}
	} else {
		fmt.Printf("Re-hashing destination files for disk %q at %s\n\n", disk, dst)
	}

	res, err := verify.RunWithOptions(ctx, m, disk, dst, onlyUnverified, func(sourcePath, destPath, status string) {
		// Show both sides: `deduped` rows share a dest_path, so printing the
		// destination alone makes duplicate rows indistinguishable.
		if destPath != "" && destPath != sourcePath {
			fmt.Printf("  %-10s %s → %s\n", status, sourcePath, destPath)
		} else {
			fmt.Printf("  %-10s %s\n", status, sourcePath)
		}
	})
	if err != nil {
		die("verify: %v", err)
	}

	fmt.Println()
	fmt.Printf("Verified: %d   Mismatch: %d   Missing: %d   Errors: %d   Bytes read: %s\n",
		res.Verified, res.Mismatch, res.Missing, res.Errors, human(res.BytesRead))
	if res.Mismatch > 0 || res.Missing > 0 || res.Errors > 0 {
		os.Exit(1)
	}
}

func runCertify(m *manifest.Manifest, configDir string, args []string) {
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk := args[0]
	out := ""
	if len(args) == 2 {
		out = args[1]
	}

	cert, err := certify.Build(m, disk, configDir)
	if err != nil {
		if errors.Is(err, certify.ErrNotCertifiable) {
			fmt.Fprintf(os.Stderr, "Cannot certify: %v\n", err)
			fmt.Fprintln(os.Stderr, "Run `vault verify` first and resolve any mismatches/missing.")
			os.Exit(1)
		}
		die("certify: %v", err)
	}

	data, err := json.MarshalIndent(cert, "", "  ")
	if err != nil {
		die("marshal: %v", err)
	}

	if out == "" {
		fmt.Println(string(data))
	} else {
		if err := os.WriteFile(out, data, 0o644); err != nil {
			die("write %s: %v", out, err)
		}
		fmt.Fprintf(os.Stderr, "Wrote signed certificate: %s\n", out)
		fmt.Fprintf(os.Stderr, "Files: %d   Bytes: %s   Disk: %s\n",
			cert.FileCount, human(cert.TotalBytes), cert.SourceDisk)
	}
}

// runRepairDest is B24. It returns its status like runCopy: 0 when every
// missing-dest row was repaired (or --dry-run, or nothing was missing), 1
// when rows are left that this tool could not back with a hash. Those rows
// are still `missing` to verify, which is the honest state.
func runRepairDest(ctx context.Context, m *manifest.Manifest, args []string) int {
	dryRun := false
	var pos []string
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		default:
			if strings.HasPrefix(a, "--") {
				die("unknown flag: %s", a)
			}
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, root := pos[0], pos[1]

	plan, err := repair.Build(ctx, m, disk, root)
	if err != nil {
		die("repair-dest: %v", err)
	}
	repairable, unresolved, by := plan.Counts()

	fmt.Printf("Disk %s at %s: %d rows with a dest_path, %d intact, %d unresolved (%d rows have no dest_path — located by source_path, any status — and were not examined)\n",
		disk, root, plan.Checked, plan.Intact, len(plan.Changes), plan.NoDest)
	for _, c := range plan.Changes {
		switch c.Outcome {
		case repair.Repairable:
			fmt.Printf("  %-10s %s : %s → %s\n", c.Outcome, c.Row.SourcePath, c.Row.DestPath, c.NewDest)
		case repair.Ambiguous:
			fmt.Printf("  %-10s %s : %s → %s\n", c.Outcome, c.Row.SourcePath, c.Row.DestPath, strings.Join(c.Candidates, " | "))
		case repair.Owned:
			fmt.Printf("  %-10s %s : %s → %s (already the dest_path of %s)\n", c.Outcome, c.Row.SourcePath, c.Row.DestPath, strings.Join(c.Candidates, " | "), c.Owner)
		case repair.NotAFile:
			fmt.Printf("  %-10s %s : %s is %s\n", c.Outcome, c.Row.SourcePath, c.Row.DestPath, c.Detail)
		case repair.Unsafe, repair.Conflict:
			fmt.Printf("  %-10s %s : %s (%s)\n", c.Outcome, c.Row.SourcePath, c.Row.DestPath, c.Detail)
		default:
			fmt.Printf("  %-10s %s : %s\n", c.Outcome, c.Row.SourcePath, c.Row.DestPath)
		}
	}
	fmt.Printf("\nRepairable: %d   Not found: %d   Ambiguous: %d   Owned: %d   Not a file: %d   Unsafe: %d   Conflict: %d   Bytes hashed: %s\n",
		repairable, by[repair.NotFound], by[repair.Ambiguous], by[repair.Owned], by[repair.NotAFile], by[repair.Unsafe], by[repair.Conflict], human(plan.BytesHashed))

	if dryRun {
		// repair-dest never writes archive files; the only thing it can
		// write is dest_path, and dry-run does not. (Opening the manifest
		// initializes it when absent - every command does; B31.)
		fmt.Println("(dry-run; no manifest row written, and repair-dest never writes archive files)")
		return 0
	}
	n, err := repair.Apply(m, plan, func(c repair.Change) {
		fmt.Printf("  repaired  %s : %s → %s\n", c.Row.SourcePath, c.Row.DestPath, c.NewDest)
	})
	if err != nil {
		// Rows already rewritten stay rewritten: each was backed by its
		// hash before the write, so a partial run leaves nothing wrong.
		die("repair-dest: after %d row(s) written: %v", n, err)
	}
	fmt.Printf("\nRepaired %d row(s). Status untouched — run `vault verify %s %s` to promote them.\n", n, disk, root)
	if unresolved > 0 {
		fmt.Fprintf(os.Stderr, "\nINCOMPLETE: %d row(s) still without a destination this tool can back with a hash (%d not found, %d ambiguous, %d owned by another row, %d not a file, %d unsafe, %d in conflict).\n",
			unresolved, by[repair.NotFound], by[repair.Ambiguous], by[repair.Owned], by[repair.NotAFile], by[repair.Unsafe], by[repair.Conflict])
		return 1
	}
	return 0
}

func runInventory(ctx context.Context, m *manifest.Manifest, args []string) {
	if len(args) != 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, dir := args[0], args[1]

	fmt.Printf("Inventorying %q under disk name %q\n", dir, disk)
	fmt.Println("Files already in manifest with matching size+mtime are skipped.")
	fmt.Println()

	var lastReport time.Time
	res, err := inventory.Run(ctx, m, disk, dir, func(rel, status string) {
		// quiet by default; emit a heartbeat every 5 s
		if time.Since(lastReport) > 5*time.Second {
			lastReport = time.Now()
			fmt.Printf("  %s %s\n", status, rel)
		}
	})
	if err != nil {
		die("inventory: %v", err)
	}

	fmt.Println()
	fmt.Printf("Hashed: %d   Skipped: %d   Errors: %d   Bytes read: %s\n",
		res.Hashed, res.Skipped, res.Errors, human(res.BytesRead))
}

func runDedup(m *manifest.Manifest, args []string) {
	var minSize int64
	for i := 0; i < len(args); i++ {
		if args[i] == "--min-size" && i+1 < len(args) {
			n, err := strconv.ParseInt(args[i+1], 10, 64)
			if err != nil {
				die("invalid --min-size: %v", err)
			}
			minSize = n
			i++
		} else {
			die("unknown arg: %s", args[i])
		}
	}

	groups, err := m.FindDuplicates(minSize)
	if err != nil {
		die("dedup: %v", err)
	}
	dedup.PrintDuplicates(os.Stdout, groups)
}

func runUnique(m *manifest.Manifest, args []string) {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk := args[0]

	entries, err := m.FindUniqueIn(disk)
	if err != nil {
		die("unique: %v", err)
	}
	dedup.PrintUnique(os.Stdout, disk, entries)
}

func runTag(m *manifest.Manifest, args []string) {
	if len(args) != 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, pattern, tag := args[0], args[1], args[2]
	n, err := m.ApplyTag(disk, pattern, tag)
	if err != nil {
		die("tag: %v", err)
	}
	fmt.Printf("Tagged %d files in %s matching %q with %q.\n", n, disk, pattern, tag)
}

func runUntag(m *manifest.Manifest, args []string) {
	if len(args) != 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, pattern, tag := args[0], args[1], args[2]
	n, err := m.RemoveTag(disk, pattern, tag)
	if err != nil {
		die("untag: %v", err)
	}
	fmt.Printf("Untagged %d files in %s matching %q from %q.\n", n, disk, pattern, tag)
}

func runTagged(m *manifest.Manifest, args []string) {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	tag := args[0]
	entries, err := m.FilesWithTag(tag)
	if err != nil {
		die("tagged: %v", err)
	}
	if len(entries) == 0 {
		fmt.Printf("No files tagged %q.\n", tag)
		return
	}
	var total int64
	for _, e := range entries {
		fmt.Printf("  [%s] %s  (%s)\n", e.SourceDisk, e.SourcePath, human(e.Size))
		total += e.Size
	}
	fmt.Printf("\n%d files, %s\n", len(entries), human(total))
}

func runTags(m *manifest.Manifest, args []string) {
	if len(args) != 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, path := args[0], args[1]
	tags, err := m.TagsFor(disk, path)
	if err != nil {
		die("tags: %v", err)
	}
	if len(tags) == 0 {
		fmt.Printf("No tags on %s/%s\n", disk, path)
		return
	}
	for _, t := range tags {
		fmt.Println(t)
	}
}

// runLinks materializes a directory of links to every file with `tag`,
// pointing at the file's actual location on disk. linkFn is the kernel call
// used to create each link (os.Symlink for soft, os.Link for hard).
// Caller supplies one or more `<disk>=<host-path>` pairs that map manifest
// disk names to filesystem roots.
func runLinks(m *manifest.Manifest, args []string, linkFn func(target, link string) error, kind string) {
	if len(args) < 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	tag := args[0]
	out := args[len(args)-1]
	roots := map[string]string{}
	for _, kv := range args[1 : len(args)-1] {
		parts := splitOnce(kv, '=')
		if len(parts) != 2 {
			die("expected <disk>=<path>, got %q", kv)
		}
		roots[parts[0]] = parts[1]
	}

	entries, err := m.FilesWithTag(tag)
	if err != nil {
		die("%ss: %v", kind, err)
	}
	if len(entries) == 0 {
		fmt.Printf("No files tagged %q.\n", tag)
		return
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		die("mkdir %s: %v", out, err)
	}

	var made, skipped int
	for _, e := range entries {
		root, ok := roots[e.SourceDisk]
		if !ok {
			fmt.Fprintf(os.Stderr, "  SKIP (no root for disk %q): %s\n", e.SourceDisk, e.SourcePath)
			skipped++
			continue
		}
		target := filepath.Join(root, e.SourcePath)
		linkBase := e.SourceDisk + "__" + filepath.Base(e.SourcePath)
		linkPath := filepath.Join(out, linkBase)
		// Best-effort cleanup of any stale link, then create.
		_ = os.Remove(linkPath)
		if err := linkFn(target, linkPath); err != nil {
			fmt.Fprintf(os.Stderr, "  FAIL %s %s -> %s: %v\n", kind, linkPath, target, err)
			skipped++
			continue
		}
		made++
	}
	fmt.Printf("Wrote %d %ss for tag %q at %s (skipped %d).\n", made, kind, tag, out, skipped)
}

func runMove(ctx context.Context, m *manifest.Manifest, args []string) {
	// Pull positional args + flags out of the mixed slice.
	dryRun := false
	prefix := ""
	collisionRaw := ""
	var rules []string
	var pos []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--rule":
			if i+1 >= len(args) {
				die("--rule needs a value")
			}
			rules = append(rules, args[i+1])
			i++
		case "--prefix":
			if i+1 >= len(args) {
				die("--prefix needs a value")
			}
			prefix = args[i+1]
			i++
		case "--on-collision":
			if i+1 >= len(args) {
				die("--on-collision needs a value")
			}
			collisionRaw = args[i+1]
			i++
		default:
			pos = append(pos, args[i])
		}
	}
	collision, err := mvpkg.ParseCollision(collisionRaw)
	if err != nil {
		die("%v", err)
	}
	if len(pos) != 4 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	srcDisk, dstDisk, srcRoot, dstRoot := pos[0], pos[1], pos[2], pos[3]

	parsed, err := mvpkg.ParseRules(rules)
	if err != nil {
		die("rules: %v", err)
	}

	plan, err := mvpkg.Build(m, srcDisk, srcRoot, prefix, dstRoot, parsed)
	if err != nil {
		die("plan: %v", err)
	}
	if len(plan.Moves) == 0 {
		fmt.Println("Nothing to move — no manifest entries for that src-disk.")
		return
	}

	fmt.Printf("Move plan: %s → %s\n", srcDisk, dstDisk)
	fmt.Printf("  src root: %s\n  dst root: %s\n\n", srcRoot, dstRoot)
	for _, mv := range plan.Moves {
		marker := "  (no rule)"
		if mv.RuleApplied != "" {
			marker = "  [" + mv.RuleApplied + "]"
		}
		fmt.Printf("  %s%s\n    %s\n    → %s\n", mv.SrcRel, marker, mv.SrcAbs, mv.DstAbs)
	}
	fmt.Printf("\nTotal: %d files\n\n", len(plan.Moves))

	if dryRun {
		fmt.Println("(dry-run; nothing moved)")
		return
	}

	res, err := mvpkg.Execute(ctx, m, plan, dstDisk, collision, func(mv mvpkg.Move, status string) {
		switch status {
		case "moved":
			fmt.Printf("  ok  %s\n", mv.DstRel)
		default:
			fmt.Printf("  %s  %s\n", status, mv.SrcRel)
		}
	})
	if err != nil {
		die("execute: %v", err)
	}

	fmt.Printf("\nMoved: %d   Skipped: %d   Errors: %d   Bytes moved: %s\n",
		res.Moved, res.Skipped, res.Errors, human(res.BytesMoved))
}

func runImportTags(ctx context.Context, m *manifest.Manifest, args []string) {
	if len(args) != 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	reportsDir, disk := args[0], args[1]

	fmt.Printf("Importing video-tagger reports from %s into disk %q\n\n", reportsDir, disk)
	res, err := importer.Run(ctx, m, disk, reportsDir, func(report, status string) {
		fmt.Printf("  %-30s  %s\n", report, status)
	})
	if err != nil {
		die("import: %v", err)
	}

	fmt.Println()
	fmt.Printf("Reports seen:  %d\n", res.Reports)
	fmt.Printf("Matched:       %d\n", res.Matched)
	fmt.Printf("Ambiguous:     %d\n", res.Ambiguous)
	fmt.Printf("Not in disk:   %d\n", res.NotFound)
	fmt.Printf("Tags applied:  %d\n", res.TagsApplied)
	fmt.Printf("Metadata rows: %d\n", res.MetadataWritten)
}

func splitOnce(s string, sep byte) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for nn := n / unit; nn >= unit; nn /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// hasDryRun reports whether --dry-run is among the arguments. It is only
// used to pick how the manifest is opened; each command still parses its
// own flags and rejects the flag where it means nothing.
func hasDryRun(args []string) bool {
	for _, a := range args {
		if a == "--dry-run" {
			return true
		}
	}
	return false
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
