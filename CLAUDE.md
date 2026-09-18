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
with `VAULT_CONFIG` and cwd pinned to temp dirs. Every package that writes
fixtures calls `testguard.Require()` from its `TestMain`
(`internal/testguard`): it resolves the temp root (`Abs` + symlinks) and exits
1 if it is under `/volume1` or `/mnt`. A new test package that writes files
gets the same three-line `TestMain`. Never point a test at either root.
`scripts/test/` holds bash tests of the NAS scripts (Docker shadowed by a
stub on `PATH`, logs redirected via `VAULT_LOG`); `go test ./scripts/test/`
runs them, so `go test ./...` is still the one command.

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

Scripts branch on exit status; a silent 0 is a data-loss path. This table is
`cmd/vault/main.go` as it is — three codes, and the same rules for every
command:

- **2** — wrong positional arity (`len(args)`/`len(pos)` checks), no command,
  or an unknown command. Prints usage. Nothing else exits 2.
- **1** — everything fatal: every `die(...)` (bad flag value or a flag missing
  its value, config dir or manifest open failure, scan/plan error, manifest
  write error, signing/marshal/output-file error, empty manifest), an
  interrupt (`interrupted` on stderr), and the per-command conditions below.
  Flags are parsed **before** arity for `scan`/`copy`/`move`, so a bad flag
  value with wrong arity is 1, not 2.
- **0** — the command ran to the end. For several commands that is *not* the
  same as "nothing went wrong" — see the last column.

| Command | Exits 1 when | Exits 0 even though |
|---|---|---|
| `scan` | scan error (unreadable source, cancelled) | collisions/recopies are predicted — it only reports |
| `copy` | run finished `INCOMPLETE:` — any file failed, any unresolved destination collision, any intra-run duplicate left unarchived (stderr names which); interrupted between files; `die` on scan or manifest-write error | no-op, `--dry-run` (even with predicted collisions) |
| `verify` | any mismatch, missing, or read error; `die` on cancel | — |
| `certify` | any row not `verified` (`Cannot certify: …`); no rows for the disk; key/sign/marshal/write error | — |
| `inventory` | `die` on walk error | per-file hash errors — counted in `Errors:`, exit 0 |
| `dedup` | unknown arg or bad `--min-size` (`die`, not usage); query error | — |
| `unique`, `tag`, `untag`, `tagged`, `tags` | query error | no matches (`No files tagged …`) |
| `symlinks`, `hardlinks` | malformed `<disk>=<path>`; query or mkdir error | individual links that FAIL or SKIP — counted, exit 0 |
| `move` | bad `--on-collision`/`--rule`; plan or execute error | per-file `Errors:`/`Skipped:` in the summary — exit 0; `--dry-run` |
| `import-tags` | import error | ambiguous / not-found reports — counted, exit 0 |

A collision counts against `copy` only after the policy ran: under
`--on-collision rename-mtime-year` a successful rename lands in `ToCopy`; a
task reaches `DstCollisions` only if the renamed path also exists. The exit
code must not depend on `--dedupe-content` (settled, see `team/BACKLOG.md →
Deferred`). `runCopy` returns its status; `main` applies it — keep new
commands on that pattern so the decision stays testable. The "exits 0 even
though" column is documented behavior, not endorsed behavior: a script
that needs to branch on those outcomes cannot today.

## Announce what you skipped

Any partial pass prints what it did **not** check as loudly as what it did:
dedupe prints eligible-row counts and says when cross-disk dedupe was
impossible; `INCOMPLETE:` lists every reason that fired; an incremental verify
(`--only-unverified`) states how many `verified` rows it skipped and the
**newest single-row `verified_at`** among them — explicitly labeled as one
row's date, **not** a full-sweep date. No command records when a disk was last
fully verified; per-row timestamps cannot prove it, so never claim it. A
number nobody can recompute is a finding, not a fact.

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
  tags. The NAS scripts pin a release tag
  (`IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:vX.Y.Z}"`), so a merge
  to `main` is not a deploy; cutting a tag is. Bump the scripts' default in the
  same PR as the release — `docs/release.md`.
- Roles, sacred paths, and the evidence ladder live in `team/TEAM.md`. Author
  evidence caps at `tested`.
