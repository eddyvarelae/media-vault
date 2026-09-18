#!/bin/bash
# Tests scripts/tagging/run-tagging.sh + tagging-helper.py (B17) against a
# REAL manifest - built by this repo's own `vault copy` / `vault verify` on
# temp dirs, so the schema, statuses and copied_at ordering are the real
# thing - with real xattrs (APFS temp dirs take them) and real rsync. What
# is stubbed is the machine: `mount` and `df` (the scratch/mount
# preconditions), `curl` (Ollama), and the video-tagger venv + tagger.py (a
# fake that tags, fails or hangs on demand). Nothing outside $work is touched.
# Needs VAULT_BIN (the Go wrapper builds it). Safe to run anywhere.
set -u

here=$(cd "$(dirname "$0")" && pwd)
script="$here/../tagging/run-tagging.sh"
helper="$here/../tagging/tagging-helper.py"
vault="${VAULT_BIN:?set VAULT_BIN to a built vault binary}"
work=$(mktemp -d "${TMPDIR:-/tmp}/tagging-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
export HOME="$work/home"     # the script's defaults live under $HOME; none of them may be real
mkdir -p "$HOME"

failures=0
check() { # check <description> <command...>
  local desc=$1; shift
  if "$@"; then echo "  ok   $desc"; else echo "  FAIL $desc"; failures=$((failures + 1)); fi
}
count() { grep -cF -- "$1" "$2" || true; }

# ── the machine: stubs on PATH ────────────────────────────────────────────
media="$work/media"; scratch="$work/scratch"; cfg="$work/cfg"; state="$work/state"; logf="$work/log/video-tagger.log"
mkdir -p "$work/bin" "$media" "$scratch" "$cfg" "$work/log"
cat > "$work/bin/mount" <<STUB
#!/bin/bash
echo "//nas/media on $media (smbfs, nodev, nosuid, mounted by varela)"
STUB
cat > "$work/bin/df" <<STUB
#!/bin/bash
# df -P <path>: the scratch dir is on its own volume, everything else on the boot disk
p="\${2:-\$1}"
echo "Filesystem 512-blocks Used Available Capacity Mounted on"
case "\$p" in
  $scratch*) echo "/dev/scratch 1 1 1 1% $scratch" ;;
  *)        echo "/dev/boot 1 1 1 1% /" ;;
esac
STUB
cat > "$work/bin/curl" <<'STUB'
#!/bin/bash
[ -n "${OLLAMA_DOWN:-}" ] && exit 22
echo '{"models":[{"name":"llava:latest"}]}'
STUB
chmod +x "$work/bin/"*
# The fake video-tagger: a venv python that admits `import ultralytics`, and
# a tagger.py that writes what the real one leaves behind (Finder tags,
# processed marker, reports/<stem>/report.json) - or fails, or hangs.
tagger="$work/video-tagger"; mkdir -p "$tagger/.venv/bin"
cat > "$tagger/.venv/bin/python" <<'STUB'
#!/bin/bash
if [ "${1:-}" = "-c" ] && [ "${2:-}" = "import ultralytics" ]; then [ -n "${NO_ULTRALYTICS:-}" ] && exit 1; exit 0; fi
exec python3 "$@"
STUB
chmod +x "$tagger/.venv/bin/python"
cat > "$tagger/tagger.py" <<'PY'
import json, os, plistlib, subprocess, sys, time
path = sys.argv[1]
mode = os.environ.get("FAKE_TAGGER_MODE", "ok")
with open(os.environ["FAKE_TAGGER_CALLS"], "a") as f:
    f.write(path + "\n")
if mode == "hang":
    open(os.environ["FAKE_TAGGER_STARTED"], "w").close()
    time.sleep(600)
if mode == "fail":
    sys.exit(0)   # the real tagger exits 0 on failure too; success is judged by what it leaves behind
