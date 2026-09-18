# dev-questions.md - PM <-> Dev

Protocol: the PM's current WORK ORDER lives at the top (newest supersedes; work it top-down). Questions and answers below it, inline, newest question first. Every note dated and signed: **Role (YYYY-MM-DD):**.

Boot line (Mac mini, visible terminal, Opus-class, Remote Control on): `cd ~/Projects/media-vault-dev && claude --model opus --remote-control media-vault-dev "You are the Dev for media-vault. Read team/TEAM.md, team/actors/dev.md, then team/channels/dev-questions.md."`

## WORK ORDER - rev 3

**PM (2026-09-17):** Review #2 came back **FINDINGS** (five, read them in full in `team/channels/review-requests.md`). `tests-and-pinning` is unfrozen **for fixes only** - new commits on top of `14f4e2c`, never a rewrite. Do this before rev 2's `ci-and-docs` items; rev 2 is superseded below and its items 1-3 fold in here as item 6 so the CLAUDE.md edits land once.

1. **Finding 1 (P1) - NAS guard in every writing test package.** One shared guard (a tiny internal test helper package, or a copied `TestMain` if you'd rather avoid a new package - your call, say which) that refuses to run when the *resolved* absolute temp root (`filepath.EvalSymlinks` + `filepath.Abs`) is under `/volume1` or `/mnt`. Apply in `cmd/vault`, `internal/certify`, `internal/scan`, `internal/copy`. Prove it: a test-of-the-guard that sets `TMPDIR` to a symlink pointing at a temp dir named `.../volume1/...` is fine as long as it never touches the real roots - or explain why you can't test it safely and I'll accept a trace.
2. **Finding 2 - round-trip asserts persisted manifest state.** Reopen the manifest after `certify` and compare rows (path, hash, status, `verified_at`) against expectations; for the missing-file case, snapshot the row before and assert it is byte-identical after (status untouched).
3. **Finding 3 - shell `FAILED` branch test.** A bash test (`scripts/test/`, run by `go test` via `exec` or a `make test`-style target - keep it in `go test ./...` so one command covers everything) that runs `nas-tars-copy-all.sh` with `docker` shadowed by a stub on `PATH` that exits 1, `LOG` pointed at a temp file, and asserts the log contains `FAILED`. The script hard-codes `LOG=/volume1/docker/tars-copy.log` - make it `LOG="${VAULT_LOG:-/volume1/docker/tars-copy.log}"` (same shape as `IMG`), nothing else changes.
4. **Finding 4 - `CLAUDE.md:68`.** Replace "when the disk was last fully verified" with the approved F4 wording: the newest single-row `verified_at`, explicitly not a full-sweep date. A full sweep has no recorded date; say so.
5. **Finding 5 - exit-code table.** Exit 2 = wrong positional arity only; exit 1 = `INCOMPLETE:` runs *and* every `die` (bad flag value, config/scan error, signing/output error, empty manifest) *and* interrupt. One table, all commands, matching `cmd/vault/main.go` as it is.
6. **Former rev 2 items 1-3** (B16 CI on tags only + `ugos.md` tags; B15 gofmt as its own commit; B14 README). Same branch, after 1-5.

**Evidence expected:** `go test ./... -count=1` green including the new shell test; paste the guard's refusal output from a deliberately bad `TMPDIR`; `gofmt -l` empty; the resulting CI `on:` block. Rung: `tested`. READY FOR REVIEW with the tip SHA; the PM stages #3 as "check the fixes" only. Do not merge.

## WORK ORDER - rev 2 (Superseded by rev 3 on 2026-09-17 - folded into rev 3 item 6, not started)

**PM (2026-09-17):** Branch `ci-and-docs` off the current `main` tip (`f7df496` - team files only on top of `5a92286`, so code-identical to your rev-1 base). Switch your worktree to it; leave `tests-and-pinning` exactly as committed. Small PR, four items:

1. **B16 - CI publishes on tags only.** `.github/workflows/docker.yml`: drop the `branches: [main]` push trigger; keep `tags: ["v*"]` and `workflow_dispatch`. Drop the `type=raw,value=latest` and `type=ref,event=branch` tag lines so `:latest` is never republished by accident (keep `sha-` and semver). Update `docs/release.md` and the CLAUDE.md deploy line to match. Also `docs/install/ugos.md`: replace the two `latest` references (lines ~23/36) with `v0.2.0` and one sentence on where to find the current tag.
2. **B15 - `gofmt -w`** on the three flagged files, as its own commit, whitespace only.
3. **B14 - README "How it works"** steps 3 and 4 still say "coming next"; rewrite them as present tense matching the code. Add `--only-unverified` to the README's verify example with the one-line warning that it is not an integrity sweep.
4. Nothing else. B9 (F4 tests) is a separate branch once F4 is on `main` - I will say when.

**Evidence expected:** `bash -n` on nothing (no scripts change), `gofmt -l` empty, `go test ./... -count=1` still green, and for item 1 paste the resulting `on:` block. Rung: `tested`. Write READY FOR REVIEW with the tip SHA; do not merge.

## WORK ORDER - rev 1 (Superseded by rev 2 on 2026-09-17 - completed, under review #2)

**PM (2026-09-16):** You work in your own worktree, `~/Projects/media-vault-dev` (branch `tests-and-pinning`, created by the PM off `main`) - never in `~/Projects/media-vault`, which is the PM's checkout on `main`. `team/` files are shared through git: commit your channel notes on your branch and the PM merges them; if you need a note seen immediately, also write it into `~/Projects/media-vault/team/channels/dev-questions.md` (same file, PM's checkout). **Do not touch `verify-incremental`** - it is under Reviewer review (request #1). Three items, one PR, in this order:

1. **B3 - test harness.** `go test ./...` must exercise the real pipeline on a temp dir + temp manifest (`t.TempDir()`, no NAS, no network): scan → copy → verify → certify round-trip; recopy on changed size/mtime; `.vault-partial` never survives a failed copy; `certify` refuses on any non-`verified` row; and the F3 "done when" list (`team/archive/2026-09-15-handoff-f3-f4.md` § F3) as explicit cases - unresolved collision → exit 1 with `collisions` named in `INCOMPLETE:`; every-rename-succeeds → exit 0; dry-run / no-op → exit 0. `runCopy` calls `os.Exit` - refactor the minimum needed to make the exit decision testable (e.g. return a status), no framework, no behavior change. Also: tests must not reach `/volume1` or `/mnt` even if those exist on the host.
2. **B5 - `CLAUDE.md`** at repo root: how to build/test, the package map, the manifest status vocabulary, the exit-code contract, the "announce what you skipped" rule, and the branch/review policy from `team/TEAM.md`. Short; it is the contract external agents read.
3. **B4 - pin the image tag.** All five `scripts/*.sh` take `IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.0}"` (one variable each, env-overridable). `v0.2.0` will not exist until the PM tags it after this PR and F4 merge - say so in a comment. Add a `docs/release.md` (5 lines: tag on `main`, CI publishes semver, bump the scripts' default in the same PR as a release).

**Evidence expected:** paste `go test ./... -count=1` output and `go vet ./...` in this channel under your rev-1 note; state the rung (`tested` is the ceiling for you). When done, write "READY FOR REVIEW" with the branch tip SHA; the PM stages it in `review-requests.md`. Do not merge.

**Out of scope for rev 1:** F4 tests (B9 - after #1 merges), anything under `internal/verify` (frozen until #1 resolves), README roadmap items.

Flag in-progress local work at the top of your first note so the Tester knows your worktree is live.

## Dev notes

**Dev (2026-09-17T18:21-07:00) - rev 3 done. READY FOR REVIEW.** **Code tip `c1f7fbd`** on `tests-and-pinning` - seven new commits on top of `14f4e2c`/`a2da6a3`, no rewrite (`git merge-base --is-ancestor 14f4e2c HEAD` holds). Worktree live but idle. **Rung: `tested`.** `internal/verify` and `verify-incremental` untouched (one local mutation of `verify.go` for a check, reverted, `git status` clean there). The PM's uncommitted `team/BACKLOG.md` / `review-requests.md` edits sit in my worktree unstaged - PM paths, left alone.

Finding → commit, in order:
1. **F1 → `71bb5c5`** - chose the **shared package** `internal/testguard` over four copied `TestMain`s: one resolver to review, one test of the guard, nothing to drift. `Require()` resolves `os.TempDir()` (`Abs` + `EvalSymlinks`; a dangling symlink followed by `Readlink`; a non-existent tail re-appended to its longest existing ancestor) and exits 1 if under `/volume1` or `/mnt` (roots compared in literal and resolved spellings). Called first thing in `TestMain` of `cmd/vault`, `internal/{certify,scan,copy}` (+ `testguard` itself, `scripts/test`). `cmd/vault`'s private guard and `safeDir` are gone. **Tested safely** via `CheckUnder(dir, roots)` against a fake root under `t.TempDir()`: exact root, child, symlink, relative symlink, dangling symlink, non-existent tail, `..` spelling all refuse; sibling, `volume10`, and a deeper dir merely *named* `volume1` pass (root-anchored, not substring - which is also why the PM's "symlink to a temp dir named `.../volume1/...`" must NOT trip the real guard). CLAUDE.md test paragraph now states the suite-wide mechanism.
2. **F2 → `55a28a9`** - `rowsOf()` reopens the manifest fresh at each stage. Asserted: rows after copy (dest, sha256-of-content, size, `copied`, `verified_at` 0); after verify (all `verified`, `verified_at` set, sha/`copied_at` unchanged); **across certify: `reflect.DeepEqual` before/after**, and every cert file ref = its row on dest/sha/size/`verified_at`, counts equal; after recopy (new sha + mtime, `copied`, `verified_at` 0, the two other rows DeepEqual their prior state, re-verify advances `verified_at`); after corruption (row keeps the sha the file *had*, `mismatch`, stamped; others still `verified`); **missing file: row snapshot DeepEqual after**. Mutation: making verify's missing branch call `MarkMismatch` fails `missing-file verify touched the row`.
3. **F3 → `15ccdd5`** - `nas-tars-copy-all.sh`: `LOG="${VAULT_LOG:-/volume1/docker/tars-copy.log}"`, nothing else (the test greps for that exact default line). `scripts/test/nas-tars-copy-all.sh` runs the real script with a `docker` stub on `PATH` that records argv and exits 1 / 0: stub-1 → all four cards log `<card> FAILED`, none `done`, exactly 4 FAILED lines, pass runs to the end, stub saw the real `copy media-djiflip /usb/sdc1/DJIFlip /volume1/media/DJIFlip … --on-collision rename-mtime-year` argv (so it was the thing invoked); stub-0 → four `done`, zero FAILED. `scripts/test/scripts_test.go` runs it under `go test ./...` (guarded, `TMPDIR=t.TempDir()`). Mutation: `FAILED`→`done` in the script fails 8 checks.
4. **F4 → `50fc0d2`** - CLAUDE.md "announce" rule: newest **single-row** `verified_at`, labeled as one row's date, explicitly not a full sweep; "no command records when a disk was last fully verified; per-row timestamps cannot prove it, so never claim it."
5. **F5 → `50fc0d2`** - exit-code contract rewritten from every `os.Exit`/`die` site: **2** = wrong positional arity / no or unknown command, nothing else; **1** = every `die` (bad flag value or missing value, config dir / manifest open, scan/plan, manifest write, sign/marshal/write, empty manifest) + interrupt + per-command conditions; flags parse *before* arity for `scan`/`copy`/`move` so bad-flag-with-wrong-arity is 1. One table, all 15 commands, with an honest "exits 0 even though" column - see "noticed" below.
6. **Former rev 2 → `d007744` (B16), `d146091` (B15), `c1f7fbd` (B14).** B16: `on:` block below; `type=ref,event=branch` and `type=raw,value=latest` dropped, `sha-` + semver kept; `ugos.md` two `latest` → `v0.2.0` + one sentence on where the current tag lives; `docs/release.md` step 1-2 and CLAUDE.md deploy line updated in the same commit; only remaining `latest` in the repo is `runs-on: ubuntu-latest`. B15: `git diff -w` empty for the three files. B14: steps 3-4 present tense, `--only-unverified` example with the not-an-integrity-sweep warning; also dropped the "(soon)" in "Why not rsync" - same stale fact, same doc, saying so here.

```yaml
on:
  push:
    tags: ["v*"]
  workflow_dispatch:
```

Evidence (Mac mini, `2026-09-17T18:20:23-07:00`, go1.27.0 darwin/arm64):
```
$ gofmt -l .                      → (empty)
$ go vet ./...                    → clean
$ bash -n scripts/*.sh scripts/test/*.sh   → clean
$ go test ./... -count=1
ok  	github.com/eddyvarelae/media-vault/cmd/vault	0.467s
ok  	github.com/eddyvarelae/media-vault/internal/certify	0.489s
ok  	github.com/eddyvarelae/media-vault/internal/copy	0.677s
ok  	github.com/eddyvarelae/media-vault/internal/scan	1.045s
ok  	github.com/eddyvarelae/media-vault/internal/testguard	0.845s
ok  	github.com/eddyvarelae/media-vault/scripts/test	1.474s
(dedup, importer, inventory, manifest, move, verify: no test files)
$ CGO_ENABLED=0 go test ./... -count=1     → same 6 ok
44 PASS lines (tests + subtests)
```

Guard refusal, from compiled test binaries (`go test -c`) run by hand - nothing created, `/volume1` and `/mnt` still absent on this host afterwards:
```
$ TMPDIR=/volume1/review-tmp ./vault.test        (same for certify/scan/copy .test)
testguard: refusing to run: temp dir "/volume1/review-tmp" resolves to "/volume1/review-tmp", under forbidden root /volume1
exit 1
$ TMPDIR=$SCRATCH/innocent-link ./copy.test      (innocent-link -> /volume1/review-tmp, dangling)
testguard: refusing to run: temp dir ".../scratchpad/guard/innocent-link" resolves to "/volume1/review-tmp", under forbidden root /volume1
exit 1
$ cd /usr && TMPDIR=../mnt/@usb/x ./copy.test
testguard: refusing to run: temp dir "../mnt/@usb/x" resolves to "/mnt/@usb/x", under forbidden root /mnt
exit 1
$ TMPDIR=$SCRATCH/guard/ok ./copy.test           → PASS
```

Noticed, not acted on (PM to triage):
- While tracing F5: `inventory`, `move`, `symlinks`/`hardlinks`, `import-tags` **exit 0 with per-file errors** (counted in the summary only). Documented as-is in CLAUDE.md's last column. `nas-verify-certify-all.sh` doesn't call them, `nas-inventory.sh` does call `inventory` - a walk-level error is 1, a per-file hash error is 0. Same class of gap F2/F3 closed for `copy`; proposing a backlog item, not touching it.
- `scan`'s F5 row is honest but thin: it has no non-zero for "collisions predicted". By design (it only reports); noting so nobody reads the table as a bug list.

**Dev (2026-09-16T12:18-07:00) - rev 1 done. READY FOR REVIEW.** Worktree `~/Projects/media-vault-dev` still live but idle; **code tip `14f4e2c`** on `tests-and-pinning` (3 commits off `main` @ `5a92286`, fast-forwardable; this note is one further commit touching only `team/`). Not pushed - PM's call. **Rung: `tested`** (author ceiling). `verify-incremental` and `internal/verify` untouched.

Commits, in work-order order:
1. `e38449f` **B3** - `runCopy` returns `int`; `main` does `os.Exit(code)` when non-zero. Same codes as before (0/1, usage still 2 via the existing path); that is the entire refactor. 29 test cases across `cmd/vault`, `internal/{copy,scan,certify}`. `cmd/vault` round-trip re-execs the test binary through `main()` (`VAULT_TEST_MAIN=1`) for real exit codes; F3 cases call `runCopy` in-process. `TestMain` refuses to run if `TMPDIR` is under `/volume1` or `/mnt`; every path handed to the CLI comes from `t.TempDir()` through a guard, `VAULT_CONFIG` and cwd pinned per subprocess so the `./vault-config` fallback is unreachable.
   - F3 "done when", each an explicit case: unresolved collision (skip, and rename still blocked) → 1 with `1 file(s) skipped on unresolved destination collisions` in `INCOMPLETE:`; every-rename-succeeds → 0 (and the row points at `only_2023.mov`, original untouched); dry-run with collisions → 0, zero files, zero rows; no-op → 0; F2 failed → 1 naming `failed to copy`; F2 orphaned → 1 naming both reasons.
   - Round-trip: scan(3) → copy → verify (3/0/0/0) → certify → `certify.Verify` OK on the JSON; certify exit 1 before verify; recopy on size+mtime change AND on mtime-only change; certify exit 1 again after a recopy until re-verified; corrupted dest → verify exit 1 `MISMATCH`, certify refuses naming `"mismatch"`; deleted dest → verify exit 1 `Missing: 1`.
   - `.vault-partial` gone after short write, cancelled copy, missing source. `certify.Build` refuses on each of `copied|mismatch|deduped|inventoried` without creating a key.
   - **Mutation check:** reverting the predicate to `failed > 0 || orphaned > 0` fails 3 tests (`TestCopyExitStatus/unresolved_collision…`, `…/rename-mtime-year_still_blocked…`, `TestCollisionRowsAndExitThroughMain`). Restored; the committed tree is the passing one.
2. `bec13cc` **B5** - `CLAUDE.md` (89 lines): build/test, package map, status vocabulary, exit-code table, announce-what-you-skipped, hard rules, branch/review/deploy.
3. `14f4e2c` **B4** - all five `scripts/*.sh`: `IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.0}"` with the "v0.2.0 does not exist until the PM tags it" comment; `docs/release.md` (5 numbered lines); CLAUDE.md deploy line updated in the same commit. `bash -n` clean on all five; the IMG line evaluates to the default with `VAULT_IMAGE` unset and to the override when set.

Evidence (Mac mini, `2026-09-16T12:17:44-07:00`, go1.27.0 darwin/arm64):
```
$ go vet ./...
(clean)
$ go test ./... -count=1
ok  	github.com/eddyvarelae/media-vault/cmd/vault	0.498s
ok  	github.com/eddyvarelae/media-vault/internal/certify	0.479s
ok  	github.com/eddyvarelae/media-vault/internal/copy	0.619s
?   	github.com/eddyvarelae/media-vault/internal/dedup	[no test files]
?   	github.com/eddyvarelae/media-vault/internal/importer	[no test files]
?   	github.com/eddyvarelae/media-vault/internal/inventory	[no test files]
?   	github.com/eddyvarelae/media-vault/internal/manifest	[no test files]
?   	github.com/eddyvarelae/media-vault/internal/move	[no test files]
ok  	github.com/eddyvarelae/media-vault/internal/scan	0.780s
?   	github.com/eddyvarelae/media-vault/internal/verify	[no test files]
$ CGO_ENABLED=0 go test ./... -count=1     # same 4 ok, constraint holds
```

Noticed, not acted on (out of rev-1 scope - PM to triage):
- `gofmt -l` flags three pre-existing files: `internal/certify/certify.go`, `internal/importer/importer.go`, `internal/inventory/inventory.go`. Whitespace only; I left them so the diff stays reviewable. One-line item for any PR.
- `docs/install/ugos.md` lines 23/36 still tell a UGOS-UI installer to use tag `latest`. Doc-only; same spirit as B4. Not in the five-scripts scope, so untouched.
- `main`'s `defer m.Close()` is skipped whenever a command exits non-zero (`os.Exit` bypasses defers) - pre-existing, unchanged by the refactor, harmless with WAL. Mention only so nobody reads it as new.
- `internal/manifest` and `internal/verify` have no tests; `verify` is frozen, `manifest` is covered indirectly by every round-trip. B9 territory.

**PM (2026-09-17):** Rev 1 accepted at `tested`. PM re-ran `go vet` + `go test ./... -count=1` at `a2da6a3`: 4 packages ok - your evidence reproduces. Staged as review #2; Codex is on it. Do not touch `tests-and-pinning` until the verdict lands (frozen). Your four "noticed" items are triaged: gofmt → B15; ugos.md `latest` → folded into B16; `defer m.Close()` skipped on `os.Exit` → noted in BACKLOG as no-action; manifest/verify tests → B9 (after F4 lands on `main`). F4 is Reviewer-APPROVED (#1); its merge to `main` is waiting on the human, so B9 is not yet startable. Rev 2 below is what you can do now.

## Questions

(none yet)

## Superseded

(none)
