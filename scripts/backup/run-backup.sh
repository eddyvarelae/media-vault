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
SLUGS_FILE="$STATE_DIR/slugs.tsv"            # name <TAB> slug, append-only: the assigned, persisted output identifier
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
# slug seeds a BASE identifier for a volume name: the hex of the first 24 bytes
# (a short name stays legible in the filename) then 16 hex of the whole name's
# sha256. A single-case alphabet [0-9a-f-] on purpose: a percent/underscore
# scheme keeps letter case, so "Disk" and "disk" would collide on a
# case-insensitive $STATE_DIR (review #41). Bounded, so a long name never
# overran the filename limit and lost its report (review #43); ~65 chars. This
# is only a FIRST CHOICE - assign_slug turns it into a guaranteed-unique,
# persisted slug (review #52), so the base need not be injective. BACKUP_SLUG_HOOK
# is a test seam: set to a command, it computes the base instead, so a test can
# force two names onto one base and exercise the -N disambiguation for real.
slug() {
  if [[ -n "${BACKUP_SLUG_HOOK:-}" ]]; then "$BACKUP_SLUG_HOOK" "$1"; return; fi
  printf '%s-%s' \
    "$(printf '%s' "$1" | head -c 24 | od -An -v -tx1 | tr -d ' \n')" \
    "$(printf '%s' "$1" | shasum -a 256 | cut -c1-16)"
}