tags = ["outdoors", "person"]
blob = plistlib.dumps(tags, fmt=plistlib.FMT_BINARY)
subprocess.run(["xattr", "-wx", "com.apple.metadata:_kMDItemUserTags", blob.hex(), path], check=True)
subprocess.run(["xattr", "-w", "com.videotagger.processed", "1", path], check=True)
stem = os.path.splitext(os.path.basename(path))[0]
rep = os.path.join(os.path.dirname(path), "reports", stem)
os.makedirs(rep, exist_ok=True)
json.dump({"path": path, "tags": tags}, open(os.path.join(rep, "report.json"), "w"))
open(os.path.join(rep, "frame_1.jpg"), "wb").write(b"jpeg")
PY
export FAKE_TAGGER_CALLS="$work/tagger.calls" FAKE_TAGGER_STARTED="$work/tagger.started"
: > "$FAKE_TAGGER_CALLS"

# ── the archive: a real manifest, built by vault itself ───────────────────
export VAULT_CONFIG="$cfg"
src="$work/src"
mkfile() { mkdir -p "$(dirname "$1")"; head -c "${3:-1000}" /dev/zero > "$1"; touch -t "$2" "$1"; }
for f in SonyA6700 SonyZVE10 GoPro DJIFlip DJIMini2 iPhone Backup LeanTank Public; do mkdir -p "$media/$f"; done
# Three copy runs → three distinct copied_at generations in SonyA6700.
mkfile "$src/SonyA6700/DCIM/old.MP4" 202601010000 3000
"$vault" copy media-sonya6700 "$src/SonyA6700" "$media/SonyA6700" > /dev/null
mkfile "$src/SonyA6700/DCIM/mid.MP4" 202602010000 2000
"$vault" copy media-sonya6700 "$src/SonyA6700" "$media/SonyA6700" > /dev/null
mkfile "$src/SonyA6700/DCIM/new.MOV" 202603010000 1000
mkfile "$src/SonyA6700/DCIM/still.JPG" 202603010000 500     # not a video: never selected
"$vault" copy media-sonya6700 "$src/SonyA6700" "$media/SonyA6700" > /dev/null
mkfile "$src/GoPro/Videos/GX01.MP4" 202601150000 4000
"$vault" copy media-gopro "$src/GoPro" "$media/GoPro" > /dev/null
mkfile "$src/Backup/clip.mov" 202512010000 1500
"$vault" copy media-backup "$src/Backup" "$media/Backup" > /dev/null
for d in media-sonya6700 media-gopro media-backup; do
  case $d in media-sonya6700) r=SonyA6700;; media-gopro) r=GoPro;; media-backup) r=Backup;; esac
  "$vault" verify "$d" "$media/$r" > /dev/null || { echo "fixture: verify $d failed"; exit 1; }
done
# One row copied after verify stays `copied`: unproven bytes are never tagged.
mkfile "$src/SonyA6700/DCIM/unverified.MP4" 202604010000 800
"$vault" copy media-sonya6700 "$src/SonyA6700" "$media/SonyA6700" > /dev/null
# A B39-shaped row: verified, dest_path emptied - the file is at <folder>/source_path.
sqlite3 "$cfg/manifest.db" "UPDATE files SET dest_path='' WHERE source_path='DCIM/mid.MP4'"
# Review #26: verified rows whose spelling must never be pulled or written
# through - climbing (would land in GoPro, or outside scratch), our own
# reports/ output, hidden, the NAS trash, AppleDouble - as dest_path and as
# the empty-dest_path fallback. Each names a real, newest file, so a
# selection that admitted any of them would put it at the top of the batch.
mkfile "$media/GoPro/Videos/escaped.MP4" 202609010000 100
mkfile "$media/SonyA6700/DCIM/reports/rep.MP4" 202609010000 100
mkfile "$media/SonyA6700/DCIM/.hidden.MP4" 202609010000 100
mkfile "$media/SonyA6700/#recycle/trash.MP4" 202609010000 100
now_ns=$(python3 -c "import time; print(int(time.time()*1e9)+10**12)")
sqlite3 "$cfg/manifest.db" "INSERT INTO files (source_disk, source_path, dest_path, size, mtime_ns, sha256, copied_at, status) VALUES
  ('media-sonya6700', 'DCIM/esc1.MP4', '../GoPro/Videos/escaped.MP4', 100, 1, 'x', $now_ns, 'verified'),
  ('media-sonya6700', '../GoPro/Videos/escaped.MP4', '', 100, 1, 'x', $now_ns, 'verified'),
  ('media-sonya6700', 'DCIM/esc2.MP4', '../../../../../../tmp/escaped.MP4', 100, 1, 'x', $now_ns, 'verified'),
  ('media-sonya6700', 'DCIM/abs.MP4', '/etc/escaped.MP4', 100, 1, 'x', $now_ns, 'verified'),
  ('media-sonya6700', 'DCIM/reports/rep.MP4', 'DCIM/reports/rep.MP4', 100, 1, 'x', $now_ns, 'verified'),
  ('media-sonya6700', 'DCIM/.hidden.MP4', '', 100, 1, 'x', $now_ns, 'verified'),
  ('media-sonya6700', '#recycle/trash.MP4', '#recycle/trash.MP4', 100, 1, 'x', $now_ns, 'verified'),
  ('media-sonya6700', 'DCIM/._app.MP4', 'DCIM/._app.MP4', 100, 1, 'x', $now_ns, 'verified')"
