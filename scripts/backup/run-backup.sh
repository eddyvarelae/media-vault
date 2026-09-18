#!/bin/bash
# Attached-SSD gap report, report-only (B22; DECISIONS 2026-09-15). launchd
# runs this every five minutes. When a known source SSD is mounted under
# /Volumes and has not been reported since it was attached (or today), run
# `vault gap` on it against a fresh snapshot of the NAS manifest and write
# the report: which files on the SSD are NOT in the archive, by content.
# It copies nothing, stages nothing, and never writes to the SSD or the NAS.
#
#   run-backup.sh              the tick (launchd, every 5 min); silent when nothing is due
#   run-backup.sh --dry-run    say what would be reported now; no snapshot, no state, no report
#   run-backup.sh --force      report every known mounted SSD even if already reported
#
# Exit codes: 0 nothing due, or every due report written · 1 a report failed
# (or the lock is held) · 2 a precondition failed with a disk due.
set -euo pipefail

# Machine settings from mini-server's config/mini.env (MINI_ENV); job policy
# defaults here (the decided known-disk list). mini.env may override policy;
# every override is logged. The environment overrides both.
# Volume names can contain spaces ("Eddy's Media Vault"), so the lists are
# |-separated and matched whole, never by word.
POLICY_DEFAULT_BACKUP_DISKS="tars|kipp|case|Eddy's Media Vault"
POLICY_DEFAULT_BACKUP_EXCLUDE="Scratch1"
policy_keys="BACKUP_DISKS BACKUP_EXCLUDE"
machine_keys="SCRATCH_DIR MANIFEST_DB BACKUP_STATE_DIR BACKUP_LOG_FILE VOLUMES_DIR VAULT_BIN REPO_DIR"
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
export PATH="/opt/homebrew/bin:$PATH"

SCRATCH_DIR="${SCRATCH_DIR:-}"
MANIFEST_DB="${MANIFEST_DB:-$HOME/mounts/docker/vault-nas-config/manifest.db}"
STATE_DIR="${BACKUP_STATE_DIR:-$HOME/Library/Application Support/mini-server}"
LOG_FILE="${BACKUP_LOG_FILE:-$HOME/Library/Logs/mini-server/media-backup.log}"
VOLUMES_DIR="${VOLUMES_DIR:-/Volumes}"
here="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="${REPO_DIR:-$(cd "$here/../.." && pwd)}"
# A VAULT_BIN given from outside is used as it is; only the job's own copy
# under the state dir is (re)built from this checkout when missing or stale.
managed_bin=0
if [[ -z "${VAULT_BIN:-}" ]]; then VAULT_BIN="$STATE_DIR/bin/vault"; managed_bin=1; fi
HELPER="$here/../tagging/tagging-helper.py"
STATE_FILE="$STATE_DIR/backup-state.tsv"     # name <TAB> attach-identity <TAB> date, one line per reported disk
LOCK_DIR="$STATE_DIR/backup.lock"
SNAP_DIR="$STATE_DIR/gap-manifest"            # the snapshot lives here as manifest.db, so VAULT_CONFIG can point at it

dry_run=0 force=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) dry_run=1 ;;
    --force) force=1 ;;
    -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

ts() { date +%Y-%m-%dT%H:%M:%S%z; }
log() { if (( dry_run )); then echo "$*"; else echo "[$(ts)] $*" >> "$LOG_FILE"; fi; }
die() { log "REFUSING TO RUN: $*"; echo "[$(ts)] REFUSING TO RUN: $*" >&2; exit 2; }
today() { date +%Y-%m-%d; }

# A volume's attach identity: device + inode + birth time of its mount
# point. A re-attach mounts a new device, so the identity changes; a report
# is owed once per identity and once per day while it stays attached.
attach_id() { stat -f '%d:%i:%B' "$1"; }
# slug maps a volume name to an injective, filesystem-safe report slug by
# hex-encoding every byte. A single-case alphabet [0-9a-f] is used on
# purpose: a percent/underscore scheme keeps letter case, so "Disk" and
# "disk" would collide on a case-insensitive $STATE_DIR and one report
# would delete the other (review #41). od handles any bytes, incl.
# non-ASCII and spaces.
slug() { printf '%s' "$1" | od -An -v -tx1 | tr -d ' \n'; }
boot_dev=$(df -P / | awk 'NR==2 {print $1}')
scratch_dev=""; [[ -n "$SCRATCH_DIR" && -d "$SCRATCH_DIR" ]] && scratch_dev=$(df -P "$SCRATCH_DIR" | awk 'NR==2 {print $1}')
in_list() {  # in_list <name> <|-separated list>: whole-name match
  local IFS='|' d
  for d in $2; do [[ "$d" == "$1" ]] && return 0; done
  return 1
}
known() { in_list "$1" "$BACKUP_DISKS"; }
excluded() { in_list "$1" "$BACKUP_EXCLUDE"; }
reported() {  # reported <name> <attach-id>: already reported for this attach today?
  [[ -f "$STATE_FILE" ]] && grep -qF "$1	$2	$(today)" "$STATE_FILE"
}

