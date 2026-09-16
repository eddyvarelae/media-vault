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

## Questions

(none yet)

## Superseded

(none)
