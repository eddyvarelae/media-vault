#!/usr/bin/env python3
"""Helper for scripts/tagging/run-tagging.sh — the parts that are unpleasant in bash.

Five concerns, one file, so the shell script stays a readable orchestrator:

  snapshot   copy the NAS manifest (db + wal) to a local snapshot and prove
             it opens clean — the only way this job ever reads the manifest
  select     pick tonight's batch from the snapshot (and a disk walk for
             folders the manifest does not know), newest first
  record     remember a per-file outcome in the job's own state db
  pending    count what is still unprocessed per tier (for the summary line)
  writeback  copy Finder tags + marker + report from the scratch copy to the
             NAS file, and verify they landed

Selection order (Eddy's B3 decisions, DECISIONS.md 2026-09-17): newest first
across the whole archive, tier 1 (the six camera folders) before tier 2
(Backup, LeanTank, Public). Tier 2 is only touched when tier 1 has nothing
pending; within a tier, newest copied_at (or file mtime, for walked folders)
first. There is no watermark: "pending" is simply "not in the done set", so
fresh footage always sorts to the top and the historical backlog drains
behind it.

State is a per-file done set. A night can fail on one file and succeed on the
next twenty; every finished file is recorded by id and selection skips those.
A file that keeps failing is re-selected (and reported) every night until
someone looks at it; it costs one file's worth of work, not the night's.

Ids: manifest rows keep their manifest id (positive). A tier-2 folder with no
manifest rows at all (Public, as of 2026-09-17) is walked instead — a plain
directory listing, never a hash of contents — and each file gets a stable
synthetic id: the negated top 63 bits of sha1("<folder>/<relative path>").
Deterministic across runs, needs no lookup table, and the sign says which
world a row came from (the `source` column says it in words). A renamed file
is a new id and gets tagged again, same as a re-copied manifest row would.

The manifest is never opened where it lives: it is WAL-mode sqlite on an SMB
share, and opening that directly writes a -shm file next to it and can read a
torn WAL. `snapshot` rsyncs db + wal to $TAG_STATE_DIR, checkpoints the copy,
and quick_checks it (a fresh copy each run, never an rsync-skipped stale
one); everything else reads the snapshot with mode=ro.
Copied-at values are unix nanoseconds, as media-vault stores them.

Run with the video-tagger venv python (or any python >= 3.9); stdlib only.
"""
import argparse
import datetime as dt
import hashlib
import json
import os
import plistlib
import shutil
import sqlite3
import subprocess
import sys

TAGS_XATTR = "com.apple.metadata:_kMDItemUserTags"
MARKER_XATTR = "com.videotagger.processed"   # tagger.py's own "done" marker


# ── state db ──────────────────────────────────────────────────────────────

SCHEMA = """
  CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
  CREATE TABLE IF NOT EXISTS files (
    id          INTEGER PRIMARY KEY,      -- manifest id, or negative synthetic id for walked files
    source      TEXT    NOT NULL DEFAULT 'manifest',   -- manifest | walk
    camera      TEXT    NOT NULL,         -- NAS top-level folder
    dest_path   TEXT    NOT NULL,         -- path relative to that folder
    size        INTEGER NOT NULL,
    copied_at   INTEGER NOT NULL,         -- manifest copied_at, or file mtime for walked rows (ns)
    status      TEXT    NOT NULL,         -- done | failed
    attempts    INTEGER NOT NULL DEFAULT 0,
    tags        TEXT,                     -- JSON list, when done
    last_error  TEXT,
    run_id      TEXT,
    updated_at  TEXT    NOT NULL
  );
"""


def migrate(c):
    """rev 1 (909db8b) → rev 3: key column renamed, `source` added, watermark gone."""
    cols = {r[1] for r in c.execute("PRAGMA table_info(files)")}
    if "manifest_id" in cols:
        c.execute("ALTER TABLE files RENAME COLUMN manifest_id TO id")
    if "source" not in cols:
        c.execute("ALTER TABLE files ADD COLUMN source TEXT NOT NULL DEFAULT 'manifest'")
    c.execute("DELETE FROM meta WHERE key='watermark'")
    c.commit()


def open_state(path, readonly=False):
    if readonly:
        if not os.path.exists(path):
            return None
        return sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    c = sqlite3.connect(path)
    c.executescript(SCHEMA)
    migrate(c)
    return c


def done_ids(state):
    if state is None:
        return set()
    return {r[0] for r in state.execute("SELECT id FROM files WHERE status='done'")}


