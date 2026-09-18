# PM handoff - restart of the Mac mini, 2026-09-17 evening

**From:** media-vault PM (Fable 5.1, session ending at the restart). **For:** the next PM session. Everything load-bearing is also in `TEAM.md` (Current state), `BACKLOG.md`, `DECISIONS.md` and the channels - this file is the reading order and the exact resume steps. Re-verify anything dated.

## Resume, in order

1. Boot the PM (Fable-class), from `~/Projects/media-vault` on `main`, with the Mac kept awake:
   `caffeinate -i claude --model fable --remote-control media-vault-pm "You are the PM for media-vault. Read team/TEAM.md and team/actors/pm.md, then team/archive/2026-09-17-pm-handoff.md - resuming after a restart, not a fresh project. Do not run the First Session."`
2. `git status` + `git worktree list` must show: `~/Projects/media-vault` on `main` (clean), `~/Projects/media-vault-dev` on `tests-and-pinning`, `~/Projects/media-vault-tester` detached. Worktrees survive a reboot; Terminal windows and tmux do not.
3. Check `~/mounts/media` and `~/mounts/docker` are mounted (`mount | grep mounts`); the `com.varela.mount-nas` agent remounts within 5 min of login, or `launchctl kickstart gui/$(id -u)/com.varela.mount-nas`.
4. Boot Dev and Tester in visible Terminal windows, then **send a bare Return** to each (see "Nudging seats"):
   - Dev: `cd ~/Projects/media-vault-dev && claude --model opus --remote-control media-vault-dev "You are the Dev for media-vault. Read team/TEAM.md, team/actors/dev.md, then team/channels/dev-questions.md. Resuming after a restart - your last note says where you stopped."`
   - Tester: `cd ~/Projects/media-vault && claude --model opus --remote-control media-vault-tester "You are the Tester for media-vault. Read team/TEAM.md, team/actors/tester.md, then team/channels/tester-feedback.md. Resuming after a restart - your last item says where you stopped."`
5. Read both channels bottom-up; the seats' last notes (written 2026-09-17 ~18:25 on the PM's stop order) say where they stopped.

## Where every in-flight item stands

| Item | State at handoff | Next step | Owner |
|---|---|---|---|
| B1 F4 `--only-unverified` | **Done.** Merged `7cca025`, pushed. CI built `:latest` with it. | none | - |
| Review #3 (Dev rev 3 @ `c1f7fbd`) | **FINDINGS, four** (18:38): two `testguard` bypasses (`..` cleaned before symlink resolution; `GOTMPDIR` unchecked), metadata-action still emits `:latest` on semver tags (`flavor: latest=false` missing), one exit-table line wrong. PM accepted all; Dev rev 4 **item 0** covers them. | Boot Dev → it fixes on `tests-and-pinning` → READY FOR REVIEW → PM stages **#4 (fixes only)** → Codex → merge on APPROVE → tag `v0.2.0`. PM evidence at `c1f7fbd`: vet/test/gofmt clean, but that does not cover the findings. | Dev → PM |
| `v0.2.0` tag | not cut | after #4 merges: `git tag -a v0.2.0 -m "..." && git push origin v0.2.0` - the scripts on `main` already default to `v0.2.0`; CI publishes the semver tag | PM |
| B2 NAS access | `docker` share mounted 18:20; SSH **enabled by Eddy** (user/sudo/docker facts not yet confirmed) | Tester reports the SSH probe result and the manifest snapshot queries (its items 1-2, staged in its channel) | Tester |
| B7 source SSDs | candidates `tars`, `kipp`, `case`, `noahsarc`(?) - unconfirmed | Tester's item 1 answers it from the manifest | Tester |
| B6 NAS incremental verify + certify | blocked on `v0.2.0` (scripts pull it) and on knowing who runs NAS commands (SSH as `figmaboi`? sudo?) | once tagged: PM writes the exact `docker run ... verify <disk> /volume1/media/<Camera> --only-unverified` per disk as a lock-noted operation; Eddy or the Tester runs; Tester witnesses | PM → human/Tester |
| B17 tagger transfer | not started; source is mini-server `dev/wo1-run-tagging` (`d9d677d` tested, `5e7bac7` WIP) | Dev **rev 4** after rev 3 lands | PM → Dev |
| B9 F4 tests | startable now (F4 on `main`) | fold into rev 4 with B17, or its own small branch | PM → Dev |
| Permission rule | `.claude/settings.local.json` saved by Eddy with `git merge/push/tag/commit/add`, `go build/vet/test`, `python3 -` allows | should be live in the new session - if a merge is refused, tell Eddy | PM |
| Retire mini-server's WO-1 part A | Eddy said he would tell that window | nothing unless it reappears | - |

