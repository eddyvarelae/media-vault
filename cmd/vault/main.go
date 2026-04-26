package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/eddyvarelae/media-vault/internal/certify"
	"github.com/eddyvarelae/media-vault/internal/copy"
	"github.com/eddyvarelae/media-vault/internal/manifest"
	"github.com/eddyvarelae/media-vault/internal/scan"
	"github.com/eddyvarelae/media-vault/internal/verify"
)

const usage = `vault — auditable media archive

Usage:
  vault scan    <source-disk-name> <source-dir> <dest-dir>
  vault copy    <source-disk-name> <source-dir> <dest-dir>
  vault verify  <source-disk-name> <dest-dir>
  vault certify <source-disk-name> [out.json]

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
