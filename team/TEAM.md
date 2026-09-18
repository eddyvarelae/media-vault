# TEAM.md

You are one of the agent sessions building **media-vault** for Eddy. Cross-role messages are dated notes in repo files - never direct chat. The **PM drives the loop**: it reads every channel, routes notes, and boots/messages Dev seats where the tooling allows. The human decides, signs, and provides credentials - the human is not the message bus. The human may still act directly in any channel, signed as themselves.

All framework files live under `team/`; the product lives in the project root in whatever shape it takes. Any input the human hands you (screenshot, download, paste) gets copied into the repo where it belongs before you rely on it (`docs/sources/` for documents, `team/archive/` for prior hand-offs).

## Roles and models

| Role | Model | Does | Does NOT |
|---|---|---|---|
| **Human** | - | Decisions, credentials, purchases, sign-offs | Carry messages, triage raw channels |
| **PM** | Fable-class | Drives the loop, architecture, backlog order, work orders, triage, verifying others' claims, staging reviews; one consolidated `DECISIONS NEEDED` note per cycle | Write production code, ever; self-approve load-bearing claims |
| **Dev** (per seat) | Opus-class | Implements the backlog top-down, evidence on every check-off; executes design + infra under its rules | Pick work freely, relitigate settled calls |
| **Tester** *(booted 2026-09-16 - the product has live side effects on 3.3 TiB of production footage)* | Opus-class | Verifies committed states on the real system, daily liveness glance on the NAS, files feedback | Prioritize, implement, decide product, test a seat's live worktree |
| **Reviewer** | **Non-Claude, by design** - Codex CLI (`/opt/homebrew/bin/codex`) | Independent review of code diffs pre-merge and of numbers pre-deliverable, via `channels/review-requests.md` | Write anywhere else; approve on plausibility |
| **Designer** *(optional, unbooted)* | Opus-class | Visual/UX in its owned paths; proposals via `channels/design-questions.md` | Change behavior, data, or infra |
| **Cloud** *(optional, unbooted)* | Opus-class | Deploys, CI/CD, accounts, secrets, liveness; `channels/cloud-questions.md` | Change app behavior; mint credentials |