sqlite3 "$cfg/manifest.db" "PRAGMA wal_checkpoint(TRUNCATE)"   # fold into the main db so the snapshot's rsync copies a consistent file
# Public has files and no rows: walked, newest mtime first.
mkfile "$media/Public/talk.mp4" 202508010000 700
mkfile "$media/Public/older.mov" 202507010000 600
mkfile "$media/Public/reports/x.mp4" 202509010000 100        # our own output dir: never a source
mkfile "$media/Public/.hidden.mp4" 202509010000 100
manifest="$cfg/manifest.db"
check "fixture: the manifest has the expected rows" \
  test "$(sqlite3 "$manifest" "SELECT COUNT(*) FROM files WHERE status='verified'")" -eq 14

# ── how the script is run ─────────────────────────────────────────────────
env_file="$work/mini.env"
cat > "$env_file" <<ENV
SCRATCH_DIR="$scratch"
ENV
# run [cmd...]: the script with the machine set up. Every setting is a
# default the caller's environment may override (the precondition cases do).
run() {
  PATH="$work/bin:$PATH" TAGGING_ENV="${TAGGING_ENV:-$env_file}" TAG_STATE_DIR="${TAG_STATE_DIR:-$state}" \
    MEDIA_ROOT="${MEDIA_ROOT:-$media}" MANIFEST_DB="${MANIFEST_DB:-$manifest}" TAGGER_DIR="${TAGGER_DIR:-$tagger}" \
    LOG_FILE="${LOG_FILE:-$logf}" OLLAMA_URL="${OLLAMA_URL:-http://stub}" \
    "$@"
}
# runbg [args...]: the script in the background with $! being the script
# itself (a subshell that execs), so a signal reaches it and not a wrapper.
runbg() {
  ( export PATH="$work/bin:$PATH" TAGGING_ENV="${TAGGING_ENV:-$env_file}" TAG_STATE_DIR="${TAG_STATE_DIR:-$state}" \
      MEDIA_ROOT="${MEDIA_ROOT:-$media}" MANIFEST_DB="${MANIFEST_DB:-$manifest}" TAGGER_DIR="${TAGGER_DIR:-$tagger}" \
      LOG_FILE="${LOG_FILE:-$logf}" OLLAMA_URL="${OLLAMA_URL:-http://stub}"; exec bash "$script" "$@" ) &
}
out="$work/out"

