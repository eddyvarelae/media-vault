# Attached-SSD gap report (B22)

`run-backup.sh` is the five-minute launchd tick that answers, for each known
source SSD plugged into the Mini, **what on it is not in the archive** - by
content, never by name. It is report-only (decided 2026-09-15): it copies
nothing, stages nothing, and never writes to the SSD or the NAS.

## What a tick does

1. Lists `/Volumes` (overridable with `VOLUMES_DIR`), drops the boot volume
   and the scratch volume by device, drops `BACKUP_EXCLUDE` (`Scratch1`),
   and keeps the volumes whose name is in `BACKUP_DISKS` - the decided
   allow-list `tars|kipp|case|Eddy's Media Vault` (`|`-separated, matched whole). An unknown mounted volume is
   logged once and ignored.
2. A known disk is **due** when it has not been reported for its current
   attach identity (device + inode + birth of the mount point: a re-attach
   is a new one) and today. (Unknown volumes are keyed by name only: a
   re-attach is logged again only when a tick in between observed the
   detachment - a mount generation is not reliable from `/Volumes` alone,
   so a detach+remount entirely between two ticks is not detected.) Nothing due → exit 0, silent: 288 ticks a day
   must not write 288 lines.
3. Preconditions (only when something is due, logged at most once an hour):
   the NAS manifest is reachable at `MANIFEST_DB`.
4. A `vault` binary: `VAULT_BIN` if given; otherwise the job's own copy at
   `$STATE_DIR/bin/vault`, (re)built with `go build` from this checkout
   when missing or when its stamp differs from the checkout's `HEAD` - so a
   merge to `main` is what changes the job, as with the tagger.
5. The manifest is **snapshotted, never opened in place** (it is WAL sqlite
   on an SMB share): `scripts/tagging/tagging-helper.py snapshot` copies db +
   wal into `$STATE_DIR/gap-manifest/manifest.db`, checkpoints and
   `quick_check`s it; a torn copy is refused (exit 2). The log says
   `manifest snapshot: N rows, newest copied_at …` - "as of", not "live".
6. For each due disk: `VAULT_CONFIG=$STATE_DIR/gap-manifest vault gap
   <mount> --tsv <state>/gap-<hexname>-<date>.tsv`, output to
   `<state>/gap-<hexname>-<date>.txt`, and one line in the log:

       GAP tars needs archiving: yes, 9872 files, 1921695784449 bytes (of 10393 files / … on the disk; 521 files / … archived by content; 521 files / … hashed to prove it; check 521+9872=10393)

   The arithmetic is on the line so the number can be recomputed. Then the
   disk is recorded as reported (`backup-state.tsv`).

`vault gap` builds a size index from the **verified** rows only, hashes a
file only when some verified row has its size, and never reads a file whose
size no row has. A disk whose content is entirely archived is therefore
fully hashed to prove it - the Tester measured ~800 MB/s on the Mini, i.e.
~26 min for 1.3 TB. The report says how many bytes were hashed.

The `<hexname>` in the output paths is a **bounded** slug: the hex of the
disk name's first 24 bytes then 16 hex of its whole sha256 (~65 chars,
`[0-9a-f]` only - so `Disk` and `disk` never share a file on a
case-folding state dir, and a long name never overruns the filename limit).
It is collision-resistant, **not injective**: two names collide only if their
24-byte heads match *and* their sha256 clashes in 16 hex (64 bits) - about
1 in 1.8e19, negligible but not impossible. So **line 1 of every slug-keyed
file is the full name** - the report (`disk: <name>`), the TSV
(`disk: <name>`) and the unknown-volume marker (`volume: <name>`).

**Collision detection runs first.** Before anything is written, pruned or
recorded, the tick checks every slug-keyed file it would touch - each due
disk's report and TSV, each unknown volume's marker - for a foreign owner on
line 1. If *any* names a different volume, it logs every collision
(`SLUG COLLISION: <file> names "<owner>", not "<expected>" …`), **touches
nothing** (the colliding files stay, no report or marker is written, no state
is appended), and **exits 1**. Only a wholly collision-free tick goes on to
write reports/markers/state and to prune. Pruning is by **owner**: a marker
is removed when the volume named on its line 1 is no longer
mounted-and-unknown. (Because a collision fails the tick before pruning, a
slug clash can never let owner-pruning drop the wrong marker.) A `--force`
re-report of the *same* disk matches its own line 1 and is replaced normally.

A configured `BACKUP_DISKS` name longer than 255 bytes - which no mount point
can be - is refused at discovery (exit 2), before any report path is built and
before any log call (the log directory is not created until a disk is due).
`BACKUP_SLUG_HOOK`, if set to a command, computes the slug in place of the
built-in - a test seam for forcing two names onto one slug.

## Two owners, two kinds of setting

Machine settings (`SCRATCH_DIR`, `MANIFEST_DB`, `BACKUP_STATE_DIR`,
`BACKUP_LOG_FILE`, `VOLUMES_DIR`, `VAULT_BIN`, `REPO_DIR`) come from
mini-server's `config/mini.env` (`MINI_ENV`, default
`~/Projects/mini-server/config/mini.env`). Job policy (`BACKUP_DISKS`,
`BACKUP_EXCLUDE`) defaults in the script; an override from `mini.env` is
applied and logged as `POLICY OVERRIDE: KEY=[value] (default […], from
<file>)`. The environment overrides both.

## Running it

    run-backup.sh --dry-run    # what is due now, what is ignored; no snapshot, no state, no report
    run-backup.sh --force      # report every known mounted disk again
    run-backup.sh              # the tick

Exit 0: nothing due, or every due report written · 1: a report failed (or a
tick is already running) · 2: a precondition failed with a disk due.
`com.varela.media-backup.plist.example` is the LaunchAgent (`StartInterval`
300 s); installing it is one act, like B18.

## Tests

`scripts/test/run-backup.sh` (under `go test ./scripts/test/`): a real
manifest built by `vault` itself, a fake `/Volumes` with known, unknown,
excluded and space-named volumes, a `df` stub that puts each on its own
device; dry run, preconditions once an hour, one report per due disk with
the arithmetic checked, the volumes byte-identical afterwards, silent
second tick, `--force`, re-attach, new day, policy override, the build into
the state dir, a torn snapshot. `internal/gap` tests the content rule.