## What the Tester found at 18:24 (read its items 5-13 in full before anything else)

- **#5 P0 data loss (B23):** 2,668 SonyA6700 photos (51.1 GB) overwritten 2026-09-01 by the `case` copy - same `(source_disk, source_path)`, different content, `copy` recopied in place. Not on `tars`, `kipp`, `Eddy's Media Vault`. List at `/Volumes/Scratch1/tester/sonya6700-overwritten-2026-09-01.tsv`. **Nothing gets wiped.** Candidates: `noahsarc`, the disk that fed the April 26 copy, camera cards.
- **#7 (B24):** the 195 `copied` rows have `dest_path` missing `CLIP/`/`DCIM/`; bytes verified 195/195. B6 cannot pass until repaired (Dev rev 4 item 2).
- **#9 (B25):** stale April cert inside `SonyA6700/` attests 51 GB that is gone. Certs must live outside the trees.
- **#10 (B2):** SSH port open, key refused → Eddy runs `ssh-copy-id`.
- Archive is **8.20 TiB / 67,735 rows**, not 3.3; `source_disk` is per camera; `kipp` never copied (B26); 3,193 duplicate sha groups (B8).
- Tester's scratch artifacts under `/Volumes/Scratch1/tester/` stay until B23 closes.

## Facts the next PM should not re-derive

- NAS: DXP2800 at `192.168.1.167`, SMB user `figmaboi`; `~/mounts/media` = `/volume1/media`, `~/mounts/docker` = `/volume1/docker` (manifest at `vault-nas-config/manifest.db`, last written 2026-09-13 11:50; `verify-certify.log` same time - a Sep 13 verify pass nobody documented; `vault-certs/` from Sep 2). **Never open the WAL sqlite over SMB - snapshot to `/Volumes/Scratch1` first.**
- The Mini's `~/vault-config/manifest.db` is a stale Sep 2 snapshot. Ignore it.
- Codex is the Reviewer: `codex exec -s workspace-write -C ~/Projects/media-vault -o <scratch>/reviewN-last.md "<prompt from review-requests.md header>"`; ~5 min per review; it cannot create worktrees under the sandbox, so it traces source and the PM supplies build/test evidence.
- This session's permission classifier blocked: pushes/merges to `main` (now allowed by the rule), any SSH/`nc` to the NAS (Tester's lane), editing its own settings, and messaging another project's seat windows. Route those through Eddy.
- Other seats on this Mac belong to other projects (TEA, mini-server). Never message them.

## Nudging seats (this cost an hour today)

`osascript -e 'tell application "Terminal" to do script "<msg>" in window id N'` pastes into the seat's prompt **without submitting**; follow with `do script "" in window id N`. Get ids with `tell application "Terminal" to get {id, name} of every window` - they change after every reboot. Confirm with `get contents of tab 1 of window id N`: an empty `❯` and "esc to interrupt" means it's working.

## Open questions for Eddy (carry into the first DECISIONS NEEDED)

1. **B23 policy** (PM recommends: a changed-content recopy over a `verified` row is a collision - never overwrite; rename-mtime-year or skip+exit 1).
2. **Where might the 2,668 photos still exist?** Which disk fed the 2026-04-26 SonyA6700 copy; where is `noahsarc`; are the camera cards from before April 26 still unformatted? Attach them one at a time; the Tester hash-compares.
3. `ssh-copy-id -i ~/.ssh/id_ed25519.pub figmaboi@192.168.1.167` on the Mini (Eddy types the password) - unblocks `docker ps`/sudo facts and lets a seat run NAS commands.
4. Who runs NAS commands once SSH works - the Tester as `figmaboi`, or Eddy?
5. Tagger `reports/` dirs (GoPro + iPhone) have no manifest rows: inventory them or exclude `reports/` from `scan`?
6. Did Eddy run the Sep 13 verify pass? (Not in any record.)