# The slug is ASSIGNED and PERSISTED, not derived, so two names can never share
# a slug however their bases hash (review #52). $SLUGS_FILE maps name<TAB>slug,
# append-only. recorded_slug returns a name's stored slug (empty if none).
recorded_slug() { [[ -f "$SLUGS_FILE" ]] && awk -F'\t' -v n="$1" '$1==n{print $2; exit}' "$SLUGS_FILE"; }
# assign_slug returns a name's slug, assigning and recording one on first sight:
# the base, else base-2, base-3, … until no OTHER name holds it; recorded via a
# temp file + rename so the append is atomic. Idempotent: a recorded name is
# returned without touching the file.
assign_slug() {
  local name="$1" existing base cand n tmp
  existing=$(recorded_slug "$name"); [[ -n "$existing" ]] && { printf '%s' "$existing"; return; }
  base=$(slug "$name"); cand="$base"; n=1
  while [[ -n "$(awk -F'\t' -v s="$cand" -v me="$name" '$2==s && $1!=me{print $1; exit}' "$SLUGS_FILE" 2>/dev/null)" ]]; do
    n=$((n + 1)); cand="$base-$n"
  done
  tmp="$SLUGS_FILE.tmp.$$"
  { [[ -f "$SLUGS_FILE" ]] && cat "$SLUGS_FILE"; printf '%s\t%s\n' "$name" "$cand"; } > "$tmp" && mv "$tmp" "$SLUGS_FILE"
  printf '%s' "$cand"
}
# check_slugs aborts (touching nothing) if the registry is corrupt - a name
# mapped to two different slugs, or a slug to two different names. assign_slug
# never creates either; only a hand-edit can (review #52).
check_slugs() {
  [[ -f "$SLUGS_FILE" ]] || return 0
  local bad
  bad=$(awk -F'\t' '
    NF>=2 {
      if (($1 in nm) && nm[$1]!=$2) nbad[$1]=1
      if (($2 in sl) && sl[$2]!=$1) sbad[$2]=1
      nm[$1]=$2; sl[$2]=$1
    }
    END { for (k in nbad) print "name \"" k "\" maps to multiple slugs"
          for (k in sbad) print "slug \"" k "\" maps to multiple names" }
  ' "$SLUGS_FILE")
  [[ -z "$bad" ]] || { log "REFUSING: $SLUGS_FILE is corrupt — $bad — nothing written, pruned or recorded this tick; clear it by hand"; exit 1; }
}
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

# A mount-point component cannot exceed 255 bytes, so a configured disk name
# longer than that can never match a real volume - and the full name is what
# each report carries on line 1 for the slug-collision guard below. Refuse
# such a name at discovery, before building any report path (review #43).
# Refuse it BEFORE any log call: the log directory is not created until a disk
# is due, so die()'s log() would write into a directory that does not exist
# yet (review #49). A configuration error goes straight to stderr, which
# launchd routes into the log.
_ifs=$IFS; IFS='|'
for _d in $BACKUP_DISKS; do
  if (( $(printf '%s' "$_d" | wc -c) > 255 )); then
    IFS=$_ifs
    printf '[%s] REFUSING TO RUN: BACKUP_DISKS entry is longer than 255 bytes, which no mount point can be: %s\n' "$(ts)" "'${_d:0:48}…'" >&2
    exit 2
  fi
done
IFS=$_ifs

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
# ── 1a. assign slugs, then a defense-in-depth foreign-owner check (review #52) ─
# Slugs are assigned and persisted (assign_slug), so distinct names never share
# an output path - the #51 planned-collision cases cannot arise. Two guards
# remain, run before ANYTHING is written, pruned or appended: a corrupt
# registry aborts (check_slugs), and any existing output whose line-1 header
# names a volume other than the one the registry assigns is a corruption/tamper
# signal - log each, touch nothing, exit 1. Only a clean tick prunes and writes.
mkdir -p "$STATE_DIR" "$(dirname "$LOG_FILE")"   # so the diagnostic can be logged; writes nothing else
check_slugs                                       # a hand-corrupted registry aborts, touching nothing
d=$(today)
foreign=0
for name in "${due[@]+"${due[@]}"}"; do
  slug=$(assign_slug "$name")
  for f in "$STATE_DIR/gap-$slug-$d.txt" "$STATE_DIR/gap-$slug-$d.tsv"; do
    [[ -e "$f" ]] || continue
    if [[ "$(head -1 "$f" 2>/dev/null)" != "disk: $name" ]]; then
      log "FOREIGN OUTPUT: $f names \"$(head -1 "$f" 2>/dev/null)\", not \"disk: $name\" (slug $slug) — the registry or the file is corrupt; leaving it untouched"
      foreign=1
    fi
  done
done
for u in "${unknown[@]+"${unknown[@]}"}"; do
  slug=$(assign_slug "$u")
  marker="$STATE_DIR/backup.unknown-$slug"
  [[ -e "$marker" ]] || continue
  if [[ "$(head -1 "$marker" 2>/dev/null)" != "volume: $u" ]]; then
    log "FOREIGN OUTPUT: unknown-volume marker $marker names \"$(head -1 "$marker" 2>/dev/null)\", not \"volume: $u\" (slug $slug) — the registry or the file is corrupt; leaving it untouched"
    foreign=1
  fi
done
(( foreign )) && { log "REFUSING: foreign output(s) above — nothing written, pruned or recorded this tick"; exit 1; }

# ── 1b. a clean tick: log new unknown volumes, prune stale markers ────────────
# Unknown mounted volumes get logged once, independent of whether any known
# disk is due (review #28). The marker is keyed by the volume's assigned slug; a
# mount generation is not observable from /Volumes (device/inode/birth can
# survive a remount), so a re-attach is logged again only when a tick in between
# observed the volume gone and pruned its marker (review #38; stated in README);
# a detach/remount entirely between two ticks is not detected. Pruning is by
# OWNER: a marker is stale when the volume named on its line 1 is no longer
# mounted-and-unknown (review #50).
for u in "${unknown[@]+"${unknown[@]}"}"; do
  slug=$(assign_slug "$u")
  marker="$STATE_DIR/backup.unknown-$slug"
  [[ -f "$marker" ]] || {
    printf 'volume: %s\n' "$u" > "$marker"   # line 1 names the volume, for the foreign-owner check
    log "mounted volume '$u' is not in BACKUP_DISKS — ignored (add it to report it)"
  }
done
for marker in "$STATE_DIR"/backup.unknown-*; do
  [[ -e "$marker" ]] || continue
  owner=$(head -1 "$marker" 2>/dev/null); owner=${owner#volume: }
  live=0
  for u in "${unknown[@]+"${unknown[@]}"}"; do [[ "$u" == "$owner" ]] && { live=1; break; }; done
  (( live )) || rm -f "$marker"
done
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
  slug=$(assign_slug "$name")   # the volume's assigned, persisted slug (review #52)
  report="$STATE_DIR/gap-$slug-$d.txt"; tsv="$STATE_DIR/gap-$slug-$d.tsv"; tsvtmp="$tsv.building"
  # The assigned slug is this disk's alone, and Phase 1a proved no foreign owner
  # sits at it, so the only file that can be here is our own from an earlier tick
  # today; a --force re-report replaces it. Line 1 records the full disk name so
  # the next tick's foreign-owner check can prove ownership (review #43/#50/#52).
  header="disk: $name"
  rm -f "$report" "$tsv" "$tsvtmp"   # our own dated output; a --force re-report replaces it
  log "GAP $name: report starting ($mp, attach $id)"
  printf '%s\n' "$header" > "$report"   # line 1: the full disk name (slug-collision guard)
  # vault gap writes the TSV to a temp path (its --tsv is O_EXCL|O_NOFOLLOW
  # against an alias); the final TSV is then the same full-name header line
  # followed by that content, so both outputs are checkable line 1.
  if VAULT_CONFIG="$SNAP_DIR" "$VAULT_BIN" gap "$mp" --tsv "$tsvtmp" >> "$report" 2>&1; then
    { printf '%s\n' "$header"; cat "$tsvtmp"; } > "$tsv"; rm -f "$tsvtmp"
    log "$(grep '^GAP ' "$report" | sed "s|^GAP $mp|GAP $name|")"
    log "GAP $name: report at $report, absent files at $tsv"
    printf '%s\t%s\t%s\n' "$name" "$id" "$d" >> "$STATE_FILE"
  else
    log "GAP $name FAILED: $(tail -1 "$report")"; rc=1; rm -f "$tsvtmp"
  fi
done
finish
