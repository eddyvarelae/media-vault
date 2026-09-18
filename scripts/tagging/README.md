# Nightly video tagging (B17)

`run-tagging.sh` is the 02:00 job that tags the archive's videos: pull one
file at a time from the NAS to scratch, run [video-tagger](https://github.com/eddyvarelae/video-tagger)
on the scratch copy, write the Finder tags, the `com.videotagger.processed`
marker and `reports/<stem>/` back next to the NAS file, read them back, delete
the scratch copy. `tagging-helper.py` (stdlib Python, run with the tagger's
venv) does the parts that are unpleasant in bash: the manifest snapshot,
batch selection, the per-file state db, write-back with read-back.

Ported byte-for-byte from mini-server `5e7bac7` (commit 1 of the transfer),
then changed here - `git log -- scripts/tagging` shows exactly what.

## What it does, in the decided order (team/DECISIONS.md 2026-09-17)

- **Newest first, whole archive:** rows ordered by `copied_at` desc; no
  watermark. "Pending" = not in the job's own done set.
- **Tier 1 before tier 2:** `SonyA6700 SonyZVE10 GoPro DJIFlip DJIMini2 iPhone`
  first; `Backup LeanTank Public` only when tier 1 has nothing pending. A tier-2
  folder the manifest has no rows for (`Public`) is walked - a directory
  listing, newest mtime first, never a read of contents; `reports/`, dotfiles
  and hidden dirs are skipped.
- **Cap 200 GB per night** (always at least one file); `--limit N` for tests.
- **Only `verified` rows.** A `copied` row's bytes are unproven; it is tagged
  the night after `verify` promotes it. A row with an empty `dest_path` is
  located at `<folder>/source_path` - media-vault's own rule (verify,
  repair-dest, restore) - so the 605 B39 rows are reachable.
- `#recycle` is never a source; a folder may not be in both tiers.

## Two owners, two kinds of setting

The **machine** is mini-server's: `SCRATCH_DIR`, `MEDIA_ROOT`, `MANIFEST_DB`,
`OLLAMA_URL`, `TAGGER_DIR`, `TAG_STATE_DIR`, `LOG_FILE` come from its
`config/mini.env` (path in `MINI_ENV`, default
`~/Projects/mini-server/config/mini.env`). The **job policy** is ours:
`TAG_SOURCES`, `TAG_SOURCES_TIER2`, `TAG_BATCH_MAX_GB`, `TAG_VIDEO_EXTS`
default in the script to the decisions above. `mini.env` may override a
policy key - every such override is logged as
`POLICY OVERRIDE: KEY=[value] (default [...], from <file>)` at the start of
the run and in `--dry-run`, so a night that tagged the wrong folders says
why. The environment overrides both (that is how the tests run it).

## The manifest is never opened where it lives

The live manifest is WAL sqlite on the NAS docker share
(`~/mounts/docker/vault-nas-config/manifest.db`). Opening it over SMB writes a
`-shm` next to it and can read a torn WAL. Every run first copies db + wal to
`$TAG_STATE_DIR/manifest.snapshot.db`, checkpoints and `quick_check`s the copy
(one retry, then exit 2), and reads only the snapshot, `mode=ro`. The log
says `manifest snapshot: N rows, newest copied_at <date>, NAS file modified
<date>` - "as of", not "live". A dry run snapshots to a temp dir and leaves
no state.

## Exit codes, log, lock

`0` all good · `1` some file failed, or the lock is held · `2` a precondition
failed (nothing attempted; the message says which). Every outcome is one
`SUMMARY run=… selected N, pulled N, tagged N, wrote back N, failed N, still
pending → tier 1 N, tier 2 N` line in `$LOG_FILE`. A failed file keeps its
scratch copy for inspection and is re-selected the next night. Single
instance via a `mkdir` lock; a lock from a dead process is taken over, a
stale one (≥24 h) is reported, never cleared. `SIGTERM` stops the child, counts
the file in flight as failed, releases the lock, summarises, exits 143.

## Running it

    run-tagging.sh --dry-run        # the batch a real run would take; touches nothing
    run-tagging.sh --limit 3        # a real run, three files (the Tester's fixture size)
    run-tagging.sh                  # the real thing; launchd runs this at 02:00

`com.varela.video-tagger.plist.example` is the LaunchAgent (B18): it points at
the PM's checkout on `main`, so a merge is what changes the job.

## Tests

`scripts/test/run-tagging.sh` (under `go test ./scripts/test/`) runs the job
against a **real** manifest built by this repo's own `vault copy` / `verify`,
with real xattrs and rsync; `mount`, `df`, `curl` and the tagger venv are
stubs, and a fake `tagger.py` tags, fails or hangs on demand. 71 checks:
every precondition, dry-run isolation, newest-first and tier order, the
`copied` and empty-`dest_path` rules, policy overrides and their log line, a
torn snapshot, real runs with write-back and merged Finder tags, a failing
file, an interrupted file, the lock, tier 2 with a walked folder.

Never run the tests with `MEDIA_ROOT` pointing at the real mount: they write
xattrs and `reports/` next to every file they tag.
