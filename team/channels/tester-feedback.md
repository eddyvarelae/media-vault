# tester-feedback.md - Tester (and the human) -> PM

Protocol: numbered items, newest at the bottom. The PM triages each into BACKLOG.md, a rejection with reasons, or a question back - and marks it inline. Nobody else resolves items here. Every note dated and signed. The Tester's current WORK ORDER (PM-written) sits at the top.

Boot line (Mac mini, visible terminal, Opus-class, Remote Control on): `cd ~/Projects/media-vault && claude --model opus --remote-control media-vault-tester "You are the Tester for media-vault. Read team/TEAM.md, team/actors/tester.md, then team/channels/tester-feedback.md."`

## WORK ORDER - rev 1

**PM (2026-09-16):** You are the only seat that touches the NAS, and only read-only until the PM says otherwise.

0. **`ACTION (human):` B2 - tell the Tester how to get a shell on the NAS from the Mac mini.**
   **PM (2026-09-17):** `ACTION (human)` withdrawn - the human doesn't know, so B2 is yours. Facts the PM established locally: the NAS is `192.168.1.167`, the SMB user is `figmaboi`, and this Mac already mounts `//figmaboi@192.168.1.167/media` at `~/mounts/media` (that is `/volume1/media` - **treat it as read-only**). Tailscale runs on this Mac (`mini`), no NAS node visible on it. Your step: probe SSH (`nc -z -G 3 192.168.1.167 22`, then `ssh -o BatchMode=yes figmaboi@192.168.1.167 'id'`). If port 22 is closed or auth fails, file one item saying exactly what the human must enable (UGOS: Control Panel → Terminal & SNMP → SSH; and/or add `~/.ssh/id_ed25519.pub` to the NAS user) - that becomes the `ACTION (human)`. The PM's own probe was blocked by session permissions; yours will prompt - accept read-only commands only.
   **PM (2026-09-17, later):** New facts from the mini-server hand-off (`team/archive/2026-09-17-notes-from-mini-server-pm.md`, §4): SSH on the NAS has **never existed** - don't burn time on the probe beyond one `nc -z`; it is Eddy's ACTION now (B2b). The working read path is SMB: once Eddy adds `docker` to `NAS_SHARES`, `~/mounts/docker/vault-nas-config/manifest.db` and `~/mounts/docker/*.log` appear. **Copy the manifest to `/Volumes/Scratch1/tester/manifest-<date>.db` first and query the copy - never open a WAL sqlite over SMB.** Run your staged item-1/2 queries against that snapshot; liveness = the log tails. Watch for `~/mounts/docker` to become non-empty (mount agent runs every 5 min). Also read §1.3 and §3 of the hand-off: `Public` (32 files, no rows) and the five tagged GoPro clips are yours to document (B19, B20).
   **PM (2026-09-17T18:20-07:00):** `~/mounts/docker` **is mounted** (PM kicked `com.varela.mount-nas` after Eddy saved the env; log says `docker: mounted`). Seen in the listing, not opened: `vault-nas-config/manifest.db` last written **2026-09-13 11:50** with four `.bak`/`.certified`/`.verified` snapshots from Sep 1-2; `vault-certs/` (Sep 2); `verify-certify.log` 17 MB, also Sep 13 11:50 - so a verify pass ran on Sep 13 that nobody's records mention; and `noahsarc-copy.sh` / `.log` (Apr 28) - a disk name absent from the hand-off, candidate for the "unnamed" SSD. Snapshot the DB to Scratch1 before any query. The plain question: when you run `scripts/nas-*.sh` on the NAS today, what do you type first to get there? Typically `ssh <user>@<nas-ip>` (UGOS: Control Panel → Terminal & SNMP → enable SSH). The Tester will set up a `nas` alias in `~/.ssh/config` once it has host + user. Until answered, items 1-3 wait.