# ── 1. preconditions: exit 2, nothing attempted ───────────────────────────
pre() { # pre <description> <expected message> <env assignments...>
  local desc=$1 want=$2; shift 2
  ( export "$@"; run bash "$script" --dry-run ) > "$out" 2>&1
  local rc=$?
  if [ $rc -eq 2 ] && grep "REFUSING TO RUN:" "$out" | grep -q -- "$want"; then echo "  ok   precondition: $desc"; else echo "  FAIL precondition: $desc (rc=$rc)"; sed 's/^/       /' "$out" | head -3; failures=$((failures+1)); fi
}
pre "#recycle as a source" "#recycle is listed" TAG_SOURCES_TIER2="Backup #recycle"
pre "a folder in both tiers" "is in both" TAG_SOURCES_TIER2="Backup GoPro"
pre "TAG_SOURCES emptied" "TAG_SOURCES is empty" TAG_SOURCES=" "
pre "scratch not a mounted volume" "is not a mounted volume" SCRATCH_DIR="$work/notavolume"
pre "media root not mounted" "is not mounted" MEDIA_ROOT="$work/elsewhere"
pre "a tier folder missing" "does not exist" TAG_SOURCES="SonyA6700 Nope"
pre "manifest missing" "manifest not found" MANIFEST_DB="$work/none.db"
pre "Ollama down" "Ollama is not answering" OLLAMA_DOWN=1
pre "tagger venv missing" "video-tagger venv missing" TAGGER_DIR="$work/no-tagger"
pre "ultralytics does not import" "ultralytics does not import" NO_ULTRALYTICS=1
check "preconditions: the tagger was never called" test ! -s "$FAKE_TAGGER_CALLS"
check "preconditions: no state db was created" test ! -e "$state/tagging-state.db"

# ── 2. dry run: the batch, newest first, tier 1; nothing touched ──────────
run bash "$script" --dry-run > "$out" 2>&1
check "dry run exits 0" test $? -eq 0
check "dry run: manifest snapshot line names rows and newest copied_at" grep -q "snapshot:  .* rows, newest copied_at 20" "$out"
check "dry run: tier 1 / tier 2 are the decided defaults" bash -c "grep -q 'tier 1:    SonyA6700 SonyZVE10 GoPro DJIFlip DJIMini2 iPhone' '$out' && grep -q 'tier 2:    Backup LeanTank Public' '$out'"
check "dry run: cap is 200 GB" grep -q "cap: 200 GB" "$out"
order=$(grep -E '^\s+(SonyA6700|GoPro)\s' "$out" | awk '{print $2}' | tr '\n' ' ')
check "dry run: tier 1 newest copied_at first, across folders (GX01, new, mid, old)" \
  test "$order" = "Videos/GX01.MP4 DCIM/new.MOV DCIM/mid.MP4 DCIM/old.MP4 "
check "dry run: the copied (unverified) row is not selected" test "$(count 'unverified' "$out")" -eq 0
check "dry run: the still image is not selected" test "$(count 'still.JPG' "$out")" -eq 0
check "dry run: the empty-dest_path row is selected by its source_path" grep -q "DCIM/mid.MP4" "$out"
check "dry run: no climbing, absolute, reports/, hidden, #recycle or AppleDouble row is selected (review #26)" \
  test "$(grep -v '^selected ' "$out" | grep -Ec 'escaped|/etc/|reports/|\.hidden|#recycle|\._app')" -eq 0
check "dry run: the skipped candidates are announced with a count and an example" \
  grep -q "skipped 8 candidate(s) with unsafe or junk paths (first: " "$out"
check "dry run: tier 2 is not touched while tier 1 has pending files" test "$(count 'Public' "$(echo "$out")")" -le 1 -a "$(grep -c '^\s\+Backup\s' "$out")" -eq 0
check "dry run: summary says selected 4 files from tier 1" grep -q "selected 4 files, .* from tier 1; pending before this run: tier 1 4, tier 2 3" "$out"
check "dry run: no POLICY OVERRIDE line with defaults" test "$(count 'POLICY OVERRIDE' "$out")" -eq 0
check "dry run: no state db, no lock, no scratch run dir, no log" \
  bash -c "test ! -e '$state/tagging-state.db' && test ! -e '$state/tagging.lock' && test -z \"\$(ls '$scratch' 2>/dev/null)\" && test ! -e '$logf'"