def meta_get(state, key):
    if state is None:
        return None
    row = state.execute("SELECT value FROM meta WHERE key=?", (key,)).fetchone()
    return row[0] if row else None


def ns_to_iso(ns):
    if not ns:
        return "unknown"
    return dt.datetime.fromtimestamp(ns / 1e9, tz=dt.timezone.utc).astimezone().isoformat(timespec="seconds")


# ── manifest snapshot ─────────────────────────────────────────────────────

def open_snapshot(path):
    if not os.path.exists(path):
        sys.exit(f"manifest snapshot not found: {path}")
    return sqlite3.connect(f"file:{path}?mode=ro", uri=True)


def take_snapshot(src, dst):
    """rsync db (+ -wal if present) to dst, checkpoint the copy, quick_check it.

    The -shm is deliberately not copied: it is sqlite's per-host wal-index,
    rebuilt from the wal on open, and one carried over from the NAS is at best
    ignored. A stale one next to the snapshot is removed for the same reason.
    Returns (source mtime ns, row count, newest copied_at ns).
    """
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    # Remove the previous snapshot and its sidecars first. rsync -a decides
    # to copy by size+mtime, and a manifest edited within the same second as
    # the last snapshot, reusing pages so the size is unchanged, would be
    # skipped - a stale snapshot that silently misses the newest rows. A
    # fresh dst is always written in full.
    for suffix in ("", "-shm", "-wal"):
        try:
            os.remove(dst + suffix)
        except FileNotFoundError:
            pass
    src_mtime = os.stat(src).st_mtime_ns
    pairs = [(src, dst)]
    if os.path.exists(src + "-wal"):
        pairs.append((src + "-wal", dst + "-wal"))
    for a, b in pairs:
        subprocess.run(["rsync", "-a", a, b], check=True)
    # Checkpoint: fold the wal into the db and leave the snapshot as a plain
    # rollback-journal file, so the read-only opens below never need a wal.
    # A wal torn mid-copy is safe here: sqlite checksums each frame and stops
    # at the first bad one, i.e. the snapshot is simply a little older.
    c = sqlite3.connect(dst)
    c.execute("PRAGMA journal_mode=DELETE")
    (check,) = c.execute("PRAGMA quick_check").fetchone()
    if check != "ok":
        c.close()
        raise RuntimeError(f"snapshot failed quick_check: {check}")
    rows, newest = c.execute("SELECT COUNT(*), COALESCE(MAX(copied_at), 0) FROM files").fetchone()
    c.close()
    return src_mtime, rows, newest


def cmd_snapshot(a):
    """Refresh the snapshot. Prints `ok <rows> <newest copied_at> <source mtime>`,
    then a WARN line if the NAS manifest has not changed since the last run
    started (new footage would not be indexed yet — not fatal)."""
    try:
        src_mtime, rows, newest = take_snapshot(a.manifest, a.snapshot)
    except (subprocess.CalledProcessError, RuntimeError, sqlite3.DatabaseError) as e:
        # Transient (a copy raced a NAS-side write): one rerun, then give up.
        print(f"snapshot attempt 1 failed ({e}); retrying once", file=sys.stderr)
        src_mtime, rows, newest = take_snapshot(a.manifest, a.snapshot)
    print(f"ok {rows} {ns_to_iso(newest)} {ns_to_iso(src_mtime)}")
    state = open_state(a.state, readonly=True)
    last = meta_get(state, "last_run_started_ns")
    if last and src_mtime <= int(last):
        print(f"WARN: the NAS manifest ({a.manifest}) has not changed since the last run started "
              f"({ns_to_iso(int(last))}) — anything copied since then is not indexed yet")


# ── candidates: manifest rows + walked folders ────────────────────────────

VIDEO_STATUS = "verified"   # only rows media-vault has hash-checked against the NAS copy


JUNK_COMPONENTS = {"reports", "#recycle", ".DS_Store", ".AppleDouble", ".Trashes", ".Spotlight-V100", ".fseventsd"}


def safe_rel(rel):
    """A candidate path is used twice - appended to the camera folder on the
    NAS and to the run dir on scratch - and written through (tags, marker,
    reports/). So it must stay inside both: relative, no empty or `..`
    component, no absolute form; and it must not be our own output or
    platform junk: no `reports` component, no hidden or AppleDouble name,
    never the NAS trash. Applies to manifest rows (dest_path or the
    source_path fallback) exactly as to walked files - a manifest spelling
    is data, not trust (review #26)."""
    if not rel or os.path.isabs(rel) or rel.startswith(("/", "\\")):
        return False
    parts = rel.replace("\\", "/").split("/")
    for c in parts:
        if c in ("", ".", ".."):
            return False
        if c in JUNK_COMPONENTS or c.startswith(".") or c.startswith("._"):
            return False
    return True