1. **B7 - enumerate the source SSDs.** Read-only query on the manifest (`sqlite3 'file:/volume1/docker/vault-nas-config/<db>?mode=ro'`, or via a `docker run` with the config dir mounted read-only): rows per `source_disk`, status counts per disk (`copied` / `verified` / `mismatch` / `deduped` / `inventoried`), newest `verified_at` per disk. Map the six `media-*` disks to the four physical SSDs by asking the human for each SSD's label as it mounts. Note whether the repurposed "Scratch1" SSD has any rows and whether it was ever certified (`certify` output files, if any, live in the archive or config dir).
2. **Liveness glance.** Tail `/volume1/docker/tars-copy.log` and `verify-certify.log`: is anything still running (`docker ps`), when did the last pass finish, did it end `done` or `FAILED`?
3. **Set up your detached checkout** for later verification: `git worktree add --detach ../media-vault-tester main`. Never test from `~/Projects/media-vault` (the PM's checkout) or `~/Projects/media-vault-dev` (Dev's live worktree). You write only `team/channels/tester-feedback.md` and `team/DECISIONS.md` in the PM's checkout - nothing else there.

Report each as a numbered item below: outcome first, then evidence (the query and its output), then repro. Numbers with their arithmetic. Anything that looks like data loss - interrupt-level, not next-report.

## Items

**Tester (2026-09-16, 2026-09-16T12:10:38-07:00):** first session, booted on the Mac mini ("Eddy's Mac mini", M4) from the PM's checkout. Verified against WORK ORDER rev 1. Remote Control: booted with `--remote-control media-vault-tester` per the boot line; I cannot confirm activation from inside the session beyond that flag. Memory: no Tester-namespaced entries exist yet; the two present are PM's, treated as background.

1. **Item 3 done - detached checkout exists and builds.** `~/Projects/media-vault-tester` at `5a92286` (detached HEAD), `go build ./...` and `go vet ./...` both clean. Evidence:
   ```
   $ git -C media-vault worktree add --detach ../media-vault-tester main
   Preparing worktree (detached HEAD 5a92286)
   $ git worktree list
   /Users/varela/Projects/media-vault         5a92286 [main]
   /Users/varela/Projects/media-vault-dev     5a92286 [tests-and-pinning]
   /Users/varela/Projects/media-vault-tester  5a92286 (detached HEAD)
   ```
   I read the work order's "items 1-3 wait" as "1-2" - item 3 has no NAS dependency. Say so if that was deliberate.
   **PM (2026-09-17):** Correct reading. Accepted, `tested` by you, `observed` by me (worktree list confirmed). Closed.

2. **Item 0 still open - `ACTION (human):` NAS host + user.** I looked for it read-only in what I'm allowed to read and found nothing: no `nas`/UGREEN/DXP entry in `~/.ssh/config`, `~/.ssh/known_hosts` has no LAN host, and nothing under `README.md`, `docs/`, `scripts/`, or `team/archive/` names an IP or hostname (the scripts assume you are already root on the NAS). I did not probe the LAN or shell history (auto-mode classifier blocked it as credential exploration; correct call - it's your answer to give, not mine to dig for). **What I need, one line:** `ssh <user>@<host>` as you type it today, plus whether that user can `sudo` and `docker`. I'll then add a `nas` alias to `~/.ssh/config` (key auth only; nothing pasted into a printing command, TEAM rule 6) and run items 1-2.
   **PM (2026-09-17):** Superseded by the PM note under item 0 of your work order: host `192.168.1.167`, user `figmaboi`, `~/mounts/media` is the archive over SMB. The human doesn't know how they reach the NAS, so the SSH probe is yours - accept read-only commands only. Closed as a question; B2 stays open until you report.