# ── 1. what is mounted, and what is due ──────────────────────────────────
due=() unknown=()
for mp in "$VOLUMES_DIR"/*; do
  [[ -d "$mp" && ! -L "$mp" ]] || continue
  name=$(basename "$mp")
  dev=$(df -P "$mp" | awk 'NR==2 {print $1}')
  [[ "$dev" != "$boot_dev" ]] || continue
  [[ -z "$scratch_dev" || "$dev" != "$scratch_dev" ]] || continue
  excluded "$name" && continue
  if ! known "$name"; then unknown+=("$name"); continue; fi
  id=$(attach_id "$mp")
  if (( force )) || ! reported "$name" "$id"; then due+=("$name"); fi
done

if (( dry_run )); then
  echo "DRY RUN — attached-SSD gap report"
  echo "  known disks: $BACKUP_DISKS   excluded: $BACKUP_EXCLUDE"
  for o in "${policy_overrides[@]+"${policy_overrides[@]}"}"; do echo "  POLICY OVERRIDE: $o"; done
  echo "  manifest:    $MANIFEST_DB"
  echo "  unknown mounted volumes (ignored): ${unknown[*]-(none)}"
  echo "  due now: ${due[*]-(none)}"
  exit 0
fi
# Unknown mounted volumes get logged once per attach, independent of whether
# any known disk is due (review #28). The marker is keyed by name+attach-id,
# so a detach/reattach (new id) logs again, and stale markers are pruned.
if (( ! dry_run )); then
  mkdir -p "$STATE_DIR" "$(dirname "$LOG_FILE")"
  # Keyed by name only. A mount generation is not observable from /Volumes
  # (device/inode/birth can all survive a remount), so a re-attach is logged
  # again ONLY when a tick in between observed the volume gone and pruned its
  # marker (review #38; stated in README). A detach/remount entirely between
  # two ticks is not detected - the honest limit of polling /Volumes.
  live_unknown=""
  for u in "${unknown[@]+"${unknown[@]}"}"; do
    slug=$(slug "$u")
    live_unknown="$live_unknown $slug"
    marker="$STATE_DIR/backup.unknown-$slug"
    [[ -f "$marker" ]] || { touch "$marker"; log "mounted volume '$u' is not in BACKUP_DISKS — ignored (add it to report it)"; }
  done
  for marker in "$STATE_DIR"/backup.unknown-*; do
    [[ -e "$marker" ]] || continue
    case " $live_unknown " in *" ${marker##*/backup.unknown-} "*) ;; *) rm -f "$marker" ;; esac
  done
