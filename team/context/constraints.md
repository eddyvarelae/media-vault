# Engineering constraints (PM, 2026-09-16)

- **Go 1.23+, `CGO_ENABLED=0`** - the image is alpine + a static binary. No dependency that needs cgo.
- **Manifest is single-writer** (SQLite WAL + busy_timeout). One `vault` process per config dir at a time. Read-only queries from a second process are fine (`?mode=ro`).
- **Never write to a source disk.** Containers mount `/sources` read-only; the CLI never opens a source for writing.
- **Atomic destination writes.** Copy to `.vault-partial`, fsync, rename. mtime preserved so re-scans skip.
- **Exit codes are the contract with the scripts.** `copy` exits 1 on any failed / orphaned / collision-blocked file (F2, F3). Scripts branch on exit status; a silent 0 is a data-loss path.
- **Announce what you didn't check.** Any partial pass (`--only-unverified`, dedupe eligibility) prints what it skipped as loudly as what it did.
- **Settled calls live in `BACKLOG.md → Deferred`** and are not reopened in code review or channels.
- **Style:** the codebase is small and flat - match `parseScanFlags`-style hand-rolled flag parsing, no new frameworks; comments explain *why*, not what. Conventions doc (`CLAUDE.md`) is Dev's to create and keep current in the same commit as the behavior it describes.
