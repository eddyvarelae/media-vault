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
	"syscall"
	"time"

	"github.com/eddyvarelae/media-vault/internal/certify"
	"github.com/eddyvarelae/media-vault/internal/copy"
	"github.com/eddyvarelae/media-vault/internal/dedup"
	"github.com/eddyvarelae/media-vault/internal/inventory"
	"github.com/eddyvarelae/media-vault/internal/manifest"
	mvpkg "github.com/eddyvarelae/media-vault/internal/move"
	"github.com/eddyvarelae/media-vault/internal/scan"
	"github.com/eddyvarelae/media-vault/internal/verify"
)

const usage = `vault — auditable media archive

Usage:
  vault scan       <source-disk-name> <source-dir> <dest-dir>
  vault copy       <source-disk-name> <source-dir> <dest-dir>
  vault verify     <source-disk-name> <dest-dir>
  vault certify    <source-disk-name> [out.json]
  vault inventory  <source-disk-name> <dir>
  vault dedup      [--min-size <bytes>]
  vault unique     <source-disk-name>
  vault tag        <source-disk-name> <path-pattern> <tag>
  vault untag      <source-disk-name> <path-pattern> <tag>
  vault tagged     <tag>
  vault tags       <source-disk-name> <path>
  vault symlinks   <tag> <source-disk>=<host-path>... <output-dir>
  vault hardlinks  <tag> <source-disk>=<host-path>... <output-dir>
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
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		die("create config dir: %v", err)
	}

	m, err := manifest.Open(filepath.Join(configDir, "manifest.db"))
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
		runCopy(ctx, m, args)
	case "verify":
		runVerify(ctx, m, args)
	case "certify":
		runCertify(m, configDir, args)
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
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func runScan(ctx context.Context, m *manifest.Manifest, args []string) {
	if len(args) != 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, src, dst := args[0], args[1], args[2]

	plan, err := scan.Build(ctx, m, disk, src, dst)
	if err != nil {
		die("scan: %v", err)
	}

	fmt.Printf("Source disk:  %s\n", disk)
	fmt.Printf("Source dir:   %s\n", src)
	fmt.Printf("Dest dir:     %s\n", dst)
	fmt.Println()
	fmt.Printf("Files to copy:    %d  (%s)\n", len(plan.ToCopy), human(plan.BytesToCopy))
	fmt.Printf("Files to skip:    %d  (in manifest, unchanged)\n", plan.SkipCount)
	fmt.Printf("Files to recopy:  %d  (%s, source size or mtime changed)\n",
		len(plan.ToRecopy), human(plan.BytesToRecopy))
}

func runCopy(ctx context.Context, m *manifest.Manifest, args []string) {
	if len(args) != 3 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, src, dst := args[0], args[1], args[2]

	plan, err := scan.Build(ctx, m, disk, src, dst)
	if err != nil {
		die("scan: %v", err)
	}

	todo := append(plan.ToCopy, plan.ToRecopy...)
	if len(todo) == 0 {
		fmt.Println("Nothing to copy. Manifest is up to date.")
		return
	}

	totalBytes := plan.BytesToCopy + plan.BytesToRecopy
	fmt.Printf("Copying %d files (%s) from %s → %s\n", len(todo), human(totalBytes), src, dst)

	var copied, copiedBytes int64
	for i, f := range todo {
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "interrupted")
			os.Exit(1)
		}
		fmt.Printf("  [%d/%d] %s ... ", i+1, len(todo), f.RelPath)
		entry, err := copy.File(ctx, src, dst, f, disk)
		if err != nil {
			fmt.Printf("FAIL: %v\n", err)
			continue
		}
		if err := m.Upsert(entry); err != nil {
			fmt.Printf("FAIL (manifest): %v\n", err)
			continue
		}
		copied++
		copiedBytes += entry.Size
		fmt.Printf("ok (sha %s…)\n", entry.SHA256[:12])
	}

	fmt.Printf("\nDone. Copied %d/%d files, %s.\n", copied, len(todo), human(copiedBytes))
}

func runVerify(ctx context.Context, m *manifest.Manifest, args []string) {
	if len(args) != 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	disk, dst := args[0], args[1]

	fmt.Printf("Re-hashing destination files for disk %q at %s\n\n", disk, dst)

	res, err := verify.Run(ctx, m, disk, dst, func(path, status string) {
		fmt.Printf("  %-10s %s\n", status, path)
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

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
