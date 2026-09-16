# Backlog

Maintained by the PM - ordering and scope are theirs alone. Fixed sections below: the PM re-sorts at triage, done items move to Done promptly, sections never fork by date, and one fact lives in one item (duplicates get struck at triage). Executors check items off with a dated note + evidence. Propose new items in your channel, never here directly. Status: `[ ]` open · `[x]` done (with evidence).

## P0 - blockers

- [ ] **B1** F4 `verify --only-unverified` (`verify-incremental` @ `f964b60`) - Reviewer verdict, then PM merges. Staged in `channels/review-requests.md` #1. Owner: Reviewer → PM. Unblocks B6, B8.
- [ ] **B2** NAS reachability from the Mac mini for the Tester - `ACTION (human)`, see `tester-feedback.md`. Blocks every NAS-side item.

## P1

- [ ] **B3** Test harness: `go test ./...` covering scan → copy → verify → certify end-to-end on temp dirs + a temp manifest, and the F3 "done when" list as tests. Owner: Dev (work order rev 1). Evidence rung expected: `tested`.
- [ ] **B4** Pin the NAS scripts to a release tag instead of `:latest`; PM cuts `v0.2.0` once B1 + B4 are on `main`. Owner: Dev (rev 1) → PM tags.
- [ ] **B5** `CLAUDE.md` conventions doc for the repo. Owner: Dev (rev 1), same PR as B3.
- [ ] **B6** Incremental verify on the NAS for all six `media-*` disks with `--only-unverified`, then full `certify`. Needs B1 deployed (B4 tag). Owner: human runs, Tester witnesses. Evidence: logs under `/volume1/docker/`.
- [ ] **B7** Enumerate the 4 source SSDs and each one's manifest coverage (rows per `source_disk`, status counts); note whether the repurposed "Scratch1" SSD was ever certified. Owner: Tester (first order). Needs B2.
- [ ] **B8** `scan --dedupe-content` pass over the archive once B6 has promoted rows to `verified` (dedupe matches only `verified` rows). Owner: human runs, Tester witnesses.
- [ ] **B9** F4 tests (the F4 "done when" list) - lands after B1 merges, on a fresh branch. Owner: Dev (rev 2).

## P2

- [ ] **B10** Scrub schedule: re-verify rows older than N days (from the F4 design notes - a different feature, kept out of F4 on purpose).
- [ ] **B11** HTML rendering of the certificate (README roadmap).
- [ ] **B12** Web UI, mobile-first PWA (README roadmap).
- [ ] **B13** UGOS / Synology / QNAP launcher integration; Tailscale remote-access docs (README roadmap).
- [ ] **B14** README "How it works" still says verify/certify are "coming next" - stale since v0.1. Doc fix, Dev, any PR.

## Deferred (decided, don't build now)

- Fail-on-collision only when the collided content is archived elsewhere - **rejected 2026-09-15** (exit code must not depend on `--dedupe-content`; the missing row is the gap).
- Allow-list of statuses for `--only-unverified` - rejected in favor of `!= 'verified'`, no maintenance when statuses are added.
- Designer / Cloud seats - no standing work for them.

## Done (PM-verified)

- [x] **F3** `copy` exits 1 when a collision leaves a file unarchived - `9518230` on `main`, pushed. Evidence: `tested` (author, pre-framework); `go build && go vet` clean on 2026-09-16 (PM). Not independently reviewed under the framework - predates it.
- [x] **F1/F2, content-dedupe** - merged `44fbf67..d0163a9` on 2026-09-15 (pre-framework). Evidence: handoff notes in `team/archive/`.