check "dry run: no NAS file got the processed marker" \
  test "$(find "$media" -type f -name '*.M*' -exec xattr -p com.videotagger.processed {} \; 2>/dev/null | wc -l | tr -d ' ')" -eq 0
check "dry run: the tagger was never called" test ! -s "$FAKE_TAGGER_CALLS"

# Policy override from mini.env: applied, and logged against the default.
cat > "$work/mini-override.env" <<ENV
SCRATCH_DIR="$scratch"
TAG_BATCH_MAX_GB="50"
TAG_SOURCES_TIER2="Backup"
ENV
TAGGING_ENV="$work/mini-override.env" run bash "$script" --dry-run > "$out" 2>&1
check "override: cap from mini.env applied" grep -q "cap: 50 GB" "$out"
check "override: logged against the default with its source" \
  grep -q "POLICY OVERRIDE: TAG_BATCH_MAX_GB=\[50\] (default \[200\], from $work/mini-override.env)" "$out"
check "override: tier 2 override logged too" grep -q "POLICY OVERRIDE: TAG_SOURCES_TIER2=\[Backup\] (default \[Backup LeanTank Public\]" "$out"
( export TAGGING_ENV="$work/mini-override.env" TAG_BATCH_MAX_GB=7; run bash "$script" --dry-run ) > "$out" 2>&1
check "override: environment still wins over mini.env" grep -q "cap: 7 GB" "$out"

# ── 2b. a failed dry-run selection leaves nothing behind (review #26) ─────
# A state db that opens but lacks the files table makes selection fail;
# the dry run must exit non-zero, say so, and leave no temp file in TMPDIR.
mkdir -p "$state"; sqlite3 "$state/tagging-state.db" "CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)"
tmpbefore=$(ls -A "${TMPDIR:-/tmp}" | sort)
run bash "$script" --dry-run > "$out" 2>&1
rc=$?
tmpafter=$(ls -A "${TMPDIR:-/tmp}" | sort)
check "dry run with a broken state db: exit 2 and says selection failed" test "$rc" -eq 2 -a "$(count 'batch selection failed' "$out")" -eq 1
check "dry run with a broken state db: no temp file left in TMPDIR" test "$tmpbefore" = "$tmpafter"
rm -f "$state/tagging-state.db"

# ── 3. torn snapshot: refused ─────────────────────────────────────────────
head -c 4000 /dev/urandom > "$work/torn.db"
MANIFEST_DB="$work/torn.db" run bash "$script" --dry-run > "$out" 2>&1
check "torn manifest copy: dry run refuses (exit 2)" test $? -eq 2
check "torn manifest copy: says the snapshot failed" grep -q "manifest snapshot failed" "$out"

# ── 4. a real run, two files ──────────────────────────────────────────────
run bash "$script" --limit 2 > "$out" 2>&1
check "run 1: exits 0" test $? -eq 0
check "run 1: log exists and stdout was not a terminal (nothing echoed)" bash -c "test -s '$logf' && test ! -s '$out'"
check "run 1: tagged the two newest tier-1 files, in order" \
  test "$(cat "$FAKE_TAGGER_CALLS" | sed "s#.*/tagging/[^/]*/##" | tr '\n' ' ')" = "GoPro/Videos/GX01.MP4 SonyA6700/DCIM/new.MOV "
check "run 1: the tagger ran on the SCRATCH copy, not the NAS file" test "$(grep -c "^$scratch/" "$FAKE_TAGGER_CALLS")" -eq 2
check "run 1: NAS file carries the processed marker" test "$(xattr -p com.videotagger.processed "$media/SonyA6700/DCIM/new.MOV")" = 1
check "run 1: NAS file carries the Finder tags" xattr -p com.apple.metadata:_kMDItemUserTags "$media/SonyA6700/DCIM/new.MOV" > /dev/null
check "run 1: report landed beside the NAS file and points at it" \
  bash -c "python3 -c \"import json,sys; r=json.load(open('$media/SonyA6700/DCIM/reports/new/report.json')); sys.exit(0 if r['path']=='$media/SonyA6700/DCIM/new.MOV' else 1)\""
