# Notes for the media-vault PM - from the mini-server PM

**Written 2026-09-17 by the mini-server PM (Fable 5.1), on Eddy's decision of the same day:** *mini-server is the machine; every scheduled batch against the archive - nightly video tagging included - is yours.* This note is what I thought was mine and turned out to be yours, with the facts I verified along the way. Handed to you by Eddy, not by me - our frameworks don't let PMs message each other. Everything here is dated; re-verify anything load-bearing.

Repo: `github.com/eddyvarelae/mini-server`, checkout `~/Projects/mini-server` on the Mini. Its `team/` files hold the full history referenced below.

## 1. The nightly tagger - built, tested once, never scheduled

**What exists.** `scripts/run-tagging.sh` + `scripts/tagging-helper.py` on branch **`dev/wo1-run-tagging`** (tip `d9d677d` = the tested version rebased; one more **"WIP, untested"** commit on top holds half-done rev-3 changes - see below). Built by an Opus Dev seat 2026-09-15 to WO-1 rev 1/2 (`team/channels/dev-questions.md`, under Superseded), evidence at `tested` (author) in the same channel, spot-checked by me 2026-09-16 (state db, xattrs, reports, log all matched). **Not merged to main anywhere.**

Design, in one paragraph: source `config/mini.env`; refuse (exit 2) unless `$SCRATCH_DIR` is a mounted volume, `~/mounts/media` is mounted, the manifest exists, Ollama answers, and the tagger venv imports `ultralytics`; single-instance `mkdir` lock with stale-lock reporting; select a batch from the **media-vault manifest** (rows `status='verified'`, video extensions, camera folders in `TAG_SOURCES`), capped at `TAG_BATCH_MAX_GB` (200); per file: rsync NAS → `$SCRATCH_DIR/tagging/<run-id>/`, run `~/Projects/video-tagger` (its own `.venv`) on the scratch copy, write Finder tags (`com.apple.metadata:_kMDItemUserTags`, merged with existing) + `com.videotagger.processed` marker + `reports/<stem>/` next to the NAS file, read each back, then delete the scratch copy; per-file done-set in `~/Library/Application Support/mini-server/tagging-state.db`; summary line + exit 1 if any file failed; `--dry-run`, `--limit N`. Log: `~/Library/Logs/mini-server/video-tagger.log`. Manifest → NAS path is `~/mounts/media/<Camera>/<dest_path>` with `source_disk = media-<camera lowercased>` (checked across all 7,110 video rows: 0 missing).

**Proven:** dry run touches nothing; 3-file real run; kill mid-file and resume (in-flight file counted failed, re-selected, done file not re-tagged); every precondition exits 2 with a message. **Not proven:** a full 200 GB night; an 11 GB Sony clip (whisper on a long clip); xattr write-back on any folder but GoPro.

**Eddy's decisions on the batch (2026-09-17, our `DECISIONS.md`)** - these are his answers, they travel with the job:
- **Newest first, whole archive** (order by `copied_at` DESC); no "from now" watermark. The tested version is oldest-first with a watermark - that is the main thing the WIP commit changes.
- **Cap stays 200 GB/night** until a Sony night has been timed.
- **No folder skipped; the six camera folders first** (`SonyA6700 SonyZVE10 GoPro DJIFlip DJIMini2 iPhone`), then `Backup LeanTank Public`. `#recycle` is NAS trash, never a source.
- The five clips Dev tagged during testing stay as they are (see §3).

**The WIP commit** (untested, `bash -n`/`py_compile` only): newest-first selection, two-tier `TAG_SOURCES`/`TAG_SOURCES_TIER2`, a disk walk for folders with no manifest rows, and a per-run rsync snapshot of the NAS manifest. Take it or `git checkout -- scripts/` it; my rev-3 spec is in `dev-questions.md` (top, struck) if you want the reasoning.

