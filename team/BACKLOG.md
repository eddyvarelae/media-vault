# Backlog

Maintained by the PM - ordering and scope are theirs alone. Fixed sections below: the PM re-sorts at triage, done items move to Done promptly, sections never fork by date, and one fact lives in one item (duplicates get struck at triage). Executors check items off with a dated note + evidence. Propose new items in your channel, never here directly. Status: `[ ]` open · `[x]` done (with evidence).

## P0 - blockers

- [ ] **B23** **Data loss: 2,668 SonyA6700 photos (51.1 GB) overwritten 2026-09-01** (Tester #5, #14, #17). **(a) recovery** - searched and absent from all five attached SSDs (`tars`, `kipp`, `case`, `Eddy's Media Vault`, `Scratch1`); `case` is the confirmed overwrite source; `noahsarc` = `Scratch1`, device-erased 2026-09-02 17:58 (mini-server `SETUP.md:129`), 36 h after the overwrite; the 2026-04-26 source disk (21,986 rows, 16:06-17:47 MDT, no log) is **unidentified** - the only remaining candidates are that disk and unformatted A6700 cards. Waiting on Eddy. Nothing is wiped until this closes. **(b) defect** - decided 2026-09-17: a `verified` destination is never overwritten; same `(source_disk, source_path)` + different content = collision (rename under the flag, else skip + exit 1). Dev rev 4 item 1. Owner: Eddy (a), Dev (b).
- [ ] **B24** 195 `media-sonya6700` rows have `dest_path` missing `CLIP/`/`DCIM/` (Tester #7; bytes verified 195/195). One-off manifest fix, snapshot first, PM-approved, before B6 can pass. Dev rev 4 item 2.
- [ ] **B2** *(half done)* `docker` share mounted; SSH port open, **key refused** → `ACTION (human):` `ssh-copy-id figmaboi@192.168.1.167`. Then Tester: `id`, `sudo -n true`, `docker ps`.

## P1

- [ ] **B3** *(#2 FINDINGS 2026-09-17 - five items back to Dev, rev 3)* Test harness: `go test ./...` covering scan → copy → verify → certify end-to-end on temp dirs + a temp manifest, and the F3 "done when" list as tests. Owner: Dev (work order rev 1). Evidence rung expected: `tested`.
- [ ] **B4** *(in #2)* Pin the NAS scripts to a release tag instead of `:latest`; PM cuts `v0.2.0` once B1 + B4 are on `main`. Owner: Dev (rev 1) → PM tags.
- [ ] **B5** *(in #2)* `CLAUDE.md` conventions doc for the repo. Owner: Dev (rev 1), same PR as B3.
- [ ] **B6** *(blocked by B24, B23)* Incremental verify on the NAS for all six `media-*` disks with `--only-unverified`, then full `certify`. Needs B1 deployed (B4 tag). Owner: human runs, Tester witnesses. Evidence: logs under `/volume1/docker/`.
- [ ] **B26** `kipp` (Auditorium/Backup/GoPro/LeanTank/Multicam/SonyA6700/SonyZVE10) appears in no copy log - never archived. Plan its copy after B23/B24 (it may also hold the lost 2,668).
- [ ] **B8** `scan --dedupe-content` pass once B6 passes. Tester #12: 3,193 sha groups with >1 row, 3,418 surplus rows already inside the manifest; renames = 1,154 (djiflip 401, gopro 558, sonya6700 195).
- [ ] **B9** F4 tests (the F4 "done when" list) - lands after B1 merges, on a fresh branch. Owner: Dev (rev 2).

- [ ] **B15** `gofmt` the three pre-existing unformatted files (`certify.go`, `importer.go`, `inventory.go`). Dev rev 2.
- [ ] **B16** CI builds only on `v*` tags + manual dispatch; no `:latest`; `ugos.md` stops saying `latest`. Makes "a merge is not a deploy" true at the source. Dev rev 2.

- [ ] **B17** Nightly video tagger → this repo. Take `scripts/run-tagging.sh` + `scripts/tagging-helper.py` from mini-server `d9d677d` (tested) into `scripts/tagging/`; evaluate the WIP `5e7bac7` (newest-first, two tiers, NAS manifest snapshot, `Public` walk) against the batch rules in DECISIONS; fix the stale-manifest read (must snapshot the NAS manifest from `~/mounts/docker/vault-nas-config/`, never open WAL sqlite over SMB). Dev rev 4, after rev 3. Needs B2(a) to test for real.
- [ ] **B18** Install the tagger LaunchAgent (`launchd/com.varela.video-tagger.plist.example`, 02:00 daily) once B17 is `observed` by the Tester on a real 3-file run from this repo. One act, evidence in channel. Owner: PM (human-approved) or mini-server on request.
- [ ] **B19** `Public` has 32 video files on disk and zero manifest rows - inventory it (`vault inventory media-public ...`) so manifest-driven selection can reach it. Human runs (writes the NAS manifest), Tester witnesses.
- [ ] **B20** Tagger residue: GoPro (5 clips, `reports/` 16 files) **and iPhone** (`reports/` 15 dirs, 60 files; 15 `metadata` rows) - none in the manifest, so `scan` will find them. Decision needed: inventory `reports/` or exclude it. Tester documents as fixture.

- [ ] **B25** Certificates out of the trees they certify: `certify` refuses an output path under the dest root; stale `media-sonya6700.cert.json` (2026-04-29) removed once B23/B24 resolve. Dev rev 4.
- [ ] **B27** `nas-verify-certify-all.sh` writes every log line twice (`tee -a` under a redirecting nohup). Dev, any PR.

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

- [x] **B7** Source SSDs mapped from the logs (Tester #6, 2026-09-17): `tars` (Apr 27), `noahsarc` (Apr 28), `case` (Sep 1), `Eddy's Media Vault` (Sep 2, the 195 rows); `kipp` never copied → B26. `source_disk` is per camera, not per SSD.

- [x] **B1** F4 `verify --only-unverified` - Reviewer APPROVE #1 (Codex, 2026-09-17), merged `--no-ff` as `7cca025` on 2026-09-17; `go build && go vet` clean on the merge (PM). Rung: `tested` (author) + independent source review; `observed` pending the first NAS run (B6).

- [x] **F3** `copy` exits 1 when a collision leaves a file unarchived - `9518230` on `main`, pushed. Evidence: `tested` (author, pre-framework); `go build && go vet` clean on 2026-09-16 (PM). Not independently reviewed under the framework - predates it.
- [x] **F1/F2, content-dedupe** - merged `44fbf67..d0163a9` on 2026-09-15 (pre-framework). Evidence: handoff notes in `team/archive/`.