check "run 1: the untouched file has no marker" bash -c "! xattr -p com.videotagger.processed '$media/SonyA6700/DCIM/old.MP4' > /dev/null 2>&1"
check "run 1: two done rows in the state db" test "$(sqlite3 "$state/tagging-state.db" "SELECT COUNT(*) FROM files WHERE status='done'")" -eq 2
check "run 1: scratch cleaned" test -z "$(ls "$scratch/tagging" 2>/dev/null)"
check "run 1: lock released" test ! -e "$state/tagging.lock"
check "run 1: SUMMARY line" grep -q "SUMMARY run=.* selected 2, pulled 2, tagged 2, wrote back 2, failed 0, still pending → tier 1 2, tier 2 3" "$logf"
check "run 1: log lines single (starting once)" test "$(count ' starting: tier1=' "$logf")" -eq 1

# Existing Finder tags on a NAS file are merged, not replaced. The next file
# is the empty-dest_path row, located and recorded by its source_path.
python3 - "$media/SonyA6700/DCIM/mid.MP4" <<'PY'
import plistlib, subprocess, sys
blob = plistlib.dumps(["keeper"], fmt=plistlib.FMT_BINARY)
subprocess.run(["xattr", "-wx", "com.apple.metadata:_kMDItemUserTags", blob.hex(), sys.argv[1]], check=True)
PY
: > "$FAKE_TAGGER_CALLS"
run bash "$script" --limit 1 > "$out" 2>&1
check "run 2: exits 0" test $? -eq 0
check "run 2: the next newest file, not the done ones (the empty-dest_path row, by source_path)" test "$(sed "s#.*/tagging/[^/]*/##" "$FAKE_TAGGER_CALLS")" = "SonyA6700/DCIM/mid.MP4"
check "run 2: recorded by its file path" test "$(sqlite3 "$state/tagging-state.db" "SELECT status FROM files WHERE camera='SonyA6700' AND dest_path='DCIM/mid.MP4'")" = done
python3 - "$media/SonyA6700/DCIM/mid.MP4" > "$work/tags.out" <<'PY'
import plistlib, subprocess, sys
h = subprocess.run(["xattr", "-p", "-x", "com.apple.metadata:_kMDItemUserTags", sys.argv[1]], capture_output=True, text=True).stdout
print(sorted(str(t).split("\n")[0] for t in plistlib.loads(bytes.fromhex(h.replace(" ", "").replace("\n", "")))))
PY
check "run 2: existing Finder tag kept alongside the new ones" grep -qF "['keeper', 'outdoors', 'person']" "$work/tags.out"

# ── 4b. a symlinked directory on the NAS side: refused before the pull ───
# The helper cannot see filesystem links in a row's path; the shell walks
# the components and refuses, recording the file failed, writing nothing
# through the link.
elsewhere="$work/elsewhere"; mkfile "$elsewhere/linked.MP4" 202609010000 300
ln -s "$elsewhere" "$media/SonyA6700/LINK"
sqlite3 "$manifest" "INSERT INTO files (source_disk, source_path, dest_path, size, mtime_ns, sha256, copied_at, status) VALUES
  ('media-sonya6700', 'LINK/linked.MP4', 'LINK/linked.MP4', 300, 1, 'x', $now_ns, 'verified')"
sqlite3 "$manifest" "PRAGMA wal_checkpoint(TRUNCATE)"
: > "$FAKE_TAGGER_CALLS"
run bash "$script" --limit 1 > "$out" 2>&1
check "symlinked dir: exits 1" test $? -eq 1
check "symlinked dir: refused, naming the link, recorded failed" bash -c "grep -q 'FILE SonyA6700/LINK/linked.MP4 FAILED: refused, .*/LINK is a symlink' '$logf' && test \"\$(sqlite3 '$state/tagging-state.db' \"SELECT status FROM files WHERE dest_path='LINK/linked.MP4'\")\" = failed"
check "symlinked dir: the tagger never ran, nothing written through the link" \
  test ! -s "$FAKE_TAGGER_CALLS" -a -z "$(ls "$elsewhere/reports" 2>/dev/null)" -a "$(xattr "$elsewhere/linked.MP4" | wc -l | tr -d ' ')" -eq 0
