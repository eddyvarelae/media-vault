# Decisions log (append-only)

Every decision the human makes - in any session, verbally or in writing - gets appended here BY THE AGENT WHO RECEIVED IT, before acting on it. No agent may assume the others heard it.

Format: `- YYYY-MM-DD · **decision** · context/why · received-by: {role}`

- 2026-09-15 · **`copy` fails the run on an unresolved collision even when `--dedupe-content` archived the bytes under another name** · the missing manifest row is itself the gap; exit code must not depend on an unrelated flag · received-by: (pre-framework, recorded in HANDOFF-f3-f4 by the then-reviewer; logged here by pm)
- 2026-09-15 · **Fail-on-collision only when the content is archived elsewhere: rejected** · same reasoning; do not relitigate · received-by: pm (from handoff)
- 2026-09-16 · **Adopt the team framework in media-vault; this session is the PM (Fable)** · `team/` copied from ~/Projects/team-framework · received-by: pm
- 2026-09-16 · **Reviewer = Codex CLI (`/opt/homebrew/bin/codex`), never a Claude session** · framework rule; the previous "MEV - Reviewer" seat is retired · received-by: pm
- 2026-09-16 · **F4 (`verify --only-unverified`, branch `verify-incremental`) goes through the Reviewer before merge** · first item in `channels/review-requests.md` · received-by: pm
- 2026-09-16 · **Immediate goal is operational: re-check all 4 source SSDs against the NAS, then certify** · README roadmap features go to P2 until the archive is certified · received-by: pm
- 2026-09-16 · **The 5th SSD is now "Scratch1", extra storage for the Mac mini - no longer a source disk** · whether it was certified before repurposing is unknown; Tester to check the manifest · received-by: pm
- 2026-09-16 · **Boot the Tester now** · product has live side effects on production data · received-by: pm
- 2026-09-16 · **NAS scripts pin a release tag instead of `:latest`** · a merge to main must not be a deploy · received-by: pm
- 2026-09-16 · **Test coverage is P1** · zero test files on code that decides whether footage is safe to wipe · received-by: pm
- 2026-09-16 · **All seats (PM, Dev, Tester) run on the Mac mini** · it is on ethernet next to the NAS; the MacBook is not a seat host · received-by: pm
- 2026-09-16 · **Audience: Eddy's archive first, public-quality docs second** · received-by: pm
- 2026-09-16 · **`HANDOFF-f3-f4.md` moves under `team/archive/`; `team/` is committed on `main` by the PM** · received-by: pm
- 2026-09-17 · **The PM runs the Reviewer (codex) and boots every seat itself; the human is not asked to run agents** · received-by: pm
- 2026-09-17 · **Push `main` (team framework commit)** · received-by: pm
- 2026-09-17 · **NAS access method is unknown to the human** · PM/Tester discover it; the human will be asked only for a concrete enable-step or credential · received-by: pm
- 2026-09-17 · **media-vault owns every scheduled batch against the archive, the nightly video tagger included; mini-server is only the machine** · handed over in `team/archive/2026-09-17-notes-from-mini-server-pm.md` (received via Eddy) · received-by: pm (relayed from the mini-server PM's record of Eddy's decision)
- 2026-09-17 · **Tagger batch rules (travel with the job): newest first over the whole archive (`copied_at` DESC, no watermark); 200 GB/night cap until a Sony night is timed; no folder skipped, six camera folders first then Backup/LeanTank/Public; `#recycle` never a source; the five GoPro test clips stay tagged** · same source · received-by: pm (relayed)
- 2026-09-15 · **`run-backup.sh` is report-only: on attach of a known external SSD run the content gap check, write a report, copy nothing; attach detection by `StartInterval` polling** · same source · received-by: pm (relayed)
- 2026-09-17 · **`case` SSD is certified and cleared to wipe; only Eddy wipes** · same source · received-by: pm (relayed)
- 2026-09-17 · **Retire mini-server's WO-1 part A over media-vault; Eddy tells that window** · received-by: pm
- 2026-09-17 · **`NAS_SHARES="media docker"` set in mini-server's `config/mini.env`** (was `"media"`) · the `docker` share mounts on the Mini within 5 min · received-by: pm
- 2026-09-17 · **SSH on the UGREEN NAS is enabled** · user/sudo/docker details still to be confirmed by the Tester · received-by: pm
- 2026-09-17 · **The PM merges F4 to `main` and pushes; a permission rule lets the PM merge/push on this repo from now on** · every merge still requires a Reviewer APPROVE in the file · received-by: pm
- 2026-09-17 · **Restart the Mac mini tonight for updates, then leave it running all night; resume with a fresh PM session and re-booted seats** · all pending items documented before the restart · received-by: pm
- 2026-09-17 · **All source SSDs are attached to the Mini (`tars`, `kipp`, `case`, `Eddy's Media Vault`, `Scratch1`); agents read them only - never wipe, format, or edit** · Eddy, after the restart; `noahsarc` is not mounted under that name · received-by: pm
- 2026-09-17 · **SSH key installed on the NAS for `figmaboi`; Eddy logged in successfully** · unblocks B2 · received-by: pm
- 2026-09-17 · **The Tester runs NAS commands as `figmaboi`, read-only - it may not delete anything** · anything that writes the manifest or the archive stays with Eddy · received-by: pm
- 2026-09-17 · **The 2026-09-13 verify pass: Eddy does not know who ran it** · stays unattributed; treated as a real pass with a real result (1 failure, sonya6700) · received-by: pm
- 2026-09-17 · **A `verified` destination is never overwritten by `copy`: same `(source_disk, source_path)` with different content is a collision - renamed under `--on-collision rename-mtime-year`, otherwise skipped, counted in `INCOMPLETE:`, exit 1; the old row keeps its hash and status** · B23(b), the defect behind the 2,668 lost photos; Dev rev 4 item 1 builds exactly this · received-by: pm
- 2026-09-17 · **The 2026-04-26 SonyA6700 source disk is unknown to Eddy; every disk he owns is attached to the Mini; there are no unformatted A6700 cards** · with all five attached SSDs searched (0 of 2,668) and `noahsarc` erased, **B23 recovery is exhausted: the 2,668 April photos (51.1 GB) are lost**; list kept at `team/archive/2026-09-01-sonya6700-lost-2668.tsv` · received-by: pm
- 2026-09-17 · **`scan` excludes `reports/` directories (tagger output) instead of inventorying them** · PM recommendation adopted by Eddy; the tagger itself still runs over every camera folder · received-by: pm
- 2026-09-17 · **Goal after the overwrite fix: run media-vault against all the SSDs, which are attached to the Mac mini (not the NAS)** · sets the next milestone after B6; how the Mini reaches the NAS manifest safely is the PM's design question (B29) · received-by: pm
- 2026-09-17 · **Mini-attached SSDs are for the read-only gap check only (which SSDs still need archiving); any SSD that needs copying gets plugged into the NAS directly and the run happens there** · replaces the B29 design question; nothing on the Mini ever writes the manifest · received-by: pm