fi
(( ${#due[@]} > 0 )) || exit 0     # the normal tick: nothing more owed; say nothing

# ── 2. preconditions (only checked when something is due) ────────────────
# A failure that will recur every five minutes - a missing OR unusable
# manifest - is logged at most once an hour; a good tick clears the stamp
# (review #28). throttled_fail logs+exits 2 the first time and stays quiet
# until the hour is up.
mkdir -p "$STATE_DIR" "$(dirname "$LOG_FILE")"
THROTTLE="$STATE_DIR/backup.precondition-failed"
throttled_fail() {
  if [[ ! -f "$THROTTLE" ]] || (( $(date +%s) - $(stat -f %m "$THROTTLE") >= 3600 )); then touch "$THROTTLE"; die "$*"; fi
  exit 2
}
[[ -f "$MANIFEST_DB" ]] || throttled_fail "manifest not found at $MANIFEST_DB (is the NAS docker share mounted?) — ${due[*]} due"

# ── 3. the vault binary: built from this checkout into the state dir when missing or stale ──
ensure_vault() {
  if (( ! managed_bin )); then [[ -x "$VAULT_BIN" ]] || { log "VAULT_BIN=$VAULT_BIN is not executable"; return 1; }; return 0; fi
  local head; head=$(git -C "$REPO_DIR" rev-parse HEAD 2>/dev/null || echo unknown)
  local stamp="$VAULT_BIN.commit"
  if [[ -x "$VAULT_BIN" && -f "$stamp" && "$(cat "$stamp")" == "$head" ]]; then return 0; fi
  if [[ -x "$VAULT_BIN" && "$head" == unknown ]]; then return 0; fi   # not a checkout: use what is there
  command -v go >/dev/null || { log "vault binary missing or stale at $VAULT_BIN and go is not installed"; return 1; }
  mkdir -p "$(dirname "$VAULT_BIN")" "$STATE_DIR/go/cache" "$STATE_DIR/go/mod" "$STATE_DIR/go/tmp" "$STATE_DIR/go/home"
  # The managed build writes only under $STATE_DIR (review #28-5): its
  # object cache, module cache and temp workspace are all confined there.
  # Everything the build writes stays under $STATE_DIR (review #38-4): the
  # caches and GOPATH by their env vars, and - because Go's telemetry and
  # config land under $HOME/Library/Application Support regardless of
  # GOTELEMETRY/GOTELEMETRYDIR - HOME itself is redirected under state for
  # the build. GOENV=off ignores the user's go env file.
  if ! (cd "$REPO_DIR" && HOME="$STATE_DIR/go/home" GOPATH="$STATE_DIR/go/path" GOCACHE="$STATE_DIR/go/cache" GOMODCACHE="$STATE_DIR/go/mod" GOTMPDIR="$STATE_DIR/go/tmp" GOENV=off GOTELEMETRY=off go build -o "$VAULT_BIN.tmp" ./cmd/vault) >> "$LOG_FILE" 2>&1; then
    log "go build failed (see above); keeping the previous binary if any"; rm -f "$VAULT_BIN.tmp"; [[ -x "$VAULT_BIN" ]]; return
  fi
  mv "$VAULT_BIN.tmp" "$VAULT_BIN"; echo "$head" > "$stamp"
  log "built vault at $VAULT_BIN from $REPO_DIR @ $head"
}

# ── 4. lock, snapshot, one report per due disk ───────────────────────────
# Same discipline as the tagger (review #26-3, #28-2): cleanup trap the
# instant the lock is ours; a lock dir with no info yet is being acquired,
# not dead; a dead lock is taken over by renaming it away, which only one
# competitor can win.
rc=0 have_lock="" finishing=""
finish() { finishing=1; [[ -n "$have_lock" ]] && rm -rf "$LOCK_DIR"; exit "$rc"; }
# Never taken over automatically (review #38, as tagger #36): mkdir is the
# only acquisition; any pre-existing lock - live, dead or info-less - makes
# this tick refuse and leave it for a human. A directory lock's takeover
# cannot be made race-free, and a stuck report that refuses quietly (the
# next tick tries again once a human clears it) is safer than two reports
# racing the snapshot.
acquire_lock() {
  if mkdir "$LOCK_DIR" 2>/dev/null; then return 0; fi
  local info pid
  info=$(cat "$LOCK_DIR/info" 2>/dev/null || echo "no info yet")
  pid=${info%% *}
  if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then return 1; fi   # a report is running
  # Dead or info-less: report it once (throttled) and leave it.
  if [[ ! -f "$STATE_DIR/backup.stale-lock" ]] || (( $(date +%s) - $(stat -f %m "$STATE_DIR/backup.stale-lock") >= 3600 )); then
    touch "$STATE_DIR/backup.stale-lock"
    log "STALE LOCK: $LOCK_DIR ($info), its process is gone or unknown — not cleared automatically; inspect, then rmdir it by hand"
  fi
  return 1
}
acquire_lock || exit 1
have_lock=1
rm -f "$STATE_DIR/backup.stale-lock"
trap 'log "interrupted — the report in flight is not recorded"; rc=143; finish' TERM INT
trap '[[ -n "$finishing" ]] || { log "UNEXPECTED EXIT (rc=$?) — releasing the lock"; rc=1; finish; }' EXIT
{ echo "$$ started $(ts)" > "$LOCK_DIR/info.tmp" && mv "$LOCK_DIR/info.tmp" "$LOCK_DIR/info"; }

for o in "${policy_overrides[@]+"${policy_overrides[@]}"}"; do log "POLICY OVERRIDE: $o"; done
ensure_vault || { rc=1; finish; }

mkdir -p "$SNAP_DIR"
if ! snap=$(python3 "$HELPER" snapshot --state "$STATE_DIR/tagging-state.db" --manifest "$MANIFEST_DB" --snapshot "$SNAP_DIR/manifest.db" 2>&1); then
  # A snapshot that keeps failing (a corrupt manifest) recurs every tick;
  # throttle it like the preconditions (review #28-4).
  rc=2
  if [[ ! -f "$THROTTLE" ]] || (( $(date +%s) - $(stat -f %m "$THROTTLE") >= 3600 )); then touch "$THROTTLE"; log "manifest snapshot failed: $snap"; fi
  finish
fi
rm -f "$THROTTLE"    # a usable snapshot clears the throttle
read -r _ rows newest mtime <<< "$(head -1 <<< "$snap")"
log "manifest snapshot: $rows rows, newest copied_at $newest, NAS file modified $mtime (from $MANIFEST_DB)"

for name in "${due[@]}"; do
  mp="$VOLUMES_DIR/$name"; id=$(attach_id "$mp"); d=$(today)
  slug=$(slug "$name")   # hex: distinct disks never share a report file, case included (review #38/#41)
  report="$STATE_DIR/gap-$slug-$d.txt"; tsv="$STATE_DIR/gap-$slug-$d.tsv"
  rm -f "$report" "$tsv"   # the job owns its dated output; a --force re-report replaces it (vault gap's --tsv is O_EXCL against aliases, not against our own file)
  log "GAP $name: report starting ($mp, attach $id)"
  if VAULT_CONFIG="$SNAP_DIR" "$VAULT_BIN" gap "$mp" --tsv "$tsv" > "$report" 2>&1; then
    log "$(grep '^GAP ' "$report" | sed "s|^GAP $mp|GAP $name|")"
    log "GAP $name: report at $report, absent files at $tsv"
    printf '%s\t%s\t%s\n' "$name" "$id" "$d" >> "$STATE_FILE"
  else
    log "GAP $name FAILED: $(tail -1 "$report")"; rc=1
  fi
done
finish