sqlite3 "$manifest" "DELETE FROM files WHERE source_path='LINK/linked.MP4'"; sqlite3 "$manifest" "PRAGMA wal_checkpoint(TRUNCATE)"; rm "$media/SonyA6700/LINK"; rm -rf "$scratch/tagging"

# ── 5. a failing file: recorded, scratch kept, exit 1, re-selected ────────
: > "$FAKE_TAGGER_CALLS"
FAKE_TAGGER_MODE=fail run bash "$script" --limit 1 > "$out" 2>&1
check "failed file: exits 1" test $? -eq 1
check "failed file: log says FAILED with the reason" grep -q "FILE SonyA6700/DCIM/old.MP4 FAILED: tagger did not mark" "$logf"
check "failed file: recorded failed" test "$(sqlite3 "$state/tagging-state.db" "SELECT status FROM files WHERE dest_path='DCIM/old.MP4'")" = failed
check "failed file: scratch kept for inspection" test -n "$(ls "$scratch/tagging" 2>/dev/null)"
check "failed file: lock released" test ! -e "$state/tagging.lock"
check "failed file: NAS file has no marker" bash -c "! xattr -p com.videotagger.processed '$media/SonyA6700/DCIM/old.MP4' > /dev/null 2>&1"
rm -rf "$scratch/tagging"
run bash "$script" --dry-run > "$out" 2>&1
check "failed file: re-selected next time" grep -q "DCIM/old.MP4" "$out"
check "failed file: dry run notes nothing else pending in tier 1" grep -q "pending before this run: tier 1 1, tier 2 3" "$out"

# ── 6. interrupted mid-file: in-flight file failed, lock released, resume ─
: > "$FAKE_TAGGER_CALLS"; rm -f "$FAKE_TAGGER_STARTED"
FAKE_TAGGER_MODE=hang runbg --limit 1
pid=$!
for _ in $(seq 1 100); do [ -e "$FAKE_TAGGER_STARTED" ] && break; sleep 0.1; done
check "interrupt: the tagger was mid-file" test -e "$FAKE_TAGGER_STARTED"
kill -TERM "$pid"; wait "$pid"; rc=$?
check "interrupt: exit 143" test $rc -eq 143
check "interrupt: logged, file counted failed" bash -c "grep -q 'interrupted (SIGTERM/SIGINT)' '$logf' && grep -q 'failed 1, still pending' '$logf'"
check "interrupt: lock released" test ! -e "$state/tagging.lock"
check "interrupt: no stray tagger process" bash -c "! pgrep -f 'tagger.py .*old.MP4' > /dev/null"
rm -rf "$scratch/tagging"

# ── 7. lock: live holder refuses, stale lock refuses loudly ───────────────
mkdir -p "$state/tagging.lock"; echo "$$ started now run=test" > "$state/tagging.lock/info"
run bash "$script" --limit 1 > "$out" 2>&1
check "lock held by a live process: exit 1, not started" bash -c "test $? -eq 1 && grep -q 'another run is in progress' '$logf'"
echo "999999 started long ago run=old" > "$state/tagging.lock/info"; touch -t 202601010000 "$state/tagging.lock"
run bash "$script" --limit 1 > "$out" 2>&1
check "stale lock (dead pid, >24h): exit 1, says STALE LOCK, not cleared" bash -c "test $? -eq 1 && grep -q 'STALE LOCK' '$logf' && test -d '$state/tagging.lock'"
rm -rf "$state/tagging.lock"