No more roles than needed: seats exist because the work demands them, not for symmetry. External agents (other projects' sessions) interact only through what the Tester documents for them - never team files, source, or backlog.

**All seats run on the Mac mini** (ethernet to the NAS). The MacBook Pro is not a seat host.

## Sacred - never touch

On the NAS (UGREEN DXP, UGOS):
- `/volume1/docker/vault-nas-config/` - the manifest (SQLite, WAL) and the Ed25519 signing key. Loss corrupts every certificate ever issued. Read-only queries only, never from a seat that also writes.
- `/volume1/media/<camera>/` - the live archive. Production data. Nothing writes here except a `vault copy` the human launched.
- `/mnt/@usb/` - the source SSDs. Mounted read-only into containers; never written, never wiped by an agent.
- `/volume1/docker/*.log` - the operational record of past runs. Append-only.

In the repo: `vault-config/` (gitignored local config), `.github/workflows/docker.yml` publishes to production on push to `main` - a merge is a deploy until B4 (tag pinning) lands.

Where the system has live side effects: exactly one running instance, ever - the manifest is single-writer. Test artifacts are tagged and cleaned up same-day.

## Isolation, deploys, and claims (learned the hard way - not optional)

1. **The Tester verifies committed states only**, from a detached checkout (`git worktree add --detach <dir> <commit>`), never a seat's live worktree. Seats flag in-progress local work in their channel top note.
2. **Deploy from the current main tip, always.** Rebase (or merge main) first; a deploy from a stale base can orphan other seats' shipped work in shared state. One deploy at a time, under a lock note in the deployer's channel. **Here, "deploy" = tagging a release that the NAS scripts pull, and any `vault` command run against the NAS manifest.**
3. **Heartbeat: long-running ops announce themselves.** Any operation over ~15 min (NAS verify passes run for hours) gets a channel note at start and finish, machine-timestamped (`date -Iseconds`). Lock held + silent past 30 min → the PM chases or reverts the lock. An operation with no channel note doesn't exist.
4. **Work orders freeze while under verification.** Scope changes create a new revision (rev N appended, old rev struck under Superseded) AND a same-time ping in `tester-feedback.md`. The Tester always names the rev it verified against.
5. **Numbers travel with their arithmetic.** Any figure headed for a human-facing deliverable (file counts, bytes, "safe to wipe") shows its computation inline and passes the Reviewer first. A number nobody can recompute is a finding, not a fact.
6. **Secrets never touch an echoing surface.** The signing key stays on the NAS; NAS credentials are never pasted into a command that prints them. Any exposure becomes a dated rotation item with an owner, same day.
7. **Author evidence caps at `tested`.** The `observed` and `witnessed` rungs require someone who didn't write the code - the Tester or the human.
8. **High-stakes acceptance goes through the Reviewer.** "This SSD is safe to wipe" is the highest-stakes claim this project makes. Before any wipe, the PM stages the certificate + the Tester's evidence in `review-requests.md`; the Reviewer answers: does this evidence prove every byte is on the NAS?

## Seat transports (how the PM runs the team)

**Every seat boots with Remote Control activated** so the human can reach and steer any seat from another machine. Activation happens in the seat's own session; the PM confirms it when it boots a seat, and a seat that cannot enable it says so in its channel rather than running unreachable.

Every seat writes to channel files regardless of how it runs. Defaults:
- **Long-lived seats (Dev, Tester): visible terminal windows** on the Mac mini. The PM opens them (`osascript` → Terminal running `claude "<boot line>"`) or hands the human the one-line boot.
- **Short fan-out tasks: internal subagents** (invisible, inside the PM's session, model-pinned). Fine for reads, checks, and drafts - never for anything touching the NAS.
- **Reviewer: a command, not a chat.** `codex` run against `channels/review-requests.md`; the verdict lands signed in the file.

## Path ownership (required before Dev's first commit)

| Role | Writable paths |
|---|---|
| PM | everything under `team/` except other roles' channel notes; release tags on `main` |
| Dev | `cmd/`, `internal/`, `scripts/`, `docs/`, `Dockerfile`, `.github/`, `go.mod`, `go.sum`, `README.md`, `CLAUDE.md`; its channel notes |
| Tester | `channels/tester-feedback.md`, `DECISIONS.md` entries, `docs/testing/` (external-agent interface, if ever needed) |
| Reviewer | `channels/review-requests.md` answers only |

Branch policy: Dev works in its own worktree (`~/Projects/media-vault-dev`) on a branch per work-order item, off the current `main` tip; the PM keeps `~/Projects/media-vault` on `main`. Nothing merges to `main` without an `APPROVE` in `review-requests.md`. The PM merges (fast-forward or `--no-ff`, never rewriting).

## Startup ritual (every session)

1. Read this file, then `team/actors/{your-role}.md`.
2. Read the Current state block below; on your first session also all of `team/context/`.
3. Read your channel's top note - that's your work order.
4. Skim `BACKLOG.md` and the `DECISIONS.md` tail.
5. Memory: trust only entries namespaced to your role; others' entries are background, not your identity.

## Current state (2026-09-17 18:30 - PM-verified, don't re-derive)

**Resuming after a restart? Read `team/archive/2026-09-17-pm-handoff.md` first - it has the boot lines and the in-flight table.**

- `main` tip = see `git log -1`; last code merge = `7cca025` (**F4 merged**, Reviewer APPROVE #1). Builds and vets clean. Zero test files on `main` until review #3 lands.
- `tests-and-pinning` (Dev's worktree `~/Projects/media-vault-dev`): review #2 = FINDINGS (5); Dev rev 3 in progress - its last channel note says where it stopped.
- Tester (`~/Projects/media-vault-tester`, detached): B2/B7 in progress from a manifest snapshot; last item says where it stopped.
- NAS: DXP2800 `192.168.1.167`, SMB `figmaboi`; `~/mounts/media` and `~/mounts/docker` mount via `com.varela.mount-nas`. SSH enabled 2026-09-17, details unconfirmed.
- Scope since 2026-09-17: every scheduled batch against the archive is ours (nightly tagger = B17, after rev 3).
- Source SSDs: `tars`, `kipp`, `case` (certified, unwiped), plus `noahsarc`? - Tester confirms (B7). A 5th is now "Scratch1".
- Reviewer = `codex exec`, run by the PM. Seats boot in visible Terminal windows with `--remote-control`; nudges need a trailing empty `do script`.
