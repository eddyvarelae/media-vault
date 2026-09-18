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

Where the system has live side effects: exactly one running instance, ever - the manifest is single-writer. Test artifacts are tagged and cleaned up same-day. **Seats share files only through git - never `cp` into another seat's worktree** (PM did, 2026-09-17; Tester #13).

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
- **Reviewer: a command, not a chat.** The PM (or the human) triggers the non-Claude CLI against `review-requests.md` (`codex exec … </dev/null` - invocation in `actors/reviewer.md`; the `</dev/null` is not optional); the verdict lands signed in the file.
- **Seat-to-seat messages go session-to-session** where the tooling allows (Claude Code: `ListAgents` → `SendMessage` by session name), never by typing into another terminal. Idle notices fire immediately when the target is already idle - ask seats to message the PM directly when a step is done instead. A message is delivery, not agreement: the file is still the record. *(media-vault, 2026-09-17: the PM's classifier blocked messaging other sessions; the Terminal paste + bare-Return nudge in the handoff is the fallback until that clears.)*

## Talking to the human (learned 2026-09-17)

The human reads in bursts, hours apart, often from a phone. **Every PM message starts with the day, date and time** (`Thu 2026-09-17 18:12`, from `date "+%a %Y-%m-%d %H:%M"`) **and leads with what the human must do** - a short list, or "nothing". Then the narrative, one timestamped line per event. When an earlier ACTION becomes moot, the next message says so explicitly ("the patch is dead - nothing to confirm"). A dozen untimestamped updates are unreadable; the human should never have to ask "what happened with X?" about something the PM already resolved.

## Machines that run things unattended (daemon-machine hygiene)

Learned on a Mac Mini running a scheduled-runner app; generalize to any always-on box:
- **One launcher.** A LaunchAgent *or* a login item - never both unless the app has a single-instance guard. Two launchers after a reboot = two schedulers.
- **Launchers get a bare `PATH`.** Anything the app spawns (`claude`, `python`, `codex`) must be resolved to an absolute path in code or configured explicitly - never found via the inherited environment.
- **OS auto-install of updates: off.** An aborted automatic restart quits the app and may never reboot; nothing relaunches it. Download automatically, install by hand.
- **Every unattended run has a wall-clock timeout** and fails loudly (status, log, notification). A hung child must never park work as "in progress" forever.
- **No build artifacts, `.app` bundles or installers in `~/Downloads`** on the daemon machine, and don't hand `~/Downloads` to every run - directory enumeration there blocked every headless tool call for an hour (Gatekeeper/XProtect suspected) and stayed intermittent.
- **Restart protocol.** Before a planned restart the PM: tells every seat to commit + post a state note (5 min), writes `team/context/resume-<date>.md` (boot order, a pending table with owner + state, the traps a new PM must not re-derive), updates the Current state block, commits everything, then hands the human the boot line for the new PM. Seats don't survive a restart; files do. *(media-vault keeps these under `team/archive/<date>-pm-handoff.md`.)*

## Path ownership (required before Dev's first commit)

| Role | Writable paths |
|---|---|
| PM | everything under `team/` except other roles' channel notes; release tags on `main` |
| Dev | `cmd/`, `internal/`, `scripts/`, `docs/`, `Dockerfile`, `.github/`, `go.mod`, `go.sum`, `README.md`, `CLAUDE.md`; its channel notes |
| Tester | `channels/tester-feedback.md`, `DECISIONS.md` entries, `docs/testing/` (external-agent interface, if ever needed) |
| Reviewer | `channels/review-requests.md` answers only |

Branch policy: Dev works in its own worktree (`~/Projects/media-vault-dev`) on a branch per work-order item, off the current `main` tip; the PM keeps `~/Projects/media-vault` on `main`. Nothing merges to `main` without an `APPROVE` in `review-requests.md`. The PM merges (fast-forward or `--no-ff`, never rewriting).

## Startup ritual (every session)

0. **Framework first (PM, every session, before reading anything else).** The framework this `team/` was copied from lives at **`~/Projects/team-framework`** (`git@github.com:eddyvarelae/team-framework.git`; clone it there if absent). Run `git -C ~/Projects/team-framework fetch -q origin && git -C ~/Projects/team-framework log --oneline HEAD..origin/main`. Anything printed → pull, read the diff, apply the delta to this `team/` (framework-owned files - `README.md`, `actors/*`, unused channel templates, `diagram.*` - are byte-copies; `TEAM.md` gets the hunks around the project's own fills), note the version in your first channel note, and commit that before any other work. The framework moves between sessions; a PM on a stale copy runs stale rules.
1. Read this file, then `team/actors/{your-role}.md`.
2. Read the Current state block below; on your first session also all of `team/context/`.
3. Read your channel's top note - that's your work order.
4. Skim `BACKLOG.md` and the `DECISIONS.md` tail.
5. Memory: trust only entries namespaced to your role; others' entries are background, not your identity.

## Current state (2026-09-18 09:06 - PM-verified, don't re-derive)

**Resume file: `team/context/resume-2026-09-18.md` (pending table, traps).** Resumed 2026-09-17 18:47 after the restart: PM, Dev (Terminal window 406) and Tester (407) rebooted; worktrees and both NAS mounts survived. Boot lines + in-flight table: `team/archive/2026-09-17-pm-handoff.md`. SSDs attached now: `tars`, `kipp`, `case`, `Eddy's Media Vault`, `Scratch1` - `noahsarc` is not.

- `main` tip = see `git log -1`; last code merges = `314416d` (overwrite-guard = `v0.2.1`), `6fbf20a` (kipp-script), `88d75d7` (repair-dest = **`v0.2.2`**, published), `0422b01` (certs-out) = `v0.2.3`, `4a028c8` (tagger), `b1277bd` (restore), **`12b5034`** (small-fixes). Next tag `v0.2.4` after `backup` lands. `copy` never overwrites a verified destination; `vault repair-dest` exists. Nothing on the NAS has pulled any image since v0.2.0; scripts default to `v0.2.0` on `main` until `certs-out` (B37) lands - runbooks pass `VAULT_IMAGE`.
- Dev (`~/Projects/media-vault-dev`): one branch left - `backup` (B22, review #50 pending). Then LaunchAgent installs are PM acts after the Tester's `observed` runs.
- Tester (`~/Projects/media-vault-tester`, detached): B29 done (Reviewer #8/#14/#20): to archive `kipp` 9,872 / 1.92 TB, `tars` 5,040 / 1.19 TB, EMV 1 file, `case` 0 = 14,913 / 3.11 TB. Found B40 (a certified torn file); archive-wide zero-tail scan running. B24 dry-run passed (#26) and is Reviewer-accepted (#23): live run per `team/context/runbook-b24.md` waits on Eddy naming the executor.
- NAS: DXP2800 `192.168.1.167`, SMB + SSH as `figmaboi` (key); `~/mounts/media` and `~/mounts/docker` mount via `com.varela.mount-nas`. NAS clock is UTC-6 (Mini is UTC-7). `sudo docker` is password-less; so is `sudo bash` (B28).
- Scope since 2026-09-17: every scheduled batch against the archive is ours (nightly tagger = B17, after rev 3).
- **P0s: 2,668 SonyA6700 photos overwritten Sep 1 are LOST (B23a closed; B23b fixed in v0.2.1). One certified NAS file is a torn write (B40, recovery path being built). 195 rows need the B24 repair before verify can pass.** Archive is 8.20 TiB (not 3.3). Agents never wipe or write an SSD.
- Source SSDs (B7 done): `tars`, `case`, `Eddy's Media Vault`, `kipp` (never copied, B26), and `Scratch1` = the former `noahsarc`, device-erased 2026-09-02 (Tester #17). The 2026-04-26 SonyA6700 source disk is unidentified. All five attached; agents read only.
- Reviewer = `codex exec … </dev/null`, run by the PM. Codex has a usage limit: hit 2026-09-17 20:21 (→21:34), 22:30 (→02:35), 2026-09-18 03:53 (→**07:38**); ~11-15 reviews per window; queue them, never two at once. Seats boot in visible Terminal windows with `--remote-control`; nudges need a trailing empty `do script`.