def disk_of(folder):
    """media-vault names a folder's source disk `media-<folder lowercased>`
    (`media-sonya6700` -> `SonyA6700`) and stores dest_path relative to the
    folder, so the NAS path is `<MEDIA_ROOT>/<folder>/<dest_path>`.
    Checked 2026-09-15 across all 7,110 tier-1 video rows and 2026-09-17
    across the 140 Backup + LeanTank rows: 0 missing."""
    return "media-" + folder.lower()


def manifest_rows(man, folders, exts):
    """(id, folder, dest_path, size, copied_at, 'manifest') for every video row
    of the given folders. Ordering is applied by the caller."""
    if not folders:
        return []
    by_disk = {disk_of(f): f for f in folders}
    # A row with an empty dest_path locates its file at <folder>/source_path
    # - media-vault's own rule (verify, repair-dest, restore) - so the same
    # expression names the file here. 605 media-sonya6700 rows are like that.
    ext_clauses = " OR ".join("lower(COALESCE(NULLIF(dest_path,''), source_path)) LIKE ?" for _ in exts)
    sql = (
        "SELECT id, source_disk, COALESCE(NULLIF(dest_path,''), source_path), size, copied_at FROM files "
        f"WHERE status=? AND source_disk IN ({','.join('?' for _ in by_disk)}) "
        f"AND ({ext_clauses})"
    )
    params = [VIDEO_STATUS] + list(by_disk) + ["%" + e.lower() for e in exts]
    out = []
    for i, d, dp, sz, ca in man.execute(sql, params):
        if not safe_rel(dp):
            UNSAFE.append((by_disk[d], dp))
            continue
        out.append((i, by_disk[d], dp, sz, ca, "manifest"))
    return out


UNSAFE = []   # (folder, path) of rows refused by safe_rel this run - announced, never silently dropped


def folders_without_rows(man, folders):
    """Tier-2 folders the manifest has never heard of (any status) get walked."""
    out = []
    for f in folders:
        (n,) = man.execute("SELECT COUNT(*) FROM files WHERE source_disk=?", (disk_of(f),)).fetchone()
        if n == 0:
            out.append(f)
    return out


def walk_id(folder, rel):
    h = hashlib.sha1(f"{folder}/{rel}".encode("utf-8", "surrogateescape")).digest()
    return -(int.from_bytes(h[:8], "big") >> 1) or -1


def walk_rows(media_root, folder, exts):
    """(id, folder, rel_path, size, mtime_ns, 'walk') for every video under
    the folder. A directory listing over SMB — cheap for the ~32 files this
    exists for — never a read of file contents. Skips dotfiles (AppleDouble
    `._*` and `.DS_Store` on SMB), hidden dirs, and `reports/` (our own
    output next to tagged files)."""
    root = os.path.join(media_root, folder)
    exts = tuple(e.lower() for e in exts)
    out = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if not d.startswith(".") and d != "reports")
        for name in filenames:
            if name.startswith(".") or not name.lower().endswith(exts):
                continue
            full = os.path.join(dirpath, name)
            rel = os.path.relpath(full, root)
            if not safe_rel(rel) or os.path.islink(full):
                UNSAFE.append((folder, rel))
                continue
            st = os.stat(full)
            out.append((walk_id(folder, rel), folder, rel, st.st_size, st.st_mtime_ns, "walk"))
    return out


def newest_first(rows):
    return sorted(rows, key=lambda r: (r[4], r[0]), reverse=True)


def pending_by_tier(man, done, a):
    """Two lists, each newest first, of rows not yet done."""
    t1 = [r for r in newest_first(manifest_rows(man, a.tier1, a.exts)) if r[0] not in done]
    walked = folders_without_rows(man, a.tier2)
    t2_rows = manifest_rows(man, [f for f in a.tier2 if f not in walked], a.exts)
    for f in walked:
        t2_rows += walk_rows(a.media_root, f, a.exts)
    t2 = [r for r in newest_first(t2_rows) if r[0] not in done]
    return t1, t2, walked


# ── subcommands ───────────────────────────────────────────────────────────