## 2026-09-18 17:57 - Eddy's answers to the standing DECISIONS list (PM logged)
- **kipp → NAS:** Eddy will plug `kipp` into the NAS after first copying one folder from it to `Scratch1` as a precaution (~21 min, human action). Agents do not touch `kipp` while that runs.
- **tars as a dump disk Apr–Jul:** unknown. Default: treat every one of the 5,040 gap files as a candidate original; the copy's overwrite guard and dedup decide per file. No policy change.
- **NAS activity Apr 27/28 (B39, 605 empty-dest rows):** unknown. Default: rows stay as they are; `verify` reports them as missing and the repair path (B24-style) decides later. Nothing is deleted.
- Items 3 and 5 re-asked in plain words (Eddy asked what a dry-run and B24 are).

## 2026-09-18 18:01 - Eddy: "ok, let's do it" → items 3 and 5 decided (PM logged)
- **B24 live repair:** executed by the **Tester** as `figmaboi`, one-off write against the live manifest, under a PM deploy lock, per `team/context/runbook-b24.md`. Eddy is told before (this note) and after (Reviewer-checked numbers).
- **kipp dry-run on the NAS:** executed by the **Tester** as `figmaboi` (read-only) once Eddy confirms `kipp` is plugged into the NAS; the real copy is launched by Eddy.
- **Dedicated NAS agent account:** Eddy offered one; PM recommends yes (spec in the 18:0x reply). Not a blocker for B24; adopted before the kipp copy if Eddy creates it.

