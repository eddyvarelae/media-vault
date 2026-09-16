# media-vault — conventions

Go 1.23 CLI (`cmd/vault`) that mirrors a source SSD to a NAS with a per-file
sha256 manifest (SQLite), re-verifies the destination, and signs an Ed25519
**wipe certificate**. This file is the contract external agents read. Keep it
current **in the same commit** as any behavior it describes.

## Build / test

```
go build ./...            # CGO_ENABLED=0 must work; the image is alpine + static binary
go vet ./...
go test ./... -count=1    # temp dirs + temp manifest only; no NAS, no network
```

Tests run the real pipeline against `t.TempDir()`. `cmd/vault` tests re-exec
the test binary through `main()` (`VAULT_TEST_MAIN=1`) to get real exit codes,
with `VAULT_CONFIG` and cwd pinned to temp dirs. `TestMain` refuses to run if
`TMPDIR` is under `/volume1` or `/mnt`. Never point a test at either.

## Package map

| Path | Does |
|---|---|
| `cmd/vault/main.go` | Hand-rolled arg parsing (`parseScanFlags` style — no flag frameworks), one `runX` per command, `die()` for fatal errors |
| `internal/manifest` | SQLite schema + queries. Single writer per config dir (WAL, `busy_timeout`). Rows keyed `(source_disk, source_path)` |
| `internal/scan` | Walk source, diff against manifest and destination → `Plan{ToCopy, ToRecopy, SkipCount, Deduped, DstCollisions}` |
| `internal/copy` | One file: stream + sha256 → `<dst>.vault-partial`, fsync, chtimes, rename. A failed copy leaves no partial |
| `internal/verify` | Re-hash destination rows → `verified` / `mismatch`; missing rows counted, not touched |
| `internal/certify` | Refuses unless every row is `verified`; signs with `$VAULT_CONFIG/key.pem` (created on first use, mode 600) |
| `internal/inventory` | NAS-side rows with no `dest_path` (`inventoried`) |
| `internal/dedup`, `internal/move`, `internal/importer` | Duplicate reports, manifest-aware moves, video-tagger imports |
| `scripts/*.sh` | How work runs on the NAS: `docker run --rm … ghcr.io/eddyvarelae/media-vault:<tag> <command>`, sequential, as root via `sudo nohup` |

## Manifest status vocabulary

`copied` → `verified` (or `mismatch`), plus `deduped` (content already
archived under another row; `dest_path` points at it) and `inventoried`
(NAS-side row, no `dest_path`). `copy` and recopy write `copied`; only
`verify` promotes. `certify` requires **every** row for the disk to be
`verified` — the fast route to that is verifying, never editing status.

The one invariant: the manifest never silently holds content it has no row
for, and never claims a row it cannot back with a hash.

## Exit-code contract

Scripts branch on exit status; a silent 0 is a data-loss path.

| Command | 0 | 1 | 2 |
|---|---|---|---|
| `copy` | every planned file archived; also no-op and `--dry-run` | run finished `INCOMPLETE:` — any file failed, any unresolved destination collision, any intra-run duplicate left unarchived. stderr names which | usage |
| `verify` | all rows hashed and matched | any mismatch, missing, or read error | usage |
| `certify` | certificate written/printed | any row not `verified` (`Cannot certify: …`) | usage |

A collision counts against `copy` only after the policy ran: under
`--on-collision rename-mtime-year` a successful rename lands in `ToCopy`; a
task reaches `DstCollisions` only if the renamed path also exists. The exit
code must not depend on `--dedupe-content` (settled, see `team/BACKLOG.md →
Deferred`). `runCopy` returns its status; `main` applies it — keep new
commands on that pattern so the decision stays testable.

## Announce what you skipped

Any partial pass prints what it did **not** check as loudly as what it did:
dedupe prints eligible-row counts and says when cross-disk dedupe was
impossible; `INCOMPLETE:` lists every reason that fired; an incremental verify
must say how many rows it skipped and when the disk was last fully verified.
A number nobody can recompute is a finding, not a fact.

## Hard rules

- Never write to a source disk. Containers mount `/sources` read-only.
- Atomic destination writes only (`.vault-partial` → fsync → rename).
- One `vault` process per config dir; read-only queries need `?mode=ro`.
- Nothing secret in the repo. `vault-config/` is gitignored.
- Comments explain *why*, not what. Match the surrounding style; no new frameworks.

## Branch / review / deploy

- Work on a branch per work-order item off the current `main` tip, in your own
  worktree. Never `git add -A`; stage only your paths.
- Nothing merges to `main` without an `APPROVE` in
  `team/channels/review-requests.md`; the PM merges (never rewriting).
- `.github/workflows/docker.yml` publishes on every push to `main` and on `v*`
  tags. **The NAS scripts currently pull `:latest`, so a merge to `main` is a
  production deploy** (B4 pins a tag; see `docs/release.md` once it lands).
- Roles, sacred paths, and the evidence ladder live in `team/TEAM.md`. Author
  evidence caps at `tested`.
