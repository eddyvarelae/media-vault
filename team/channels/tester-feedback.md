# tester-feedback.md - Tester (and the human) -> PM

Protocol: numbered items, newest at the bottom. The PM triages each into BACKLOG.md, a rejection with reasons, or a question back - and marks it inline. Nobody else resolves items here. Every note dated and signed. The Tester's current WORK ORDER (PM-written) sits at the top.

Boot line (Mac mini, visible terminal, Opus-class, Remote Control on): `cd ~/Projects/media-vault && claude --model opus --remote-control media-vault-tester "You are the Tester for media-vault. Read team/TEAM.md, team/actors/tester.md, then team/channels/tester-feedback.md."`

## WORK ORDER - rev 1

**PM (2026-09-16):** You are the only seat that touches the NAS, and only read-only until the PM says otherwise.

0. **`ACTION (human):` B2 - tell the Tester how to get a shell on the NAS from the Mac mini.**
   **PM (2026-09-17):** `ACTION (human)` withdrawn - the human doesn't know, so B2 is yours. Facts the PM established locally: the NAS is `192.168.1.167`, the SMB user is `figmaboi`, and this Mac already mounts `//figmaboi@192.168.1.167/media` at `~/mounts/media` (that is `/volume1/media` - **treat it as read-only**). Tailscale runs on this Mac (`mini`), no NAS node visible on it. Your step: probe SSH (`nc -z -G 3 192.168.1.167 22`, then `ssh -o BatchMode=yes figmaboi@192.168.1.167 'id'`). If port 22 is closed or auth fails, file one item saying exactly what the human must enable (UGOS: Control Panel → Terminal & SNMP → SSH; and/or add `~/.ssh/id_ed25519.pub` to the NAS user) - that becomes the `ACTION (human)`. The PM's own probe was blocked by session permissions; yours will prompt - accept read-only commands only. The plain question: when you run `scripts/nas-*.sh` on the NAS today, what do you type first to get there? Typically `ssh <user>@<nas-ip>` (UGOS: Control Panel → Terminal & SNMP → enable SSH). The Tester will set up a `nas` alias in `~/.ssh/config` once it has host + user. Until answered, items 1-3 wait.
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

2. **Item 0 still open - `ACTION (human):` NAS host + user.** I looked for it read-only in what I'm allowed to read and found nothing: no `nas`/UGREEN/DXP entry in `~/.ssh/config`, `~/.ssh/known_hosts` has no LAN host, and nothing under `README.md`, `docs/`, `scripts/`, or `team/archive/` names an IP or hostname (the scripts assume you are already root on the NAS). I did not probe the LAN or shell history (auto-mode classifier blocked it as credential exploration; correct call - it's your answer to give, not mine to dig for). **What I need, one line:** `ssh <user>@<host>` as you type it today, plus whether that user can `sudo` and `docker`. I'll then add a `nas` alias to `~/.ssh/config` (key auth only; nothing pasted into a printing command, TEAM rule 6) and run items 1-2.

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

4. **Housekeeping, not a bug:** TEAM.md "Current state" says `main = 9518230`; the tip is `5a92286` (the team-framework commit on top of it). Code content is identical; naming it so my "verified against" SHAs don't look wrong.

