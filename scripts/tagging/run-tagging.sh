#!/bin/bash
# Nightly video tagging: pull new NAS footage to scratch, tag it locally with
# video-tagger, write the Finder tags + JSON report back to the NAS file.
#
#   run-tagging.sh              the real thing (launchd runs this at 02:00)
#   run-tagging.sh --dry-run    print tonight's batch, touch nothing
#   run-tagging.sh --limit N    stop after N files (testing)
#   run-tagging.sh --tier2-only debug: select as if tier 1 were drained
#
# What gets tagged, in what order (DECISIONS.md 2026-09-17): every video the
# archive holds, newest first, tier 1 ($TAG_SOURCES, the camera folders)
# before tier 2 ($TAG_SOURCES_TIER2 — Backup, LeanTank, Public). Tier 2 is
# only touched once tier 1 has nothing pending. "Pending" = not in this job's
# per-file done set; there is no watermark, so fresh footage always sorts to
# the top and the historical backlog drains behind it.
#
# Where the list comes from: media-vault's manifest on the NAS docker share
# (the single writer lives NAS-side since 2026-09-02), which this job never
# opens in place — it is WAL sqlite over SMB. Each run rsyncs it to a local
# snapshot first. A tier-2 folder the manifest has no rows for (Public) is
# walked with a directory listing instead. See tagging-helper.py.
#
# The storage rule this enforces (team/context/project.md): NAS is the archive,
# never the working surface. Each file is rsynced to $SCRATCH_DIR, tagged
# there, and only the results travel back. tagger.py is never pointed at
# ~/mounts/media.
#
# Exit codes: 0 all good · 1 some file failed (or lock held) · 2 precondition
# not met (nothing was attempted). Every outcome is one line in the log; a
# night that did less than it claims is the failure mode we refuse to add.
set -euo pipefail

# Two kinds of setting, from two owners (B17). The MACHINE is mini-server's:
# where scratch, the mounts, Ollama, the tagger venv and the log live come
# from its config/mini.env (path overridable with MINI_ENV). The JOB is
# ours: which folders, in what order, how much per night, which extensions -
# Eddy's decisions of 2026-09-17 (team/DECISIONS.md), defaulted right here.
# mini.env may still override a policy key (it is Eddy's file), but every
# such override is logged against the default so a night that tagged the
# wrong folders says why. The environment overrides both (tests use it).
POLICY_DEFAULT_TAG_SOURCES="SonyA6700 SonyZVE10 GoPro DJIFlip DJIMini2 iPhone"
POLICY_DEFAULT_TAG_SOURCES_TIER2="Backup LeanTank Public"
POLICY_DEFAULT_TAG_BATCH_MAX_GB="200"
POLICY_DEFAULT_TAG_VIDEO_EXTS=".mp4 .mov"    # .lrv proxies, .arw .jpg .xml .srt .thm stay out
policy_keys="TAG_SOURCES TAG_SOURCES_TIER2 TAG_BATCH_MAX_GB TAG_VIDEO_EXTS"
machine_keys="SCRATCH_DIR TAG_STATE_DIR MEDIA_ROOT MANIFEST_DB OLLAMA_URL TAGGER_DIR LOG_FILE"
overridable="$policy_keys $machine_keys"
for v in $overridable; do eval "_pre_$v=\${$v-}"; done
MINI_ENV="${MINI_ENV:-$HOME/Projects/mini-server/config/mini.env}"
if [[ -f "$MINI_ENV" ]]; then
  source "$MINI_ENV"
fi
policy_overrides=()
for v in $policy_keys; do
  eval "_env_$v=\${$v-}"
  eval "_def_$v=\$POLICY_DEFAULT_$v"
  eval "_e=\$_env_$v; _d=\$_def_$v"
  if [[ -n "$_e" && "$_e" != "$_d" ]]; then
    policy_overrides+=("$v=[$_e] (default [$_d], from $MINI_ENV)")
  fi
  eval "[[ -n \$_env_$v ]] || $v=\$_def_$v"
done
for v in $overridable; do eval "[[ -n \${_pre_$v} ]] && $v=\${_pre_$v}" || true; done

# launchd starts with a bare PATH; ffmpeg/exiftool/whisper-cli are Homebrew's.
export PATH="/opt/homebrew/bin:$PATH"