def cmd_select(a):
    """Print the batch as TSV: id, folder, rel_path, size, ts_ns, source."""
    man = open_snapshot(a.snapshot)
    state = open_state(a.state, readonly=True)
    done = done_ids(state)
    t1, t2, walked = pending_by_tier(man, done, a)
    if a.tier2_only:
        tier, rows = 2, t2
    elif t1:
        tier, rows = 1, t1
    else:
        tier, rows = 2, t2
    picked, total = [], 0
    for row in rows:
        size = row[3]
        # Always take at least one file, otherwise a single clip bigger than
        # the cap would block the queue forever.
        if picked and total + size > a.max_bytes:
            break
        picked.append(row)
        total += size
        if a.limit and len(picked) >= a.limit:
            break
    for row in picked:
        print("\t".join(str(x) for x in row))
    walked_note = f" (walked: {' '.join(walked)})" if walked and tier == 2 else ""
    unsafe_note = f"; skipped {len(UNSAFE)} candidate(s) with unsafe or junk paths (first: {UNSAFE[0][0]}/{UNSAFE[0][1]})" if UNSAFE else ""
    print(f"selected {len(picked)} files, {total / 1e9:.2f} GB from tier {tier}{walked_note}; "
          f"pending before this run: tier 1 {len(t1)}, tier 2 {len(t2)}{unsafe_note}", file=sys.stderr)


def cmd_record(a):
    state = open_state(a.state)
    now = dt.datetime.now().astimezone().isoformat(timespec="seconds")
    state.execute(
        """INSERT INTO files (id, source, camera, dest_path, size, copied_at, status,
                              attempts, tags, last_error, run_id, updated_at)
           VALUES (?,?,?,?,?,?,?,1,?,?,?,?)
           ON CONFLICT(id) DO UPDATE SET
             status=excluded.status, attempts=attempts+1, tags=excluded.tags,
             last_error=excluded.last_error, run_id=excluded.run_id,
             updated_at=excluded.updated_at""",
        (a.id, a.source, a.camera, a.dest_path, a.size, a.copied_at, a.status,
         a.tags or None, a.error or None, a.run_id, now))
    state.commit()


def cmd_run_started(a):
    """Stamp the run in meta (the snapshot WARN compares against it)."""
    state = open_state(a.state)
    now_ns = dt.datetime.now().timestamp() * 1e9
    state.executemany("INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)",
                      [("last_run_started_ns", str(int(now_ns))), ("last_run_id", a.run_id)])
    state.commit()


def cmd_pending(a):
    """Print `tier 1 N, tier 2 N` — what is still unprocessed after this run."""
    man = open_snapshot(a.snapshot)
    done = done_ids(open_state(a.state, readonly=True))
    t1, t2, _ = pending_by_tier(man, done, a)
    print(f"tier 1 {len(t1)}, tier 2 {len(t2)}")


# ── write-back ────────────────────────────────────────────────────────────

def read_tags(path):
    """Finder tags as a dict name -> raw plist entry ("name\\ncolor" or "name")."""
    r = subprocess.run(["xattr", "-p", "-x", TAGS_XATTR, path], capture_output=True)
    if r.returncode != 0:
        return {}
    raw = bytes.fromhex(r.stdout.decode().replace(" ", "").replace("\n", ""))
    try:
        entries = plistlib.loads(raw)
    except Exception:
        return {}
    return {str(e).split("\n")[0]: str(e) for e in entries}


def has_xattr(path, name):
    return subprocess.run(["xattr", "-p", name, path], capture_output=True).returncode == 0