**Three facts that bit us, in priority order:**
1. **The Mini's `~/vault-config/manifest.db` is a stale snapshot** - last written 2026-09-02 23:52, `max(copied_at)` = 2026-09-02. The tested script reads *that*. Your `context/production.md` says the live manifest is `/volume1/docker/vault-nas-config/` on the NAS, single writer. So as built, the job would never see footage archived after Sep 2. Read the NAS copy (via snapshot - don't open a WAL sqlite over SMB).
2. **The NAS `docker` share is not mounted on the Mini.** `config/mini.env` (sacred, Eddy's) has `NAS_SHARES="media"`; `~/mounts/docker` is empty. `ACTION (human)` with Eddy: `NAS_SHARES="media docker"`, then the `mount-nas` LaunchAgent picks it up within 5 min. Your Tester will need this too.
3. **`Public` has 32 video files on disk and zero manifest rows** (`media-backup` 14 rows, `media-leantank` 126, no `media-public`). Manifest-driven selection can't reach it; Eddy said don't skip it.

## 2. Hosting: what the Mini gives your job, and what stays ours

| Thing | Where | Owner |
|---|---|---|
| Scratch volume | `/Volumes/Scratch1` (2 TB SSD, `SCRATCH_DIR` in `mini.env`) - rule: pull to scratch, process locally, write back; never process over SMB (~7× slower) | mini-server |
| NAS mounts | `~/mounts/media` (mounted), `~/mounts/docker` (not yet - §1.2), by `com.varela.mount-nas` every 5 min; `/Volumes` needs root so user agents mount under `~/mounts` | mini-server |
| Ollama | `homebrew.mxcl.ollama`, `127.0.0.1:11434`, llava pulled | mini-server |
| video-tagger | `~/Projects/video-tagger`, `.venv` on python@3.13, `ultralytics` imports OK 2026-09-17 | shared - the repo is Eddy's; we rebuild the venv if the machine changes |
| LaunchAgent template | `launchd/com.varela.video-tagger.plist.example` - 02:00 daily, stdout/err to the log above; install = copy to `~/Library/LaunchAgents/`, `launchctl bootstrap gui/$(id -u) ...`. launchd's PATH is bare; the script prepends `/opt/homebrew/bin` itself | template ours; **the job it points at is yours**. Ask us to install it when your script is where you want it, or install it yourselves - one act, evidence in your channel |
| Logs | `~/Library/Logs/mini-server/` | shared dir |
| tmux | session `mini` (recreated after the 2026-09-16 reboot - sessions don't survive reboots) | anyone |
| Reboot chain | power → autorestart → no FileVault → autologin → keychain → LaunchAgents → mounts. Verified live on 2026-09-16 (unplanned shutdown 2026-09-15 20:30, up 10:03). tmux and any seat window die | mini-server |

## 3. Archive items I had on my backlog that are yours

- **B5 (ours) → your B6:** merge F4, `verify --only-unverified` the 195 `copied` rows, certify. You already own it. One thing our context has that yours may not: `--dedupe-content` matches only `verified` rows, so those 195 are invisible to dedupe until promoted, and `certify` refuses the disk while they exist.
- **`tars`** - attached to the Mini, never examined by content. `scripts/archive-gap.py` in mini-server is a standalone content-hash gap tool that agrees exactly with `vault scan --dedupe-content`; it also stages absent files. Path-based scan said 9,339 files where content said 186 - `team/context/archive.md` has the numbers.
- **`kipp` and one unnamed disk** - never seen. **`case`** - certified, cleared to wipe, Eddy hasn't (only Eddy wipes).
- **152 probable duplicates** (~0.09 TB, DJIFlip/GoPro broken-clock filenames); `vault dedup` exists; deletion is Eddy's call.
- **Test residue on the archive** - `GoPro/Videos/GX010007.MP4`, `GX010008`, `GX010012`, `GX010014`, `GX010021 copy.MP4` carry real Finder tags + `com.videotagger.processed` + `reports/<stem>/` from Dev's tests; rows 240010-240014 in the state db. Eddy said leave them; they're your Tester's first fixture.
- **`run-backup.sh`** (our B8, never built): Eddy decided 2026-09-15 it is **report-only** - on attach of a known external SSD (anything except `Scratch1` and the boot disk), run the content-based gap check, write a report to the log dir, **copy nothing**; attach-triggered via `StartInterval` polling `/Volumes` (not `WatchPaths`, unreliable for mounts; not a weekly timer). Template `launchd/com.varela.media-backup.plist.example`. Yours if you want it.

## 4. For your Tester (their open ACTION on NAS access)

What we know, verified 2026-09-15: NAS is a UGREEN DXP2800 at **`192.168.1.167`** (DHCP-reserved), advertises as **`DXP2800-43F8`**; `ugreen.local` does not resolve. SMB user `figmaboi`, password in the Mini's Keychain (`figmaboi @ DXP2800-43F8.local`) - sacred, never echo it. **SSH user/sudo/docker on the NAS we never had** - that is Eddy's answer. The Mini is on the wired `192.168.1.x` segment with the NAS.

## 5. Lessons that transfer (full text: `team/context/lessons.md`)

- `pkill -f 'vault scan'` matches its own shell and kills the job you just started. Backgrounded processes don't survive a tool call - tmux.
- video-tagger once shelled out to a literal `python3.10`; `run_yolo` swallowed the `FileNotFoundError` and produced a fully "tagged" library with no object tags. Fixed upstream; the precondition above exists because of it. `tags=[]` on a clip is legitimate only if the report shows detections that fell below threshold.
- ffmpeg inside `tagger.py` reads the parent's stdin - Dev's first real run died on file 2 because ffmpeg ate the batch list. Children get `</dev/null`, batch on fd 3.
- Certificates never go inside the tree they certify (a cert row once blocked 39,219 files).
- Check archives by content, never by name.

## 6. What mini-server keeps (so you know what to ask us for)

The machine: `SETUP.md` to completion, `bootstrap.sh`, `config/mini.env` schema, mounts, SSH/tailnet, toolchain and venvs, LaunchAgent *hosting*, scratch, macOS patch habit, Tailscale key expiry (reminder set for 2027-02-14), gworkspace tokens, and hosting TEA's daemon. If a job needs something from the machine - a share mounted, a venv rebuilt, a plist installed, disk space - that is a note to Eddy for our channel. We don't run jobs.

-- mini-server PM, 2026-09-17T16:40-07:00
