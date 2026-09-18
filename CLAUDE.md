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
`internal/verify` has package-level F4 tests (incremental pass over every
non-verified status, verified rows untouched, bytes read as proof) and
`cmd/vault` the CLI ones. `scripts/test/` holds bash tests of the NAS scripts (Docker shadowed by a
stub on `PATH`, logs redirected via `VAULT_LOG`) and of the tagging job
(a real manifest built by the freshly built `vault`, real xattrs, stubs for
the machine); `go test ./scripts/test/` runs them, so `go test ./...` is
still the one command.

## Package map

| Path | Does |
|---|---|
| `cmd/vault/main.go` | Hand-rolled arg parsing (`parseScanFlags` style — no flag frameworks), one `runX` per command, `die()` for fatal errors |
| `internal/manifest` | SQLite schema + queries. Single writer per config dir (WAL, `busy_timeout`). Rows keyed `(source_disk, source_path)` |
| `internal/scan` | Walk source (skipping dev junk and, at any depth, the tagger's `reports/` directories — B20), diff against manifest and destination → `Plan{ToCopy, ToRecopy, SkipCount, Deduped, DstCollisions, VerifiedChanged, Retouched, DstOwned, DstThroughLink}` |
| `internal/copy` | One file: refuse a destination that climbs out of the root or resolves outside it (`Escapes`, `Under` — every writer feeds through here), `Lstat` both targets, stream + sha256 → `<dst>.vault-partial` (`O_EXCL`), fsync, chtimes, rename. A failed copy leaves no partial; a refused one touches nothing |
| `internal/verify` | Re-hash destination rows → `verified` / `mismatch`; missing rows counted, not touched |
| `internal/certify` | Refuses unless every row is `verified`; signs with `$VAULT_CONFIG/key.pem` (created on first use, mode 600) |
| `internal/inventory` | NAS-side rows with no `dest_path` (`inventoried`) |
| `internal/gap` | `gap <dir> [--tsv f]`: report-only content gap (B22) — size index over **verified** rows, sha256 only on a size match, per-folder files/bytes/archived/absent with the arithmetic on the `GAP …` line; opens the manifest read-only (like `--dry-run`), writes only the TSV |
| `internal/repair` | `repair-dest`: rows whose `dest_path` is not a regular file under the root → same basename one directory down, `Lstat` only (no symlink as leaf, subdirectory or ancestor; containment proven under the resolved root), kept only on size **and** sha256 match and only if no other row's `dest_path` is that file (physical compare, as in `scan`); the row's own directory walked the same way *before* a leaf counts as intact; a chosen file is reserved so two rows of one run cannot repair to it (`CONFLICT`, both unresolved), and `Apply` re-checks claims at write time; outcomes `REPAIR` / `NOT FOUND` / `AMBIGUOUS` / `OWNED` / `NOT A FILE` / `UNSAFE` / `CONFLICT`; `Apply` rewrites `dest_path` alone (`UpdateDestPath`), status untouched so `verify` still promotes |
| `internal/restore` | `restore`: the deliberate replacement of **one** row's destination (B40 — a torn write hashed after the fact). `Build` gathers and checks every fact (row exists; destination reached through real dirs, a regular file, its current bytes hashed; no other row of any disk resolves to the same physical file — no override; replacement is a regular file hashing to the mandatory `--expect-sha`); `Apply` writes through `copy.File` with `Replace`, re-checks the landed hash, and sets the row to `copied` with the new sha/size/mtime, `verified_at` 0, `dest_path` untouched. Never promotes: `verify` does |
| `internal/dedup`, `internal/move`, `internal/importer` | Duplicate reports, manifest-aware moves, video-tagger imports |
| `scripts/backup/` | The five-minute attached-SSD gap report (B22): `run-backup.sh`, `README.md` is the contract. Allow-list of known disks, report once per attach identity and per day, manifest via the tagging helper's snapshot, `vault` built into the state dir from this checkout when stale. Tested by `scripts/test/run-backup.sh` |
| `scripts/tagging/` | The nightly video tagger (B17): `run-tagging.sh` + `tagging-helper.py`, `README.md` is the contract. Machine settings from mini-server's `mini.env`, job policy defaulted in the script (tiers, 200 GB, `verified` rows only, newest first), every policy override logged; the NAS manifest is read only through a per-run snapshot. Tested by `scripts/test/run-tagging.sh` against a manifest built by `vault` itself |
| `scripts/*.sh` | How work runs on the NAS: `docker run --rm … ghcr.io/eddyvarelae/media-vault:<tag> <command>`, sequential, as root via `sudo nohup`. `nas-tars-copy-all.sh` and `nas-verify-certify-all.sh` log through a `log()` helper (once per line under any launch form — B27/B35). `nas-kipp-copy-all.sh` (B26): `KIPP_SRC` required (container path of the disk), `DRY_RUN=1` plans only, `--dedupe-content --on-collision rename-mtime-year` on every folder, per-folder flags per `team/context/runbook-kipp.md` step 2 |

## Manifest status vocabulary

`copied` → `verified` (or `mismatch`), plus `deduped` (content already
archived under another row; `dest_path` points at it) and `inventoried`
(NAS-side row, no `dest_path`). `copy` and recopy write `copied`; only
`verify` promotes. `certify` requires **every** row for the disk to be
`verified` — the fast route to that is verifying, never editing status.

Recopy is for rows that are **not** `verified` (`copied`, `mismatch`): a
changed source replaces the destination and the row takes the new hash. A
`verified` destination is never overwritten (B23(b), decided 2026-09-17):
same `(source_disk, source_path)` with different bytes is `VerifiedChanged`
— the archived copy and its row stay as certified, the file is skipped
under every `--on-collision` policy, counted in `INCOMPLETE:`, exit 1. The
manifest keys on `(source_disk, source_path)`, so there is no second row
for the new bytes under this disk name; copy them under their own `<disk>`
with `--on-collision rename-mtime-year` and they land beside the originals.
Same size with a new mtime is hashed first: identical content is
`Retouched` and skipped like an unchanged file, no row written.

That check is by source row; the second one is by **destination**, and
by **physical location**, not spelling. `Build` indexes every `verified`
row of every disk (`VerifiedRows`) by `lower(clean(root/dest_path))`, and
before a task is admitted to `ToCopy` or `ToRecopy` looks up both paths
`copy.File` will touch — the `.vault-partial` staging name and the final
name — the same way. A hit goes to `DstOwned` and is never written, under
any policy. This is what stops a `deduped` row's recopy (its own route
lands on another disk's certified file), a new file whose staging name is
an archived file (also spelled `X.mov` on a case-folding root, also stored
as `../archive/x.mov` by a routing rule), and a new file at a verified
row's *missing* destination. The fold is unconditional: on a
case-sensitive root it can only refuse a write that differs from a
certified file by case alone. `dest_path` is relative to a root the
manifest does not record, so a hit from another disk under another root is
a false refusal — accepted: the safe direction, and the output names the
owning row.

Neither the key nor a leaf `Lstat` can see a **symlinked directory**
under the root (`dst/alias → real` makes `alias/x.mov` and `real/x.mov`
one file), so every destination path is walked component by component
from the root with `Lstat` (`scan.SymlinkComponent`) — in `Build` before
admitting, and again in `copy.File` before writing — and a symlink
component refuses the file (`DstThroughLink`, never written). A component
that does not exist yet ends the walk: `MkdirAll` creates real
directories.

The writer (`copy.File`) is the last line and checks the filesystem
itself: the component walk above, then `Lstat` on the staging path — anything there refuses the file
(a leftover partial from a crash and an archived file that happens to end
in `.vault-partial` are indistinguishable to the writer, so both refuse;
the message says to look, then remove a leftover by hand); `Lstat` on the
final path — anything there refuses a file planned as new, and only a
regular file may be replaced by a recopy (`FileTask.Replace`); the
staging file is opened `O_EXCL`, so nothing that exists is ever
truncated, and a refused open removes nothing. A writer refusal is a
per-file `FAIL`, counted in `INCOMPLETE:`, exit 1.

The one invariant: the manifest never silently holds content it has no row
for, and never claims a row it cannot back with a hash.

## Exit-code contract

Scripts branch on exit status; a silent 0 is a data-loss path. This table is
`cmd/vault/main.go` as it is — three codes, and the same rules for every
command:

- **2** — no command, an unknown command, or wrong positional arity
  (`len(args)`/`len(pos)` checks). Prints usage. Nothing else exits 2.
- Whether a run is a dry run is decided by one parse that honours
  value-taking flags (`--prefix`/`--rule`/`--on-collision`), so a literal
  `--dry-run` in a flag's value position is that value, not a mode switch
  (B31/review #27); the same decision opens the manifest and runs the
  command.
- **1** — everything fatal: every `die(...)` (bad flag value or a flag missing
  its value, config dir or manifest open failure, scan/plan error, manifest
  write error, signing/marshal/output-file error, empty manifest), an
  interrupt (`interrupted` on stderr), and the per-command conditions below.
  Ordering, where it differs: `scan` and `copy` validate every flag value
  before arity, so a bad `--rule`/`--on-collision` with wrong arity is 1.
  `move` validates a missing flag value and `--on-collision` before arity
  but `--rule` values only after it, so `move --rule <malformed>` with wrong
  arity is 2. `TestExitCodeOrdering` in `cmd/vault` pins this.
- **0** — the command ran to the end. For several commands that is *not* the
  same as "nothing went wrong" — see the last column.

| Command | Exits 1 when | Exits 0 even though |
|---|---|---|
| `scan` | scan error (unreadable source, cancelled); a `--rule` whose subdir is absolute or has a `..` component (`invalid rule`, `die`) — same for `copy` and `move`, B34 | collisions/recopies/verified-changed are predicted — it only reports |
| `copy` | run finished `INCOMPLETE:` — any file failed, any unresolved destination collision, any file whose verified archive copy holds different content (kept, never overwritten), any file whose destination or staging path a verified row owns, any file whose destination path passes through a symlinked directory, any intra-run duplicate left unarchived (stderr names which); interrupted between files; `die` on scan or manifest-write error | no-op (including only retouched files), `--dry-run` (even with predicted collisions, verified-changed or owned files). `--dry-run` writes **no archive file and no manifest row**, and (B31) neither creates the config dir nor initializes `manifest.db`: the manifest is opened read-only (`OpenReadOnly`, `mode=ro`, proven by a write that must fail) or, when none exists, planned against an empty in-memory one with a notice on stderr. A read-only open of a WAL database still creates `-shm`/empty `-wal` beside it — SQLite's, not ours |
| `verify` | any mismatch, missing, or read error; `die` on cancel | — |
| `certify` | output path not a regular file or absent (a symlink at the output name — dangling or not — a directory: `Cannot certify: certificate output path is not a regular file`); output path inside the tree it certifies (`Cannot certify: certificate output is inside the archive …`) — with `--root <dest-dir>` by physical containment (resolved paths, `filepath.Rel`), without it by recognising the tree from its own files (`certify.InsideArchive`: an ancestor of the output path under which a row's `dest_path` exists as a regular file of the row's size — a fallback that a damaged tree defeats, so the scripts always pass `--root`); all before signing and before the key is created; the certificate is then written to `<out>.vault-partial` opened `O_CREATE|O_EXCL|O_NOFOLLOW`, fsynced and renamed over the leaf — a symlink substituted at `out` after the check is *replaced* by the rename, never followed (the parent directory itself is not bound; that needs `os.Root`, Go 1.25 — backlog); a stale `<out>.vault-partial` refuses (`die`); `--root` without a value or an unknown flag (`die`); any row not `verified` (`Cannot certify: …`); no rows for the disk; key/sign/marshal/write error | — |
| `repair-dest` | unknown flag (`die`); query, read-dir or hash error (`die`); write error mid-run (`die`, names how many rows were already written — each was hash-backed, so they stand); after a real run, any row still unresolved — `NOT FOUND`, `AMBIGUOUS`, `OWNED`, `NOT A FILE`, `UNSAFE`, `CONFLICT` (`INCOMPLETE:` on stderr, counts per outcome) | `--dry-run` (even with unresolved rows) — it writes no manifest row, and `repair-dest` never writes archive files; and (B31) it neither creates the config dir nor initializes `manifest.db`: the manifest is opened read-only (`OpenReadOnly`, `mode=ro`) or, when none exists, planned against an empty in-memory one; a disk with no rows |
| `gap` | unknown flag or `--tsv` without a value (`die`); walk, read or TSV write error (`die`) | a disk that needs archiving is still exit 0 — the `GAP` line is the answer, and a script keys on `needs archiving: yes/no` |
| `restore` | any refusal (`Cannot restore: …`): unknown row, destination outside the root (a row path that climbs out or is absolute — lexically, then physically under the resolved root — refused before anything is read), destination missing / not a regular file / through a symlinked directory, another row reaching the same file — by file identity (`os.SameFile`: directory-symlink, leaf-symlink, hard-link and case aliases all count; a row whose file cannot be stat'ed is **not** a claimant when the error is ENOENT (its file is not under this root), and **refuses** the restore on any other stat error — there is no spelling fallback, since a different disk's row is relative to a root restore does not record — review #30/#39), replacement missing / not a regular file / hashing to something other than `--expect-sha`, malformed or missing `--expect-sha`, unknown flag; I/O or manifest error (`die`); landed hash ≠ `--expect-sha` after the rename (`die`, names the file as in doubt) | `--dry-run` (prints everything incl. `Claimants: none`); destination already holds the expected bytes (nothing written). Success prints one `RESTORED <disk> <path> old=<sha>:<size> new=<sha>:<size> expect=<sha> prior_status=… prior_verified_at=… from=<file>` line |
| `inventory` | `die` on walk error | per-file hash errors — counted in `Errors:`, exit 0 |
| `dedup` | unknown arg or bad `--min-size` (`die`, not usage); query error | — |
| `unique`, `tag`, `untag`, `tagged`, `tags` | query error | no matches (`No files tagged …`) |
| `symlinks`, `hardlinks` | malformed `<disk>=<path>`; query or mkdir error | individual links that FAIL or SKIP — counted, exit 0 |
| `move` | bad `--on-collision`/`--rule`; plan or execute error | per-file `Errors:`/`Skipped:` in the summary — exit 0, including (B32) a destination a verified row owns (`dst-owned by verified row …`) or one through a symlinked directory (`dst through a symlink …`), both never written; `--dry-run` |
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
- Certificates live beside the manifest (`/volume1/docker/vault-certs/` on
  the NAS, `VAULT_CERTS` in `nas-verify-certify-all.sh`), never inside the
  tree they certify; `certify --root <dest-dir>` refuses (always pass
  `--root` from scripts — without it the tree is only recognised by files
  that still match their rows), and never writes through a symlink at the
  output name. A `--rule` never routes outside the destination root.
- Atomic destination writes only (`.vault-partial` created `O_EXCL` → fsync
  → rename). The staging path must be empty; the writer never truncates.
- A `verified` destination is never overwritten by `copy` — nor by `move`
  (B32). Not by recopy, not by any collision policy, not through another
  row's route, not as a staging file, not through a symlinked directory,
  not when the verified file is missing (the row still owns the path). The
  one deliberate path is `vault restore`: one named row, the replacement's
  sha stated up front, and the row drops back to `copied`.
  Both checks — by source row and by destination path — live in
  `scan.Build`, so `scan` and `copy` agree and every write `copy.File`
  makes was admitted there.
- One `vault` process per config dir; read-only queries need `?mode=ro`.
- Nothing secret in the repo. `vault-config/` is gitignored.
- Comments explain *why*, not what. Match the surrounding style; no new frameworks.

## Branch / review / deploy

- Work on a branch per work-order item off the current `main` tip, in your own
  worktree. Never `git add -A`; stage only your paths.
- Nothing merges to `main` without an `APPROVE` in
  `team/channels/review-requests.md`; the PM merges (never rewriting).
- `.github/workflows/docker.yml` publishes on `v*` tags and manual dispatch
  only, and never `:latest` (`flavor: latest=false` — metadata-action's
  default adds `:latest` to every semver tag by itself). The NAS scripts pin
  a release tag (`IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:vX.Y.Z}"`),
  so a merge to `main` is not a deploy; cutting a tag is. Bump the scripts'
  default in the same PR as the release — `docs/release.md`.
- Roles, sacred paths, and the evidence ladder live in `team/TEAM.md`. Author
  evidence caps at `tested`.