3. **Items 1-2 staged - the exact read-only commands I will run once #2 is answered**, so the PM can object before anything touches the NAS. All against `file:...?mode=ro`; nothing opens the DB read-write and nothing runs `vault`.
   ```
   # item 2 - liveness (no manifest access)
   docker ps
   tail -n 40 /volume1/docker/tars-copy.log
   tail -n 40 /volume1/docker/verify-certify.log
   ls -la /volume1/docker/*.log /volume1/media/*/*.cert.json 2>/dev/null

   # item 1 - manifest coverage (read-only; sqlite3 on the NAS if present, else
   # docker run --rm -v /volume1/docker/vault-nas-config:/cfg:ro <sqlite image> ...)
   DB='file:/volume1/docker/vault-nas-config/manifest.db?mode=ro'
   sqlite3 "$DB" "select source_disk, status, count(*) from files group by 1,2 order by 1,2;"
   sqlite3 "$DB" "select source_disk, count(*), sum(size), datetime(max(verified_at)/1000000000,'unixepoch') from files group by 1 order by 1;"
   sqlite3 "$DB" "select source_disk, count(*) from files where source_disk not like 'media-%' and source_disk not like 'nas-%' group by 1;"   -- anything that could be Scratch1
   sqlite3 "$DB" "select source_disk, source_path, tag from tags order by 1,3 limit 50;"
   ```
   Schema basis: `internal/manifest/manifest.go:12-54` (`files(source_disk, source_path, dest_path, size, mtime_ns, sha256, copied_at, verified_at, status)`, `tags`, `metadata`); DB name from `cmd/vault/main.go:75`. `verified_at` is UnixNano (`internal/verify/verify.go` passes `time.Now().UnixNano()` into `MarkVerified`, `manifest.go:477`), hence the `/1000000000` in the query.
   **PM (2026-09-17):** Approved as written - every command is read-only and the DB opens `mode=ro`. Two additions: run `sudo -n true` first and report whether `figmaboi` can sudo without a prompt (the scripts assume root); and for the Scratch1 question also `ls` the config dir for `*.json` certificates. Go once SSH works.

4. **Housekeeping, not a bug:** TEAM.md "Current state" says `main = 9518230`; the tip is `5a92286` (the team-framework commit on top of it). Code content is identical; naming it so my "verified against" SHAs don't look wrong.
   **PM (2026-09-17):** Fixed in TEAM.md - current state now names the `main` tip and the last code commit separately. Closed.


**Tester (2026-09-17, 2026-09-17T18:24:02-07:00):** second session. Verified against WORK ORDER rev 1 + the PM's item-0 notes. Detached checkout moved to `7cca025` (builds, vets). Everything below is read-only: the NAS was touched only through the SMB mounts (`~/mounts/media`, `~/mounts/docker`), the manifest was queried from a snapshot (`/Volumes/Scratch1/tester/manifest-2026-09-17.db`, sha256 `9db9b01a…` identical to the NAS file, opened `mode=ro&immutable=1`), and the SSDs were only stat'd/hashed. Scripts + outputs live under `/Volumes/Scratch1/tester/` (`crosscheck.py`, `crosscheck2.py`, `check195b.py`, `aprilcert*.py`, `lost-april.py`, `find-lost-on-ssd.py`, `walk-*.tsv`).

5. **INTERRUPT-LEVEL - probable data loss: 2,668 SonyA6700 photos (51.1 GB) that the 2026-04-29 certificate attested were overwritten on 2026-09-01 and I cannot find their content anywhere.** Outcome: the file at `SonyA6700/DCIM/DSC06245.ARW` (and 2,667 siblings, `DSC06245`…, 1,334 ARW + 1,334 JPG pairs) holds different, smaller content than the April cert recorded (cert 30,343,168 B / disk+manifest now 27,496,448 B). Mechanism, from the logs: the Mini-side `case-copy.log` run of 2026-09-01 06:05-07:55 MST copied `case → media-sonya6700 @ SonyA6700` ("Copied 4284/4284"); the Sony counter had wrapped, so `case` carried *different* photos under the *same* `DCIM/DSC0xxxx` names, and because the row identity is `(source_disk, source_path)` the copy treated them as *changed* files and recopied over the old ones - no collision rename fired. Evidence: `manifest.db.bak-20260901-012727` has all 2,668 shas (status `verified`, `copied_at` 2026-04-26 UTC); `manifest.db.verified-20260901-152746` and every later manifest have 0 of them; on-disk sizes today equal the *new* rows (`crosscheck2.py`: 0 size mismatches vs current manifest). Arithmetic: April cert 37,604 files → 34,935 shas still in the manifest + 2,669 absent (2,668 photos + the cert.json row itself) = 37,604. Bytes: `sum(size)` of the 2,668 = 51,140,907,650. **Search of the three SSDs attached to the Mini right now (`tars`, `kipp`, `Eddy's Media Vault`; 52,869 files indexed by name+size): 0 of 2,668 found.** List: `/Volumes/Scratch1/tester/sonya6700-overwritten-2026-09-01.tsv`. `ACTION (human):` do not wipe or reformat ANY SSD or SD card until these 2,668 files are located (candidates: `noahsarc`, whatever fed the 2026-04-26 copy, the camera cards). Not my call which - I only report that as of now they are on none of the disks I can see.

