# tester-feedback.md - Tester (and the human) -> PM

Protocol: numbered items, newest at the bottom. The PM triages each into BACKLOG.md, a rejection with reasons, or a question back - and marks it inline. Nobody else resolves items here. Every note dated and signed. The Tester's current WORK ORDER (PM-written) sits at the top.

Boot line (Mac mini, visible terminal, Opus-class, Remote Control on): `cd ~/Projects/media-vault && claude --model opus --remote-control media-vault-tester "You are the Tester for media-vault. Read team/TEAM.md, team/actors/tester.md, then team/channels/tester-feedback.md."`

## WORK ORDER - rev 1

**PM (2026-09-16):** You are the only seat that touches the NAS, and only read-only until the PM says otherwise.

0. **`ACTION (human):` B2 - tell the Tester how to get a shell on the NAS from the Mac mini.** The plain question: when you run `scripts/nas-*.sh` on the NAS today, what do you type first to get there? Typically `ssh <user>@<nas-ip>` (UGOS: Control Panel → Terminal & SNMP → enable SSH). The Tester will set up a `nas` alias in `~/.ssh/config` once it has host + user. Until answered, items 1-3 wait.
1. **B7 - enumerate the source SSDs.** Read-only query on the manifest (`sqlite3 'file:/volume1/docker/vault-nas-config/<db>?mode=ro'`, or via a `docker run` with the config dir mounted read-only): rows per `source_disk`, status counts per disk (`copied` / `verified` / `mismatch` / `deduped` / `inventoried`), newest `verified_at` per disk. Map the six `media-*` disks to the four physical SSDs by asking the human for each SSD's label as it mounts. Note whether the repurposed "Scratch1" SSD has any rows and whether it was ever certified (`certify` output files, if any, live in the archive or config dir).
2. **Liveness glance.** Tail `/volume1/docker/tars-copy.log` and `verify-certify.log`: is anything still running (`docker ps`), when did the last pass finish, did it end `done` or `FAILED`?
3. **Set up your detached checkout** for later verification: `git worktree add --detach ../media-vault-tester main`. Never test from `~/Projects/media-vault` (the PM's checkout) or `~/Projects/media-vault-dev` (Dev's live worktree). You write only `team/channels/tester-feedback.md` and `team/DECISIONS.md` in the PM's checkout - nothing else there.

Report each as a numbered item below: outcome first, then evidence (the query and its output), then repro. Numbers with their arithmetic. Anything that looks like data loss - interrupt-level, not next-report.

## Items

(none yet)