## 2026-09-18 18:30 - NAS agent account, kipp on the NAS, Scratch1 precaution copy (PM logged)
- **`vaultagent`** exists on the NAS: uid 1001, admin group (Eddy's choice, left as is), key-only SSH with the Mini's `~/.ssh/id_ed25519`, `/etc/sudoers.d/vaultagent-vault` = `NOPASSWD: /usr/bin/docker` only. PM verified key login + `sudo -n docker ps` at 2026-09-18 18:30. All NAS work from the kipp dry-run onward runs as `vaultagent`; B24 finishes as `figmaboi` (no switch mid-operation). Password stays in Eddy's 1Password; no agent has or needs it.
- **kipp** is plugged into the NAS over USB-C (2026-09-18 18:30); Eddy first copied one folder from it to **Scratch1 as a precaution - that copy is never deleted or modified by anyone but Eddy.**

## 2026-09-19 01:32 - B47 ownership semantics: physical probe, no dest-root column yet (PM decision, engineering lane)
Dev offered two routes for the cross-root "Dst owned" bug: (a) probe ownership physically - a foreign disk's verified row owns a destination only if its file is present at the owner's path under this run's root; (b) add a dest-root column to the manifest + backfill. PM took (a) now. Cost: a foreign disk's verified row whose file is *missing* at a shared root no longer blocks a new file landing there; `verify` then reports the old row as mismatched instead of the file being silently skipped. Exposure today: zero such rows (Tester tail scan: the only verified-missing rows were B24's 195, now repaired). (b) stays as **B49** (P2). Not a change to any sacred-path rule; nothing overwrites a verified file that exists.

## 2026-09-19 02:00 - kipp copy cleared for launch; empty `-wal`/`-shm` after a dry-run is not a write (PM, Reviewer #82)
- Dry-run 2 on `v0.2.7` (Tester #33, Reviewer #82) plans exactly the gap report: 9,872 files / 1,789.7 GiB copied (5,012 of them landing as `_2026` beside same-named originals), 521 recorded as already archived by content, 0 kept / owned / INCOMPLETE. The real copy is the same command without `DRY_RUN=1`; **who types it is Eddy's call** (Eddy, or the Tester as `vaultagent` under lock).
- A dry-run opens the manifest with `mode=ro` and returns before any upsert (Reviewer #82, `cmd/vault/main.go:96`, `manifest.OpenReadOnly`). The empty `manifest.db-wal` and 32 KiB `-shm` it leaves are SQLite reader housekeeping; the next writer checkpoints and removes them. Not a deviation; the "no persisting -wal/-shm" check is dropped from the runbooks for dry-runs.

## 2026-09-19 07:37 - kipp copy launched by Eddy (human-launched `vault copy`, TEAM.md sacred-path rule satisfied)
Eddy pasted the runbook-kipp step-5 line in a Mini Terminal at 07:36; PM confirmed the container on the NAS at 07:37. Tester witnesses (rev 7, read-only); PM lock in dev-questions.md; nothing else runs against the NAS until `all kipp copies done`.

## 2026-09-19 13:47 - B6 on the `kipp-*` disks: split into two passes; `deduped` rows are certified by reference (PM decision after Tester #42)
- Eddy said "go" at 13:4x. Tester #42 found that on `v0.2.7` a `deduped` row is resolved under the *current* disk's root, so `kipp-backup`'s single by-reference row (owner `media-leantank`) reads as `Missing: 1` and blocks its certificate, while `kipp-leantank`'s 520 by-reference rows would be silently rewritten from `deduped` to `verified`.
- Decision: **B51 (Dev, `v0.2.8`)**: `verify` treats `deduped` rows as by-reference - it does not hash them under this root and does not change their status; `certify` accepts a `deduped` row when its sha is present on a `verified` row of any disk, and records the owner disk/path in the certificate. The `deduped` status is provenance and stays.
- Meanwhile **pass A** (verify + certify `kipp-sonya6700`, `kipp-multicam`, `kipp-auditorium`, `kipp-gopro`, `kipp-sonyzve10` - no deduped rows) runs now on `v0.2.7`; **pass B** (`kipp-backup`, `kipp-leantank`) after `v0.2.8`. One vault process at a time throughout.

## 2026-09-19 16:40 - Reviewer unavailable until Tue 2026-09-22 12:21 (Codex weekly limit) - PM ruling
- Nothing merges to `main`, no number reaches Eddy as verified, and no SSD is wiped on the strength of a `witnessed`-only claim until #85 is answered. Tester work that needs no verdict to *run* (read-only gap checks, dry-runs) may proceed; launches wait. Eddy asked whether to wait or to provide an alternative non-Claude reviewer.

## 2026-09-20 21:57 - Local reviewer trial FAILED; a 7B model on the Mini is not a Reviewer (PM ruling)
Eddy ran `codex exec --oss --local-provider ollama -m qwen2.5-coder:7b` on review #85 (numbers only). Result: **APPROVE with invented numbers** ("PASS: 123 / FAIL: 45", "Before: 1234 / After: 1240"), commands never executed (2,391 tokens total; it "queried" log files as SQLite tables). Ruling: no local model on the 16 GB M4 Mini may hold the Reviewer seat, for numbers or code. A tool that says APPROVE without looking is worse than no reviewer. #85 waits for Codex (Tue 2026-09-22 12:21). Revisit only with ≥48 GB unified memory and a 30B-class coder model, and then only after it passes a known-answer test (re-review a closed request and reproduce the recorded verdict).

## 2026-09-20 22:30 - Gemini CLI (`gemini-3.8-flash`, Eddy's AI Studio key) is the second Reviewer for NUMERIC verdicts (PM ruling after calibration)
Calibration on closed #83 passed: real commands, every number reproduced, same finding as Codex. Recorded as `Reviewer (Gemini, <ts>)` in review-requests.md. Code reviews stay with Codex until Gemini passes the #79 code calibration (needs the pro model = billing). Free-tier quota is 20 requests/day/model, resets 00:00 PT; #85 runs on it at 00:07 unless Eddy enables billing first.

## 2026-09-21 00:30 - "Every byte of `kipp` is on the NAS, verified and certified": YES (Reviewer #85, Codex, after the Tester's witness #43-#44)
Arithmetic: 9,872 verified rows + 521 by-reference rows whose owners are verified = 10,393 files; 1,921,695,784,449 + 31,897,100,927 = 1,953,592,885,376 bytes = kipp's independent inventory (Tester #19 Mini walk, #31 NAS walk). Seven Ed25519 certificates at `/volume1/docker/vault-certs/kipp-*.cert.json` cover all of them; 12/12 certs in the folder validate under one key. **Wipe or keep `kipp` is Eddy's decision; no agent wipes, formats or writes an SSD.** Codex is on the 5x plan since 2026-09-21 00:2x (Eddy).

## 2026-09-21 00:31 - Eddy: `tars` connected to the NAS (`/mnt/@usb/sdd1`, label `tars`); "go sweep" = B6 full re-verify + re-certify of the eight `media-*` disks (runbook-b6-media.md)
Executor: Tester as `vaultagent` under a PM lock, after the tars dry-run. Old Sep-2 certificates are archived, not overwritten.

## 2026-09-21 00:35 - A root cron has been running the April verify+certify script every Sunday 02:00 since April 27 (Tester #46) - PM rulings
Facts: `/etc/cron.d/vault-weekly-verify` (root, Apr 27) runs `/volume1/docker/nas-verify-certify-all.sh` (April version, image `:latest` = the April 28 build, certs written **inside** the archive trees, six disks). It ran Sep 13 (the "unattributed" pass - now attributed) and Sep 20 02:00-12:51: all six disks 0 mismatches, 67,179 rows' `verified_at` refreshed, nothing else changed. Next fire: Sun 2026-09-27 02:00 MDT.
Rulings: (1) **Eddy disables the cron** (root file; agents cannot and should not): `sudo bash -c 'mv /etc/cron.d/vault-weekly-verify /volume1/docker/vault-weekly-verify.disabled'` as `figmaboi` - before Sunday 27 at the latest; until then no long NAS job may straddle Sunday 02:00. (2) **B53**: a maintained weekly job replaces it (re-placed script from `main`, image pinned, all disks incl. `media-backup`/`media-leantank`/`kipp-*`, certs in `vault-certs/`); Eddy re-enables the cron pointing at it once reviewed. (3) **B54**: the six in-tree `media-*.cert.json` files (rewritten Sep 20) are files without rows inside sacred trees - removing them is Eddy's `rm`, after B53, listed by path by the Tester. (4) **Rev 10 is reduced**: six disks were fully re-hashed 12 h ago with 0 mismatches, so tonight the Tester runs full verify+certify only for `media-backup` and `media-leantank` (49 GB), and **certify only** (no re-hash) for the other six on `v0.2.8` so all eight current certificates land out-of-tree now (~20 min instead of 13 h). The full 13-h sweep stays available any night Eddy wants it; the Sep-20 pass is recorded as the last full verification.

## 2026-09-21 00:37 - Eddy disabled the root weekly cron: `/etc/cron.d/vault-weekly-verify` → `/volume1/docker/vault-weekly-verify.disabled` (PM verified read-only as `vaultagent`: no vault entry left in cron.d). The single-writer rule holds again. Re-enable only via B53.

## 2026-09-21 08:44 - tars launch plan: `leinfo.sav` is out of scope; the port moves before launch (PM rulings after Tester #49)
- Dry-run 2 on the B52 script plans the gap exactly except one file: GoPro's 8-byte housekeeping file `DCIM/leinfo.sav`, whose `_2026` landing name is already taken by kipp's copy (three distinct 8-byte versions exist). **Ruling: out of scope** - it is camera state, not media; the tars copy is launched expecting `GoPro FAILED` / `1 folder(s) FAILED` for that one file, and the Reviewer's post-copy recount must show exactly that and nothing else. **B55 (P2, Dev):** `rename-mtime-year` needs a deterministic second suffix for a third same-name-same-year file so a future run does not stop on it.
- `tars` on its current NAS port reads at ~41 MB/s (kipp's port did 155). **Eddy moves tars to kipp's port before launching** (kipp is done: rule-8 YES, #85). The plan is content-based; no third dry-run - but the Tester re-identifies the mount (device letter changes) and the launch line uses the new `SSD_SRC`.
- The 6 tars files outside the copy tables (`Test/` ×5 and `GoPro/GX010788.MP4` above `DCIM/`) are archived by content already (#20, #49); recorded, no action.

## 2026-09-21 12:28 - tars copy launched by Eddy (human-launched `vault copy`; runbook-kipp template; expected `1 folder(s) FAILED` = GoPro `leinfo.sav`, out of scope per 2026-09-21 ruling)
