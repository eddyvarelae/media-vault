# dev-questions.md - PM <-> Dev

Protocol: the PM's current WORK ORDER lives at the top (newest supersedes; work it top-down). Questions and answers below it, inline, newest question first. Every note dated and signed: **Role (YYYY-MM-DD):**.

Boot line (Mac mini, visible terminal, Opus-class, Remote Control on): `cd ~/Projects/media-vault-dev && claude --model opus --remote-control media-vault-dev "You are the Dev for media-vault. Read team/TEAM.md, team/actors/dev.md, then team/channels/dev-questions.md."`

## WORK ORDER - rev 1

**PM (2026-09-16):** You work in your own worktree, `~/Projects/media-vault-dev` (branch `tests-and-pinning`, created by the PM off `main`) - never in `~/Projects/media-vault`, which is the PM's checkout on `main`. `team/` files are shared through git: commit your channel notes on your branch and the PM merges them; if you need a note seen immediately, also write it into `~/Projects/media-vault/team/channels/dev-questions.md` (same file, PM's checkout). **Do not touch `verify-incremental`** - it is under Reviewer review (request #1). Three items, one PR, in this order:

1. **B3 - test harness.** `go test ./...` must exercise the real pipeline on a temp dir + temp manifest (`t.TempDir()`, no NAS, no network): scan → copy → verify → certify round-trip; recopy on changed size/mtime; `.vault-partial` never survives a failed copy; `certify` refuses on any non-`verified` row; and the F3 "done when" list (`team/archive/2026-09-15-handoff-f3-f4.md` § F3) as explicit cases - unresolved collision → exit 1 with `collisions` named in `INCOMPLETE:`; every-rename-succeeds → exit 0; dry-run / no-op → exit 0. `runCopy` calls `os.Exit` - refactor the minimum needed to make the exit decision testable (e.g. return a status), no framework, no behavior change. Also: tests must not reach `/volume1` or `/mnt` even if those exist on the host.
2. **B5 - `CLAUDE.md`** at repo root: how to build/test, the package map, the manifest status vocabulary, the exit-code contract, the "announce what you skipped" rule, and the branch/review policy from `team/TEAM.md`. Short; it is the contract external agents read.
3. **B4 - pin the image tag.** All five `scripts/*.sh` take `IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.0}"` (one variable each, env-overridable). `v0.2.0` will not exist until the PM tags it after this PR and F4 merge - say so in a comment. Add a `docs/release.md` (5 lines: tag on `main`, CI publishes semver, bump the scripts' default in the same PR as a release).

**Evidence expected:** paste `go test ./... -count=1` output and `go vet ./...` in this channel under your rev-1 note; state the rung (`tested` is the ceiling for you). When done, write "READY FOR REVIEW" with the branch tip SHA; the PM stages it in `review-requests.md`. Do not merge.

**Out of scope for rev 1:** F4 tests (B9 - after #1 merges), anything under `internal/verify` (frozen until #1 resolves), README roadmap items.

Flag in-progress local work at the top of your first note so the Tester knows your worktree is live.

## Dev notes

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

## Questions

(none yet)

## Superseded

(none)
