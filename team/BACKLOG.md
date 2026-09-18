# Backlog

Maintained by the PM - ordering and scope are theirs alone. Fixed sections below: the PM re-sorts at triage, done items move to Done promptly, sections never fork by date, and one fact lives in one item (duplicates get struck at triage). Executors check items off with a dated note + evidence. Propose new items in your channel, never here directly. Status: `[ ]` open · `[x]` done (with evidence).

## P0 - blockers

- [ ] **B23** **Data loss: 2,668 SonyA6700 photos overwritten 2026-09-01.** (a) recovery **closed 2026-09-17, LOST** (all disks searched; `noahsarc` erased; Apr 26 source unknown; list `team/archive/2026-09-01-sonya6700-lost-2668.tsv`). (b) defect **done**: `overwrite-guard` merged `314416d`, `v0.2.1` (Reviewer #5→#11) - a `verified` destination is never overwritten: physical-path ownership, `O_EXCL` staging, symlink components refused. Item stays open only until the stale April cert is gone (B25).
- [ ] **B40** **Corrupt `verified` file on the NAS: `SonyA6700/DCIM/DSC04868_2025.JPG` is 3 MiB of image + 4,139,319 zero bytes; manifest row (`media-sonya6700`, empty `dest_path`, `verified`, in the Sep 13 cert) attests the corrupt bytes** (Tester #24, 2026-09-17). Intact original on `Eddy's Media Vault` (`Backups/SonyA6700/DCIM/DSC04868.JPG`, sha `b7ecf808…`). Recovery needs a deliberate path that the v0.2.1 guard does not forbid by accident: Dev builds `vault restore <disk> <source-file> <dest-root>` (or equivalent) that replaces a named destination **only** when given an explicit row + a source whose sha is stated up front, rewrites that row's sha/size/status → `copied`, logs both hashes; then PM-logged act, Tester verifies. P0, Dev rev 5 item 2.
- [ ] **B38** `verify`/`certify` cannot detect a torn write that was hashed after the fact (Tester #24): a hash of a truncated file is still a hash. **Archive-wide tail scan done (Tester #25, 2026-09-17): exactly one torn file (B40); 144 other hits all camera-native padding, source-identical.** Add a content-plausibility pass per type (JPEG EOI then optional zero padding, DJI 4 KiB padding, Sony fixed-size ARW, `.RSV` reserve files, zero-length SRT twins) as `vault audit`, report-only, seeded from `tailscan-join.py`; and `copy` must never create a row whose bytes were not compared to a source (the 605 rows of B39 were). Dev, after B40.
- [ ] **B39** 605 `media-sonya6700` rows have an empty `dest_path`, all `_20xx`-named, `copied_at` 2026-04-28 - rows created by scanning the NAS tree itself, not by a copy (Tester #24). Tester #25: the Apr 26 rows' `source_path` already has the NAS layout (`Videos/…`) - that run was an import of files already on the NAS. Question to Eddy: what ran on the evening of Apr 27/28? Tester's 605-file tail check: 1 zero-tailed (B40), 604 intact. Repair of the rows (fill `dest_path`) after B24's tool, same shape.
- [ ] **B24** 195 `media-sonya6700` rows have `dest_path` missing `CLIP/`/`DCIM/` (Tester #7; bytes verified 195/195). **Tool merged** (`88d75d7`, Reviewer #15) and **published as `v0.2.2`**. **Dry-run on a snapshot passed** (Tester #26: 195 REPAIR, 0 other, 195/195 hash-located). Acceptance review #23 → Eddy names the executor → live run per `team/context/runbook-b24.md` → verify → witness.

## P1

- [ ] **B6** *(blocked by B24, B23)* Incremental verify on the NAS for all six `media-*` disks with `--only-unverified`, then full `certify`. `v0.2.0` is tagged (2026-09-17); runbook facts: `sudo docker run` as `figmaboi`, NAS clock UTC-6. Owner: human runs, Tester witnesses. Evidence: logs under `/volume1/docker/`.
- [ ] **B29** **Gap report per attached SSD** (decided 2026-09-17: the Mini is read-only; copies run on the NAS with the SSD plugged in there). For each of `tars`, `kipp`, `case`, `Eddy's Media Vault`: files on the SSD whose content is not in the manifest (name+size index, sha256 on candidates), with counts and bytes → "needs archiving: yes/no, N files, X GB". First pass by the Tester by hand from the 2026-09-17 snapshot (Tester rev 2 item 6): `kipp` done (#19, Reviewer #8); `tars` done (#20, Reviewer #14 APPROVE); `case` done (#21: nothing to archive, all 5,141 on verified rows; Reviewer #14); `Eddy's Media Vault` in progress (34,717 files). The durable tool is B22. Output tells Eddy which SSD to hook to the NAS next.
- [ ] **B41** `Eddy's Media Vault`: 1 file needs archiving (the intact `DSC04868.JPG`, B40) - Tester #22, Reviewer #20 pending; everything else on it is archived by content (195 via the B24 `copied` rows). Four-disk roll-up: 14,913 files / 3.11 TB to archive.
- [ ] **B36** `tars` **needs archiving: 5,040 files / 1,188,169,289,959 B (1.19 TB)** - Tester #20, Reviewer-recomputed #14 (2026-09-17). Reused as a camera dump disk after the Apr 27 archive run (gap files dated Apr 24 → Jul 20). Folders: SonyA6700 4,775 / 1,003.8 GB, SonyZVE10 240 / 130.0 GB, GoPro 25 / 54.4 GB. Same rule as B26: copy after v0.2.1, on the NAS, PM runbook.
- [ ] **B26** `kipp` **needs archiving: 9,872 files / 1,921,695,784,449 B (1.92 TB)** - Tester #19, Reviewer-recomputed #8 (2026-09-17). Archived already: 521 files (LeanTank, via `case`). Folders: SonyA6700 5,978 / 582.5 GB, Multicam 34 / 426.2 GB, Auditorium 10 / 336.2 GB, Backup 3,648 / 260.0 GB, GoPro 184 / 230.9 GB, SonyZVE10 18 / 85.9 GB. 13 files share name+size with archived files but differ in content (the B23 trap). **v0.2.1 is out; runbook `team/context/runbook-kipp.md` (draft)**: Tester steps 1-2, Dev script (rev 5 item 1, #18), dry-run acceptance vs the gap numbers, Eddy launches the copy, the 13 collisions as a second pass.
- [ ] **B8** `scan --dedupe-content` pass once B6 passes. Tester #12: 3,193 sha groups with >1 row, 3,418 surplus rows already inside the manifest; renames = 1,154 (djiflip 401, gopro 558, sonya6700 195).
- [ ] **B9** F4 tests (the F4 "done when" list) - lands after B1 merges, on a fresh branch. Owner: Dev (rev 2).

- [ ] **B17** Nightly video tagger → this repo. Take `scripts/run-tagging.sh` + `scripts/tagging-helper.py` from mini-server `d9d677d` (tested) into `scripts/tagging/`; evaluate the WIP `5e7bac7` (newest-first, two tiers, NAS manifest snapshot, `Public` walk) against the batch rules in DECISIONS; fix the stale-manifest read (must snapshot the NAS manifest from `~/mounts/docker/vault-nas-config/`, never open WAL sqlite over SMB). **Dev rev 5 item 3, shape approved 2026-09-17 22:44** (WIP `5e7bac7` as base; machine facts from `mini.env`, job policy defaults in `scripts/tagging/run-tagging.sh`; per-run manifest snapshot with `quick_check`; `verified` rows only; bash harness under `go test ./scripts/test/`). Branch `tagger` → **built `6b061f3`, review #26 queued** (Codex 02:38). B18 gate after merge: Tester's 3-file real run.
- [ ] **B18** Install the tagger LaunchAgent (`launchd/com.varela.video-tagger.plist.example`, 02:00 daily) once B17 is `observed` by the Tester on a real 3-file run from this repo. One act, evidence in channel. Owner: PM (human-approved) or mini-server on request.
- [ ] **B19** `Public` has 32 video files on disk and zero manifest rows - inventory it (`vault inventory media-public ...`) so manifest-driven selection can reach it. Human runs (writes the NAS manifest), Tester witnesses.
- [ ] **B20** Tagger residue: GoPro (5 clips, `reports/` 16 files) **and iPhone** (`reports/` 15 dirs, 60 files; 15 `metadata` rows). **Decided 2026-09-17: `scan` skips `reports/` directories at any depth.** Dev rev 4 item 6 (test on temp dirs). Tester documents as fixture.

- [ ] **B25** Certificates out of the trees they certify: `certify` refuses an output path under the dest root; stale `media-sonya6700.cert.json` (2026-04-29) removed once B23/B24 resolve. Dev rev 4.
- [ ] **B28** `figmaboi` has NOPASSWD `sudo bash`/`nohup`/`docker` on the NAS (Tester #18) - a password-less root shell for the only agent-reachable account. Eddy's call: leave (UGOS default) or restrict to `docker` only. Security, not product.
- [ ] **B30** *(folded into B23(b) by review #5 finding 1)* a `deduped` row recopy or a colliding `.vault-partial` name can still write over a `verified` destination - the guard must be destination-level. Dev item 1 fixes.
- [ ] **B33** `repair-dest`: a candidate that is already another row's `dest_path` should be a fourth outcome `OWNED`, never chosen (Dev noticed 2026-09-17; does not arise on the 195). Dev, with the next `repair-dest` change.
- [ ] **B35** `nas-tars-copy-all.sh` has the same `tee` double-logging pattern as B27 (Dev noticed 2026-09-17). One-line follow-up with the B27 helper. Dev, next small PR.
- [ ] **B34** `ParseRules` accepts `..` in a rule's subdir, so `--rule` can route writes outside the destination root (Dev noticed 2026-09-17; the guard now sees through it but the rule itself is a foot-gun). Refuse `..` components in `--rule` for `scan`/`copy`/`move`. Dev, one line + test, next small PR.
- [ ] **B37** `scripts/*.sh` default image tag → `v0.2.1` (release rule in `docs/release.md`); until then runbooks pass `VAULT_IMAGE` explicitly. Dev, in `certs-out`.
- [ ] **B42** `repair-dest` labels rows without a `dest_path` as `inventoried` in its summary line; the B39 rows are `verified` (Tester #26). Wording only. Dev, next touch of `internal/repair`.
- [ ] **B43** `certify.WriteOutput` binds the leaf but not the parent directory between check and write (Dev, review #16 fix); binding needs `openat` = `os.Root` (Go 1.24, `Rename` 1.25) - a toolchain bump PR (`go.mod`, `golang:1.23-alpine` → 1.25). Mitigation today: `/volume1/docker/vault-certs` is root-owned. P2.
- [ ] **B44** `move` exits 0 when it skips a verified-owned or symlinked destination (per-file `Skipped`); align with `copy`'s `INCOMPLETE` → exit 1 (Dev, review #27 note). P2.
- [ ] **B32** `move` writes destinations with its own collision handling and does not consult `VerifiedOwner` (Dev noticed 2026-09-17). Until it does, the never-overwrite rule is `copy`'s only. Dev, after rev 4 - or fold into B10-era scrub work.
- [ ] **B31** `--dry-run` still creates the config dir and initializes `manifest.db` before parsing flags (review #5 finding 2, pre-existing). Read-only open for dry-run. P2.
- [ ] **B27** `nas-verify-certify-all.sh` writes every log line twice (`tee -a` under a redirecting nohup). Dev, any PR.

## P2
- [ ] **B21** 152 probable duplicates (~0.09 TB, DJIFlip/GoPro broken-clock names); `vault dedup` exists. Deletion is Eddy's call - PM to stage the list with hashes for a decision after B6.
- [ ] **B22** `run-backup.sh`, report-only (see DECISIONS 2026-09-15): on attach of a known SSD, content gap check → report, copy nothing. **Shape approved 2026-09-17 23:08**: `vault gap <dir>` (Go, read-only, verified rows only, per-folder arithmetic) + `scripts/backup/run-backup.sh` (mini.env machine facts, snapshot first, allow-list `BACKUP_DISKS`, once per attach + per day, copies nothing, `go build` into state when stale). Branch `backup`, review #28.

- [ ] **B10** Scrub schedule: re-verify rows older than N days (from the F4 design notes - a different feature, kept out of F4 on purpose).
- [ ] **B11** HTML rendering of the certificate (README roadmap).
- [ ] **B12** Web UI, mobile-first PWA (README roadmap).
- [ ] **B13** UGOS / Synology / QNAP launcher integration; Tailscale remote-access docs (README roadmap).

## Deferred (decided, don't build now)

- A symlink introduced under the destination root *between* the writer's component walk and its `MkdirAll` can still redirect the write (Reviewer #11 caveat) - concurrent filesystem mutation, outside the static guarantee; not built against.
- Equal size + equal mtime + different bytes is not detected by `scan` - by design, scan is metadata-keyed and `verify` is the integrity pass (Reviewer #5 note, 2026-09-17).
- Same-size + new-mtime verified files are re-hashed on every scan (no row refresh) - accepted 2026-09-17: one read per touched file per run, no unasked manifest write; revisit only if a scan becomes slow (Dev noticed).
- `defer m.Close()` in `main` is skipped when a command exits non-zero (`os.Exit` bypasses defers) - pre-existing, harmless under WAL; no action (Dev noticed 2026-09-16).

- Fail-on-collision only when the collided content is archived elsewhere - **rejected 2026-09-15** (exit code must not depend on `--dedupe-content`; the missing row is the gap).
- Allow-list of statuses for `--only-unverified` - rejected in favor of `!= 'verified'`, no maintenance when statuses are added.
- Designer / Cloud seats - no standing work for them.

## Done (PM-verified)

- [x] **B2** NAS access: SMB shares `media`+`docker` mounted on the Mini; SSH as `figmaboi` with key (Tester #18, 2026-09-17); `sudo -n docker` NOPASSWD. `observed` by the Tester.
- [x] **B3/B4/B5/B14/B15/B16** tests-and-pinning merged `e4a4aed` (Reviewer APPROVE #4 after FINDINGS #2, #3), `v0.2.0` tagged 2026-09-17: test harness + NAS guard (`internal/testguard`), scripts pinned to `v0.2.0`, `CLAUDE.md`, CI on `v*` tags only with `latest=false`, gofmt, README present tense. Rung: `tested` (Dev) + PM re-run at `52d30b0` + Reviewer source trace; CI tag-only + no-`latest` **observed** on the real `v0.2.0` push (run 35298191638: tags `v0.2.0`, `sha-e4a4aed`, digest `fe3c2724…`).
- [x] **B7** Source SSDs mapped from the logs (Tester #6, 2026-09-17): `tars` (Apr 27), `noahsarc` (Apr 28), `case` (Sep 1), `Eddy's Media Vault` (Sep 2, the 195 rows); `kipp` never copied → B26. `source_disk` is per camera, not per SSD.

- [x] **B1** F4 `verify --only-unverified` - Reviewer APPROVE #1 (Codex, 2026-09-17), merged `--no-ff` as `7cca025` on 2026-09-17; `go build && go vet` clean on the merge (PM). Rung: `tested` (author) + independent source review; `observed` pending the first NAS run (B6).

- [x] **F3** `copy` exits 1 when a collision leaves a file unarchived - `9518230` on `main`, pushed. Evidence: `tested` (author, pre-framework); `go build && go vet` clean on 2026-09-16 (PM). Not independently reviewed under the framework - predates it.
- [x] **F1/F2, content-dedupe** - merged `44fbf67..d0163a9` on 2026-09-15 (pre-framework). Evidence: handoff notes in `team/archive/`.