SCRATCH_DIR="${SCRATCH_DIR:?SCRATCH_DIR must be set in $MINI_ENV (mini-server config/mini.env)}"
TAG_STATE_DIR="${TAG_STATE_DIR:-$HOME/Library/Application Support/mini-server}"
MEDIA_ROOT="${MEDIA_ROOT:-$HOME/mounts/media}"
MANIFEST_DB="${MANIFEST_DB:-$HOME/mounts/docker/vault-nas-config/manifest.db}"
OLLAMA_URL="${OLLAMA_URL:-http://127.0.0.1:11434}"
TAGGER_DIR="${TAGGER_DIR:-$HOME/Projects/video-tagger}"
LOG_FILE="${LOG_FILE:-$HOME/Library/Logs/mini-server/video-tagger.log}"

TAGGER_PY="$TAGGER_DIR/.venv/bin/python"
HELPER="$(cd "$(dirname "$0")" && pwd)/tagging-helper.py"
STATE_DB="$TAG_STATE_DIR/tagging-state.db"
SNAPSHOT="$TAG_STATE_DIR/manifest.snapshot.db"
LOCK_DIR="$TAG_STATE_DIR/tagging.lock"
RUN_ID=$(date +%Y%m%d-%H%M%S)
RUN_DIR="$SCRATCH_DIR/tagging/$RUN_ID"

dry_run=0 limit=0 tier2_only=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) dry_run=1 ;;
    --limit) limit="${2:?--limit needs a number}"; shift ;;
    --tier2-only) tier2_only="--tier2-only" ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done
limit_note=""; (( limit > 0 )) && limit_note=" limit=$limit"
[[ -n "$tier2_only" ]] && limit_note="$limit_note tier2-only"

ts() { date +%Y-%m-%dT%H:%M:%S%z; }
log() { echo "[$(ts)] $*"; }
die() { echo "[$(ts)] REFUSING TO RUN: $*" >&2; exit 2; }
helper() { "$TAGGER_PY" "$HELPER" "$@"; }

# ── 1. preconditions — nothing is attempted unless all of these hold ──────
mounted_elsewhere() {   # $1 exists and is not on the boot disk (its device differs from / and the Data volume)
  [[ -d "$1" ]] || return 1
  local dev; dev=$(df -P "$1" | awk 'NR==2 {print $1}')
  [[ "$dev" != "$(df -P / | awk 'NR==2 {print $1}')" && "$dev" != "$(df -P /System/Volumes/Data | awk 'NR==2 {print $1}')" ]]
}
[[ -n "${TAG_SOURCES// /}" ]] || die "TAG_SOURCES is empty — the default is [$POLICY_DEFAULT_TAG_SOURCES]; something set it to nothing"
for f in $TAG_SOURCES $TAG_SOURCES_TIER2; do   # the NAS trash is never a source
  [[ "$f" != "#recycle" ]] || die "#recycle is listed as a source — remove it from TAG_SOURCES/TAG_SOURCES_TIER2"
done
for f in $TAG_SOURCES_TIER2; do
  case " $TAG_SOURCES " in *" $f "*) die "$f is in both TAG_SOURCES and TAG_SOURCES_TIER2" ;; esac
done
mounted_elsewhere "$SCRATCH_DIR" || die "SCRATCH_DIR=$SCRATCH_DIR is not a mounted volume"
[[ -w "$SCRATCH_DIR" ]] || die "SCRATCH_DIR=$SCRATCH_DIR is not writable"
mount | grep -q " on $MEDIA_ROOT " || die "$MEDIA_ROOT is not mounted (run scripts/mount-nas.sh)"
for cam in $TAG_SOURCES $TAG_SOURCES_TIER2; do
  [[ -d "$MEDIA_ROOT/$cam" ]] || die "$MEDIA_ROOT/$cam does not exist — check TAG_SOURCES / TAG_SOURCES_TIER2"