def cmd_writeback(a):
    """scratch copy -> NAS: tags (merged with whatever the NAS file already
    carries), the processed marker, and the report dir. Verifies by reading
    back. Prints the tag names as JSON on success."""
    if not has_xattr(a.src, MARKER_XATTR):
        sys.exit("tagger did not mark the scratch copy processed — treating as failed")
    report_json = os.path.join(a.report_src, "report.json")
    if not os.path.isfile(report_json):
        sys.exit(f"no report at {report_json}")

    new = read_tags(a.src)                  # may be empty: "no tags matched" is a valid outcome
    merged = read_tags(a.dst)
    merged.update(new)                      # scratch entry wins on a name collision (carries colour)

    if merged:
        blob = plistlib.dumps(sorted(merged.values()), fmt=plistlib.FMT_BINARY)
        subprocess.run(["xattr", "-wx", TAGS_XATTR, blob.hex(), a.dst], check=True)
    subprocess.run(["xattr", "-w", MARKER_XATTR, "1", a.dst], check=True)

    # Report: rewrite `path` to where the video actually lives, then copy the
    # whole report dir (report.json + frame_N.jpg) next to the NAS file.
    with open(report_json) as f:
        report = json.load(f)
    report["path"] = a.dst
    with open(report_json, "w") as f:
        json.dump(report, f, indent=2)
    os.makedirs(a.report_dst, exist_ok=True)
    for name in os.listdir(a.report_src):
        shutil.copy2(os.path.join(a.report_src, name), os.path.join(a.report_dst, name))

    # Verify — every tag we meant to write is on the NAS file, the marker is
    # there, and the report round-trips with the same size for every file.
    back = read_tags(a.dst)
    missing = [t for t in new if t not in back]
    if missing:
        sys.exit(f"tags did not land on {a.dst}: missing {missing}")
    if not has_xattr(a.dst, MARKER_XATTR):
        sys.exit(f"marker did not land on {a.dst}")
    for name in os.listdir(a.report_src):
        s, d = os.path.join(a.report_src, name), os.path.join(a.report_dst, name)
        if not os.path.isfile(d) or os.path.getsize(d) != os.path.getsize(s):
            sys.exit(f"report file did not land: {d}")
    with open(os.path.join(a.report_dst, "report.json")) as f:
        if json.load(f).get("path") != a.dst:
            sys.exit(f"report.json on NAS does not point at {a.dst}")
    print(json.dumps(sorted(new)))


# ── main ──────────────────────────────────────────────────────────────────

def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="cmd", required=True)

    def selection_args(sp):
        sp.add_argument("--state", required=True, help="path to tagging-state.db")
        sp.add_argument("--snapshot", required=True, help="local manifest snapshot (see `snapshot`)")
        sp.add_argument("--tier1", required=True, help="space-separated camera folders, processed first")
        sp.add_argument("--tier2", default="", help="space-separated folders, only when tier 1 is drained")
        sp.add_argument("--exts", required=True, help="space-separated extensions, with dots")
        sp.add_argument("--media-root", required=True, help="~/mounts/media — walked for folders the manifest lacks")

    s = sub.add_parser("snapshot")
    s.add_argument("--state", required=True)
    s.add_argument("--manifest", required=True, help="the NAS manifest.db (WAL, over SMB — copied, never opened)")
    s.add_argument("--snapshot", required=True, help="where the local copy goes")
    s.set_defaults(fn=cmd_snapshot)

    s = sub.add_parser("select"); selection_args(s)
    s.add_argument("--max-bytes", type=int, required=True)
    s.add_argument("--limit", type=int, default=0)
    s.add_argument("--tier2-only", action="store_true", help="debug: pretend tier 1 is drained")
    s.set_defaults(fn=cmd_select)

    s = sub.add_parser("pending"); selection_args(s)
    s.set_defaults(fn=cmd_pending)

    s = sub.add_parser("record")
    s.add_argument("--state", required=True)
    s.add_argument("--id", type=int, required=True)
    s.add_argument("--source", choices=["manifest", "walk"], required=True)
    s.add_argument("--camera", required=True)
    s.add_argument("--dest-path", required=True)
    s.add_argument("--size", type=int, required=True)
    s.add_argument("--copied-at", type=int, required=True)
    s.add_argument("--status", choices=["done", "failed"], required=True)
    s.add_argument("--tags", default=None, help="JSON list")
    s.add_argument("--error", default=None)
    s.add_argument("--run-id", required=True)
    s.set_defaults(fn=cmd_record)

    s = sub.add_parser("run-started")
    s.add_argument("--state", required=True)
    s.add_argument("--run-id", required=True)
    s.set_defaults(fn=cmd_run_started)

    s = sub.add_parser("writeback")
    s.add_argument("--src", required=True, help="scratch copy (tagged)")
    s.add_argument("--dst", required=True, help="NAS file")
    s.add_argument("--report-src", required=True, help="reports/<stem>/ on scratch")
    s.add_argument("--report-dst", required=True, help="reports/<stem>/ on the NAS")
    s.set_defaults(fn=cmd_writeback)

    a = p.parse_args()
    if hasattr(a, "tier1"):
        a.tier1 = a.tier1.split()
        a.tier2 = a.tier2.split()
        a.exts = a.exts.split()
        if not a.tier1 or not a.exts:
            sys.exit("--tier1 and --exts must be non-empty")
        if set(a.tier1) & set(a.tier2):
            sys.exit(f"a folder is in both tiers: {sorted(set(a.tier1) & set(a.tier2))}")
    a.fn(a)


if __name__ == "__main__":
    main()
