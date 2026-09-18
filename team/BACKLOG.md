# Backlog

Maintained by the PM - ordering and scope are theirs alone. Fixed sections below: the PM re-sorts at triage, done items move to Done promptly, sections never fork by date, and one fact lives in one item (duplicates get struck at triage). Executors check items off with a dated note + evidence. Propose new items in your channel, never here directly. Status: `[ ]` open · `[x]` done (with evidence).

## P0 - blockers

- [ ] **B2** NAS reachability for the Tester. Two halves: **(a)** read path via SMB - `ACTION (human):` set `NAS_SHARES="media docker"` in mini-server's `config/mini.env` so `~/mounts/docker` mounts (manifest snapshot + logs become readable, no SSH needed); **(b)** SSH on the UGREEN - nobody has ever had it (mini-server PM, 2026-09-17); needed only to *run* `vault` on the NAS. `ACTION (human):` is SSH enabled in UGOS (Control Panel → Terminal)? If not, say how you run the `nas-*.sh` scripts today.

## P1

- [ ] **B3** *(#2 FINDINGS 2026-09-17 - five items back to Dev, rev 3)* Test harness: `go test ./...` covering scan → copy → verify → certify end-to-end on temp dirs + a temp manifest, and the F3 "done when" list as tests. Owner: Dev (work order rev 1). Evidence rung expected: `tested`.
- [ ] **B4** *(in #2)* Pin the NAS scripts to a release tag instead of `:latest`; PM cuts `v0.2.0` once B1 + B4 are on `main`. Owner: Dev (rev 1) → PM tags.
- [ ] **B5** *(in #2)* `CLAUDE.md` conventions doc for the repo. Owner: Dev (rev 1), same PR as B3.
- [ ] **B6** Incremental verify on the NAS for all six `media-*` disks with `--only-unverified`, then full `certify`. Needs B1 deployed (B4 tag). Owner: human runs, Tester witnesses. Evidence: logs under `/volume1/docker/`.
- [ ] **B7** Source SSDs - per mini-server's records (re-verify): **`tars`** (attached to the Mini, never content-checked; path scan 9,339 vs content 186), **`kipp`** (never seen), **one unnamed** (never seen), **`case`** (certified, cleared to wipe, not wiped). Tester: confirm each against the manifest and report status counts; note whether Scratch1 was ever a source. Needs B2(a).
- [ ] **B8** `scan --dedupe-content` pass over the archive once B6 has promoted rows to `verified` (dedupe matches only `verified` rows). Owner: human runs, Tester witnesses.
- [ ] **B9** F4 tests (the F4 "done when" list) - lands after B1 merges, on a fresh branch. Owner: Dev (rev 2).

- [ ] **B15** `gofmt` the three pre-existing unformatted files (`certify.go`, `importer.go`, `inventory.go`). Dev rev 2.
- [ ] **B16** CI builds only on `v*` tags + manual dispatch; no `:latest`; `ugos.md` stops saying `latest`. Makes "a merge is not a deploy" true at the source. Dev rev 2.

- [ ] **B17** Nightly video tagger → this repo. Take `scripts/run-tagging.sh` + `scripts/tagging-helper.py` from mini-server `d9d677d` (tested) into `scripts/tagging/`; evaluate the WIP `5e7bac7` (newest-first, two tiers, NAS manifest snapshot, `Public` walk) against the batch rules in DECISIONS; fix the stale-manifest read (must snapshot the NAS manifest from `~/mounts/docker/vault-nas-config/`, never open WAL sqlite over SMB). Dev rev 4, after rev 3. Needs B2(a) to test for real.
- [ ] **B18** Install the tagger LaunchAgent (`launchd/com.varela.video-tagger.plist.example`, 02:00 daily) once B17 is `observed` by the Tester on a real 3-file run from this repo. One act, evidence in channel. Owner: PM (human-approved) or mini-server on request.
- [ ] **B19** `Public` has 32 video files on disk and zero manifest rows - inventory it (`vault inventory media-public ...`) so manifest-driven selection can reach it. Human runs (writes the NAS manifest), Tester witnesses.
- [ ] **B20** Test residue as fixture: `GoPro/Videos/GX010007/8/12/14.MP4`, `GX010021 copy.MP4` carry Finder tags + `com.videotagger.processed` + `reports/<stem>/`; state-db rows 240010-240014. Tester documents them as the tagger's known-good check. Stays as is (Eddy).

## P2
- [ ] **B21** 152 probable duplicates (~0.09 TB, DJIFlip/GoPro broken-clock names); `vault dedup` exists. Deletion is Eddy's call - PM to stage the list with hashes for a decision after B6.
- [ ] **B22** `run-backup.sh`, report-only (see DECISIONS 2026-09-15): on attach of a known SSD, content gap check → report, copy nothing. Template `launchd/com.varela.media-backup.plist.example`, gap tool `scripts/archive-gap.py` in mini-server. After B17.

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

- [x] **B1** F4 `verify --only-unverified` - Reviewer APPROVE #1 (Codex, 2026-09-17), merged `--no-ff` as `7cca025` on 2026-09-17; `go build && go vet` clean on the merge (PM). Rung: `tested` (author) + independent source review; `observed` pending the first NAS run (B6).

- [x] **F3** `copy` exits 1 when a collision leaves a file unarchived - `9518230` on `main`, pushed. Evidence: `tested` (author, pre-framework); `go build && go vet` clean on 2026-09-16 (PM). Not independently reviewed under the framework - predates it.
- [x] **F1/F2, content-dedupe** - merged `44fbf67..d0163a9` on 2026-09-15 (pre-framework). Evidence: handoff notes in `team/archive/`.