done
[[ -f "$MANIFEST_DB" ]] || die "manifest not found at $MANIFEST_DB (is the NAS docker share mounted? NAS_SHARES in mini-server config/mini.env, its scripts/mount-nas.sh)"
models=$(curl -sf --max-time 5 "$OLLAMA_URL/api/tags") || die "Ollama is not answering at $OLLAMA_URL"
grep -q '"name":"llava' <<< "$models" || die "Ollama is up but the llava model is not pulled (ollama pull llava)"
[[ -x "$TAGGER_PY" ]] || die "video-tagger venv missing at $TAGGER_PY (mini-server SETUP.md Phase 5)"
# The lesson in lessons.md: YOLO failing silently produced a fully "tagged"
# library with every object tag missing. Prove the detector imports first.
"$TAGGER_PY" -c "import ultralytics" 2>/dev/null || die "ultralytics does not import in $TAGGER_PY — object detection would be silently skipped"

# Every read of the manifest goes through a fresh local snapshot (db + wal
# rsynced, checkpointed, quick_checked). Prints "ok <rows> <newest> <mtime>"
# and maybe a WARN line if the NAS file has not changed since the last run.
take_snapshot() { helper snapshot --state "$STATE_DB" --manifest "$MANIFEST_DB" --snapshot "$SNAPSHOT"; }
# Built at call time, not up front: a dry run points SNAPSHOT at a temp dir
# after this point, and a list captured earlier would name a snapshot the
# dry run never wrote (found by the harness on the first dry run).
sel_args() { printf '%s\n' --snapshot "$SNAPSHOT" --state "$STATE_DB" --tier1 "$TAG_SOURCES" --tier2 "$TAG_SOURCES_TIER2" --exts "$TAG_VIDEO_EXTS" --media-root "$MEDIA_ROOT"; }
with_sel() {  # with_sel <helper subcommand> [more args]: the subcommand with the selection args
  local sub=$1; shift
  local args=(); while IFS= read -r a; do args+=("$a"); done < <(sel_args)
  helper "$sub" "${args[@]}" "$@"
}
select_batch() {   # TSV on stdout: id, folder, rel_path, size, ts_ns, source
  with_sel select --max-bytes "$(( TAG_BATCH_MAX_GB * 1000000000 ))" --limit "$limit" $tier2_only
}
iso() { "$TAGGER_PY" -c "import datetime,sys;print(datetime.datetime.fromtimestamp(int(sys.argv[1])/1e9).astimezone().isoformat(timespec='seconds'))" "$1"; }
leftover_dirs() { ls -d "$SCRATCH_DIR"/tagging/*/ 2>/dev/null || true; }

# ── --dry-run: show the batch, touch nothing (no lock, no log, no state) ──
if (( dry_run )); then
  SNAPSHOT="$(mktemp -d)/manifest.snapshot.db"; trap 'rm -rf "$(dirname "$SNAPSHOT")"' EXIT   # a dry run leaves no state behind
  echo "DRY RUN — the batch a real run would process now"
  echo "  manifest:  $MANIFEST_DB"
  snap=$(take_snapshot) || die "manifest snapshot failed"
  read -r _ rows newest mtime <<< "$(head -1 <<< "$snap")"
  echo "  snapshot:  $rows rows, newest copied_at $newest, NAS file modified $mtime"
  grep '^WARN' <<< "$snap" | sed 's/^/  /' || true
  echo "  tier 1:    $TAG_SOURCES"
  echo "  tier 2:    ${TAG_SOURCES_TIER2:-(none)}"
  echo "  exts: $TAG_VIDEO_EXTS   cap: ${TAG_BATCH_MAX_GB} GB$limit_note"
  for o in "${policy_overrides[@]+"${policy_overrides[@]}"}"; do echo "  POLICY OVERRIDE: $o"; done
  [[ -d "$LOCK_DIR" ]] && echo "  NOTE: lock held at $LOCK_DIR ($(cat "$LOCK_DIR/info" 2>/dev/null))"
  leftover_dirs | while read -r d; do echo "  NOTE: leftover run dir on scratch (kept after a failed run): $(du -sh "$d" | tr '\t' ' ')"; done
  echo
  errf=$(mktemp)
  batch=$(select_batch 2>"$errf"); summary=$(cat "$errf"); rm -f "$errf"
  while IFS=$'\t' read -r id cam dest size ts source; do
    [[ -n "$id" ]] || continue
    if [[ "$source" == walk ]]; then via="mtime"; ref="walked, id $id"; else via="copied"; ref="manifest id $id"; fi
    printf '  %-10s %-52s %8.2f GB  %s %s  (%s)\n' \
      "$cam" "$dest" "$(awk -v b="$size" 'BEGIN{print b/1e9}')" "$via" "$(iso "$ts")" "$ref"
  done <<< "$batch"
  echo
  echo "$summary"
  exit 0
fi

# ── logging: launchd already redirects to the log; only tee when interactive ──
mkdir -p "$(dirname "$LOG_FILE")"
if [[ -t 1 ]]; then exec > >(tee -a "$LOG_FILE") 2>&1; else exec >>"$LOG_FILE" 2>&1; fi

# ── 2. single-instance lock ───────────────────────────────────────────────
mkdir -p "$TAG_STATE_DIR"
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
  info=$(cat "$LOCK_DIR/info" 2>/dev/null || echo "no info")
  lock_pid=${info%% *}
  lock_age_h=$(( ( $(date +%s) - $(stat -f %m "$LOCK_DIR") ) / 3600 ))
  if [[ "$lock_pid" =~ ^[0-9]+$ ]] && kill -0 "$lock_pid" 2>/dev/null; then
    log "another run is in progress (lock $LOCK_DIR: $info) — not starting"; exit 1
  fi
  if (( lock_age_h >= 24 )); then
    log "STALE LOCK: $LOCK_DIR is ${lock_age_h}h old ($info) and its process is gone. Not clearing it automatically — inspect, then rmdir it."; exit 1
  fi
  log "lock from a run that is no longer alive ($info, ${lock_age_h}h old) — taking it over"
fi
echo "$$ started $(ts) run=$RUN_ID" > "$LOCK_DIR/info"
helper run-started --state "$STATE_DB" --run-id "$RUN_ID"

# ── signals: stop the child, keep scratch, release the lock, summarise ────
child="" finishing=""
selected=0 pulled=0 tagged=0 wroteback=0 failed=0
finish() {
  local rc=$1
  finishing=1
  if [[ -n "$child" ]]; then
    pkill -TERM -P "$child" 2>/dev/null || true   # by parent pid, never by pattern (lessons.md)
    kill "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true
  fi
  pend=$(with_sel pending 2>/dev/null || echo "? (pending count failed)")
  log "SUMMARY run=$RUN_ID selected $selected, pulled $pulled, tagged $tagged, wrote back $wroteback, failed $failed, still pending → $pend"
  if (( rc == 0 && failed == 0 )); then
    rm -rf "$RUN_DIR"; log "scratch cleaned: $RUN_DIR"
  elif [[ -d "$RUN_DIR" ]]; then
    log "scratch KEPT for inspection: $RUN_DIR ($(du -sh "$RUN_DIR" 2>/dev/null | cut -f1))"
  fi
  rm -rf "$LOCK_DIR"
  log "run $RUN_ID finished rc=$rc"
  exit "$rc"
}
trap 'log "interrupted (SIGTERM/SIGINT) — stopping; the file in flight counts as failed"; failed=$((failed+1)); finish 143' TERM INT
# set -e on an unexpected error must still leave a summary and release the lock.
trap '[[ -n "$finishing" ]] || { log "UNEXPECTED EXIT (rc=$?) — a command failed outside the per-file handling"; failed=$((failed+1)); finish 1; }' EXIT
# Children get no stdin: ffmpeg (inside tagger.py) reads it otherwise and eats
# the batch lines the loop below is still reading.
run_child() { "$@" </dev/null & child=$!; wait "$child"; local rc=$?; child=""; return $rc; }

log "run $RUN_ID starting: tier1=[$TAG_SOURCES] tier2=[$TAG_SOURCES_TIER2] exts=[$TAG_VIDEO_EXTS] cap=${TAG_BATCH_MAX_GB}GB$limit_note scratch=$RUN_DIR"
for o in "${policy_overrides[@]+"${policy_overrides[@]}"}"; do log "POLICY OVERRIDE: $o"; done
leftover_dirs | while read -r d; do log "NOTE: leftover run dir on scratch from an earlier failed run: $(du -sh "$d" | tr '\t' ' ')"; done

# ── 3. snapshot the manifest, select the batch ────────────────────────────
mkdir -p "$RUN_DIR"
if ! snap=$(take_snapshot 2>"$RUN_DIR/.snap"); then log "manifest snapshot failed: $(cat "$RUN_DIR/.snap")"; finish 2; fi
rm -f "$RUN_DIR/.snap"
read -r _ rows newest mtime <<< "$(head -1 <<< "$snap")"
log "manifest snapshot: $rows rows, newest copied_at $newest, NAS file modified $mtime (from $MANIFEST_DB)"
grep '^WARN' <<< "$snap" | while read -r w; do log "$w"; done
if ! batch=$(select_batch 2>"$RUN_DIR/.select"); then log "batch selection failed: $(cat "$RUN_DIR/.select")"; finish 2; fi
log "$(cat "$RUN_DIR/.select")"; rm -f "$RUN_DIR/.select"
selected=$(grep -c . <<< "$batch" || true)
if (( selected == 0 )); then log "nothing to do"; finish 0; fi

# ── 4-8. per file: pull → tag → write back → verify → clean ───────────────
# Sequential on purpose: one file on scratch at a time keeps the resume story
# simple (state is recorded per file, so a killed run loses at most the file
# it was on) and scratch never holds more than one clip beyond the cap.
cd "$TAGGER_DIR"   # yolo_detect.py loads yolov8n.pt relative to the CWD
record() {  # $1 status, $2 tags-json, $3 error
  helper record --state "$STATE_DB" --id "$id" --source "$source" --camera "$cam" --dest-path "$dest" \
    --size "$size" --copied-at "$ts" --status "$1" --tags "$2" --error "$3" --run-id "$RUN_ID"
}
while IFS=$'\t' read -r -u 3 id cam dest size ts source; do
  [[ -n "$id" ]] || continue
  rel="$cam/$dest"
  src="$MEDIA_ROOT/$rel"
  dst="$RUN_DIR/$rel"
  stem="$(basename "${dest%.*}")"
  report_scratch="$(dirname "$dst")/reports/$stem"   # where tagger.py writes it
  report_nas="$(dirname "$src")/reports/$stem"       # same convention, on the NAS
  t0=$(date +%s)

  if [[ ! -f "$src" ]]; then
    log "FILE $rel FAILED: not on the NAS ($source id $id)"; record failed "" "missing on NAS"; failed=$((failed+1)); continue
  fi
  mkdir -p "$(dirname "$dst")"
  if ! run_child rsync -a --partial "$src" "$dst"; then
    log "FILE $rel FAILED: pull (rsync) — see above"; record failed "" "rsync failed"; failed=$((failed+1)); continue
  fi
  pulled=$((pulled+1)); t1=$(date +%s)

  # tagger.py exits 0 even when it logs "Failed to process", so success is
  # judged by what it leaves behind: its processed marker + report.json.
  run_child "$TAGGER_PY" "$TAGGER_DIR/tagger.py" "$dst" || true
  t2=$(date +%s)
  if tags=$(helper writeback --src "$dst" --dst "$src" --report-src "$report_scratch" --report-dst "$report_nas" 2>"$RUN_DIR/.err.$id" </dev/null); then
    tagged=$((tagged+1)); wroteback=$((wroteback+1))
    record done "$tags" ""
    rm -f "$dst" "$RUN_DIR/.err.$id"; rm -rf "$report_scratch"
    log "FILE $rel ok tags=$tags (pull $((t1-t0))s, tag $((t2-t1))s, writeback $(( $(date +%s) - t2 ))s)"
  else
    err=$(tail -1 "$RUN_DIR/.err.$id" 2>/dev/null || echo "?")
    xattr -p com.videotagger.processed "$dst" >/dev/null 2>&1 && tagged=$((tagged+1))
    log "FILE $rel FAILED: $err (scratch copy kept at $dst)"
    record failed "" "$err"; failed=$((failed+1))
  fi
done 3<<< "$batch"

# ── 9. summary, pending count, cleanup, exit code (all in finish) ─────────
find "$RUN_DIR" -type d -empty -delete 2>/dev/null || true   # subtrees left by successes
if (( failed > 0 )); then finish 1; else finish 0; fi