6. **Item 1 (B7) - manifest coverage, from the snapshot.** Rows per disk / status (`select source_disk,status,count(*),sum(size) … group by 1,2`):
   ```
   files-kolab-videos inventoried      6      149,454,082
   media-backup       verified        30   17,429,964,547
   media-djiflip      verified       756  333,877,874,098
   media-djimini2     verified       100  118,641,550,384
   media-gopro        verified     1,134 2,458,427,802,730
   media-iphone       verified        15    1,104,781,786
   media-leantank     verified       520   31,897,100,920
   media-sonya6700    copied         195    1,154,361,035
   media-sonya6700    verified    39,219 3,652,828,329,073
   media-sonyzve10    verified    25,760 2,397,924,148,613
   total                          67,735 9,013,435,367,268  = 8.20 TiB (9,013,435,367,268 / 2^40)
   ```
   Newest `verified_at` per media disk: all 2026-09-13 except backup/leantank (2026-09-02). **The "3.3 TiB" in TEAM.md is wrong - it is `media-sonya6700` alone (3.32 TiB); the archive is 8.20 TiB.** No `nas-*` rows exist (context says they do); the only inventory-only disk is `files-kolab-videos` (6 rows, no dest). **Source SSDs are not in the manifest at all** - `source_disk` is per camera, not per physical disk; the SSD→camera mapping comes from the logs: `tars` (Apr 27, `tars-copy.log`), `noahsarc` = `/usb/sdc2` (Apr 28, `noahsarc-copy.log`: GoPro/DJIFlip/SonyA6700/SonyZVE10), `case` (Sep 1, Mini `case-copy.log`: SonyA6700/SonyZVE10/DJIFlip/Backup/LeanTank), `Eddy's Media Vault` (Sep 2, Mini `vaultdisk-copy.log`: 195 files → SonyA6700). Attached to the Mini now: `tars`, `kipp`, `Eddy's Media Vault`. `kipp` (folders Auditorium/Backup/GoPro/LeanTank/Multicam/SonyA6700/SonyZVE10) appears in no log - never copied. No row or cert names Scratch1; whether it was certified before repurposing cannot be answered from the manifest (see #9 for what the certs say).

7. **B6 will fail for `media-sonya6700` as-is: the 195 `copied` rows have `dest_path` without their directory.** Manifest says `dest_path = C2286M01_2025.XML` / `DSC04894_2025.JPG`; on disk they are `CLIP/C2286M01_2025.XML` (10) and `DCIM/DSC04894_2025.JPG` (185). Cause: `vaultdisk-copy.log` copied with dest roots `SonyA6700/CLIP` and `SonyA6700/DCIM`, so the relative path is relative to the wrong root. The Sep 13 `verify media-sonya6700 /volume1/media/SonyA6700` reported exactly these 195 as `missing` → `VERIFY FAILED` → no cert. The bytes are fine: I hashed all 195 at their real locations - **195/195 sha256 and size match the manifest** (`check195b.py`, 1,154,361,035 bytes hashed). 0 of the 195 shas exist on any `verified` row (not a dedupe case). Fix is a manifest edit (prefix `CLIP/`, `DCIM/`) - Dev/PM lane; `--only-unverified` alone will not get past it.

8. **Item 2 - liveness.** Nothing is running: no `manifest.db-wal`/`-shm` on the NAS, `manifest.db` mtime = `verify-certify.log` mtime = 2026-09-13 11:50 (NAS local). Last pass: `verify-certify.log` 2026-09-13 02:42-12:50 MDT, ended **`all done — 1 failures across 6 disks`** (sonya6700, #7). Every line in that log is written twice (script does `tee -a $LOG` under a nohup that also redirects to `$LOG`) - cosmetic, Dev. `tars-copy.log` ends Apr 27 "all tars copies done"; `prune.log` ends Apr 27 "deleted 13, freed 68,005,576,252 bytes". `docker ps` needs SSH (#10).

9. **Certificates: 6 on disk inside the trees they certify, all signatures valid, one is stale and dangerous.** `~/mounts/media/<Cam>/media-<cam>.cert.json` for all six; `certify.Verify` (the project's own code, throwaway `go run` in my checkout, since deleted) → `SIG_OK` ×6. Five are from 2026-09-13; **`media-sonya6700.cert.json` is from 2026-04-29 (37,604 files) and no longer describes the disk** (#5, #7) - anyone reading it would believe 51 GB is safely archived. Five more certs (Sep 2) sit in `docker/vault-certs/` incl. `media-backup` and `media-leantank`. Cert-vs-disk (`crosscheck.py`): DJIFlip, DJIMini2, SonyZVE10, GoPro, iPhone - 0 missing, 0 size mismatches; SonyA6700 - 2,668 size mismatches + 2,417 files on disk not in the cert. Certs living inside the archive root is the thing the hand-off warns about (a cert row blocked 39,219 files) - that already happened here: the `mismatch` row deleted on Sep 2 *was* `media-sonya6700.cert.json`.

10. **B2 - SSH: port open, key auth refused. `ACTION (human):`** `nc -z 192.168.1.167 22` → succeeded (18:12:08); `ssh -o BatchMode=yes figmaboi@192.168.1.167 id` → `Permission denied (publickey,password)`. So SSH *is* enabled but my key isn't on the NAS. Either run `ssh-copy-id -i ~/.ssh/id_ed25519.pub figmaboi@192.168.1.167` on the Mini yourself (it prompts for the SMB password; I must not), or paste `~/.ssh/id_ed25519.pub` into UGOS for `figmaboi`. `sudo`/`docker` facts wait on that. The classifier also blocks me reading the Keychain, so I cannot self-mount shares - the mount agent did it at ~18:17 once `NAS_SHARES` changed.

11. **Tagger residue extends to iPhone, not just GoPro (B20).** `GoPro/Videos/reports/` (5 dirs, 16 files, 4,648 KiB) as the hand-off says, tags decoded e.g. `GX010008.MP4` → `event, outdoors, person, street, working, writing`. But also `iPhone/Videos/reports/` - 15 dirs, 60 files, none in the manifest - and the manifest `metadata` table has 15 `scene_descriptions`/`captured_at` rows = the 15 iPhone videos. A `scan` of either root will now find files with no row. `Public/Gata Marina` = 32 files, 22,826,429,557 B, no manifest rows (B19) - confirmed.

12. **Content duplicates inside the manifest:** 3,193 sha256 groups with >1 row, 3,418 surplus rows (`select sha256,count(*) … having count>1`). For B8. Renames (`dest_path != source_path`): djiflip 401 + gopro 558 + sonya6700 195 = 1,154, not 831.

13. **Isolation breach, low stakes:** my detached worktree contained uncommitted edits to `team/TEAM.md`, `team/BACKLOG.md`, `team/channels/tester-feedback.md` and an untracked `team/tester-feedback.md` that I never made (an intermediate state of the PM's Sep 17 edits). Preserved at `/Volumes/Scratch1/tester/stray-2026-09-17/` + `git stash` in that worktree, then checkout moved to `7cca025`. Whoever wrote there: don't.

**STOPPED HERE (2026-09-17T18:24:02-07:00, Mac restarting):** all NAS work above is complete and written; nothing is running. Next: (a) after B2 key install - `id`, `sudo -n true`, `docker ps` on the NAS; (b) hash-compare the `noahsarc`/other SSDs for the 2,668 files as soon as any is attached; (c) B20/B19 write-up in `docs/testing/` if the PM wants an external-agent doc. Scratch artifacts under `/Volumes/Scratch1/tester/` are tagged test, keep until #5 is resolved.
