# Backlog

Maintained by the PM - ordering and scope are theirs alone. Fixed sections below: the PM re-sorts at triage, done items move to Done promptly, sections never fork by date, and one fact lives in one item (duplicates get struck at triage). Executors check items off with a dated note + evidence. Propose new items in your channel, never here directly. Status: `[ ]` open · `[x]` done (with evidence).

## P0 - blockers

- [ ] **B1** F4 `verify --only-unverified` (`verify-incremental` @ `f964b60`) - **Reviewer APPROVE 2026-09-17**; merge to `main` blocked for the PM's session → `ACTION (human):` merge. Unblocks B6, B8, B9.
- [ ] **B2** NAS reachability from the Mac mini for the Tester - `ACTION (human)`, see `tester-feedback.md`. Blocks every NAS-side item.

## P1

- [ ] **B3** *(READY FOR REVIEW #2 @ `14f4e2c`, PM-verified green)* Test harness: `go test ./...` covering scan → copy → verify → certify end-to-end on temp dirs + a temp manifest, and the F3 "done when" list as tests. Owner: Dev (work order rev 1). Evidence rung expected: `tested`.
- [ ] **B4** *(in #2)* Pin the NAS scripts to a release tag instead of `:latest`; PM cuts `v0.2.0` once B1 + B4 are on `main`. Owner: Dev (rev 1) → PM tags.
- [ ] **B5** *(in #2)* `CLAUDE.md` conventions doc for the repo. Owner: Dev (rev 1), same PR as B3.
- [ ] **B6** Incremental verify on the NAS for all six `media-*` disks with `--only-unverified`, then full `certify`. Needs B1 deployed (B4 tag). Owner: human runs, Tester witnesses. Evidence: logs under `/volume1/docker/`.
- [ ] **B7** Enumerate the 4 source SSDs and each one's manifest coverage (rows per `source_disk`, status counts); note whether the repurposed "Scratch1" SSD was ever certified. Owner: Tester (first order). Needs B2.
- [ ] **B8** `scan --dedupe-content` pass over the archive once B6 has promoted rows to `verified` (dedupe matches only `verified` rows). Owner: human runs, Tester witnesses.
- [ ] **B9** F4 tests (the F4 "done when" list) - lands after B1 merges, on a fresh branch. Owner: Dev (rev 2).

- [ ] **B15** `gofmt` the three pre-existing unformatted files (`certify.go`, `importer.go`, `inventory.go`). Dev rev 2.
- [ ] **B16** CI builds only on `v*` tags + manual dispatch; no `:latest`; `ugos.md` stops saying `latest`. Makes "a merge is not a deploy" true at the source. Dev rev 2.

## P2

- [ ] **B10** Scrub schedule: re-verify rows older than N days (from the F4 design notes - a different feature, kept out of F4 on purpose).
- [ ] **B11** HTML rendering of the certificate (README roadmap).
- [ ] **B12** Web UI, mobile-first PWA (README roadmap).
- [ ] **B13** UGOS / Synology / QNAP launcher integration; Tailscale remote-access docs (README roadmap).
- [ ] **B14** README "How it works" still says verify/certify are "coming next" - stale since v0.1. Dev rev 2.

## Deferred (decided, don't build now)

- `defer m.Close()` in `main` is skipped when a command exits non-zero (`os.Exit` bypasses defers) - pre-existing, harmless under WAL; no action (Dev noticed 2026-09-16).

- Fail-on-collision only when the collided content is archived elsewhere - **rejected 2026-09-15** (exit code must not depend on `--dedupe-content`; the missing row is the gap).
- Allow-list of statuses for `--only-unverified` - rejected in favor of `!= 'verified'`, no maintenance when statuses are added.
- Designer / Cloud seats - no standing work for them.

## Done (PM-verified)

- [x] **F3** `copy` exits 1 when a collision leaves a file unarchived - `9518230` on `main`, pushed. Evidence: `tested` (author, pre-framework); `go build && go vet` clean on 2026-09-16 (PM). Not independently reviewed under the framework - predates it.
- [x] **F1/F2, content-dedupe** - merged `44fbf67..d0163a9` on 2026-09-15 (pre-framework). Evidence: handoff notes in `team/archive/`.