# ── 8. tier 2 after tier 1 drains: manifest rows and the walked folder ────
run bash "$script" > "$out" 2>&1     # finishes tier 1 (old.MP4)
run bash "$script" --dry-run > "$out" 2>&1
check "tier 2: selected once tier 1 is drained; folders without rows are walked" grep -q "from tier 2 (walked: LeanTank Public)" "$out"
t2=$(grep -E '^\s+(Backup|Public)\s' "$out" | awk '{print $1"/"$2}' | tr '\n' ' ')
# Within tier 2 a manifest row sorts by copied_at (today) and a walked file
# by its mtime (2025): newest first across both kinds.
check "tier 2: newest first across manifest and walked rows (Backup/clip, Public/talk, Public/older)" \
  test "$t2" = "Backup/clip.mov Public/talk.mp4 Public/older.mov "
check "tier 2: reports/ and dotfiles are not walked" test "$(count 'reports/x.mp4' "$out")" -eq 0 -a "$(count '.hidden' "$out")" -eq 0
check "tier 2: walked rows say so" grep -q "walked, id -" "$out"
run bash "$script" > "$out" 2>&1
check "tier 2 run: exits 0 and tags the walked file on the NAS" bash -c "test $? -eq 0 && test \"\$(xattr -p com.videotagger.processed '$media/Public/talk.mp4')\" = 1"
check "tier 2 run: walked rows recorded with source=walk" test "$(sqlite3 "$state/tagging-state.db" "SELECT COUNT(*) FROM files WHERE source='walk' AND status='done'")" -eq 2
run bash "$script" > "$out" 2>&1
check "everything done: 'nothing to do', exit 0" bash -c "test $? -eq 0 && grep -q 'nothing to do' '$logf'"

# ── 9. lock edge cases (review #26), now that the batch is drained ────────
mkfresh() { mkfile "$src/SonyA6700/DCIM/$1" 202612010000 111; "$vault" copy media-sonya6700 "$src/SonyA6700" "$media/SonyA6700" > /dev/null; "$vault" verify media-sonya6700 "$media/SonyA6700" > /dev/null; sqlite3 "$manifest" "PRAGMA wal_checkpoint(TRUNCATE)"; }

# A lock dir with no info yet is one being acquired, not a dead one.
mkdir -p "$state/tagging.lock"
run bash "$script" --limit 1 > "$out" 2>&1
check "young lock without info: exit 1, treated as live, not taken over" bash -c "test $? -eq 1 && grep -q 'another run is acquiring the lock' '$logf' && test -d '$state/tagging.lock' && test ! -e '$state/tagging.lock/info'"

# A dead lock (dead pid) is taken over by renaming it away; a fresh file
# proves the run then proceeds; the dead dir is gone and the lock released.
echo "999999 started earlier run=dead" > "$state/tagging.lock/info"; touch -t "$(date -v-2H +%Y%m%d%H%M)" "$state/tagging.lock"
mkfresh takeover.MP4; : > "$FAKE_TAGGER_CALLS"
run bash "$script" --limit 1 > "$out" 2>&1
check "dead lock: taken over, run proceeds, old dir gone, lock released" \
  bash -c "grep -q 'is no longer alive .* — taken over' '$logf' && test -s '$FAKE_TAGGER_CALLS' && test ! -e '$state/tagging.lock' && test -z \"\$(ls -d '$state'/tagging.lock.dead.* 2>/dev/null)\""

# A failure right after acquiring the lock (corrupt state db) still releases it.
mkfresh corrupt.MP4
cp "$state/tagging-state.db" "$work/state.bak"; head -c 2000 /dev/urandom > "$state/tagging-state.db"
run bash "$script" --limit 1 > "$out" 2>&1
check "corrupt state db right after the lock: exit non-zero, lock released, no summary claimed" \
  bash -c "test $? -ne 0 && test ! -e '$state/tagging.lock'"
cp "$work/state.bak" "$state/tagging-state.db"

echo
if [ "$failures" -ne 0 ]; then echo "$failures check(s) failed"; exit 1; fi
echo "all checks passed"
