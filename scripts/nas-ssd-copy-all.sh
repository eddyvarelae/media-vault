#!/bin/bash
# Archive one source SSD into /volume1/media/<Folder>/, one folder at a time —
# copying in parallel just thrashes the SATA pool. The <label> picks the source
# root and the per-folder table (B26/B36, team/context/runbook-kipp.md):
#
#   tars — the 4 active cameras from tars (USB), source /usb/sdc1 by default.
#   kipp — the kipp USB disk, 7 folders, deduped against the archive by content;
#          SSD_SRC is required (the disk's container path, runbook step 1).
#
#   SSD_SRC=/usb/sdd1 sudo -E nohup ./nas-ssd-copy-all.sh kipp >> /volume1/docker/kipp-copy.log 2>&1 &
#   sudo -E nohup ./nas-ssd-copy-all.sh tars >> /volume1/docker/tars-copy.log 2>&1 &
#   SSD_SRC=/usb/sdd1 DRY_RUN=1 sudo -E ./nas-ssd-copy-all.sh kipp
#     (plans only: no archive file, no manifest row — but the log is appended
#      and every container still opens the live manifest, B31)
#
# docker needs root on the NAS, hence sudo -E; the outer >> is opened by the
# invoking shell, so that user must be able to write the log.
#
# SSD_SRC is the disk's path INSIDE the container (/mnt/@usb is mounted at /usb
# read-only). For kipp there is no default on purpose: the wrong disk under a
# right-looking folder name is how content gets copied under the wrong
# provenance. tars is the fixed /usb/sdc1 unless SSD_SRC overrides it.
set -u

label="${1-}"
# Pinned release tag; override with VAULT_IMAGE=... for a one-off run.
IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.5}"
case "$label" in
  tars) SRC="${SSD_SRC:-/usb/sdc1}"; dedupe="" ;;
  kipp) SRC="${SSD_SRC:?set SSD_SRC to the container path of the kipp disk, e.g. /usb/sdd1 (runbook step 1)}"; dedupe="--dedupe-content" ;;
  *)    echo "usage: $(basename "$0") <label>   (label: tars | kipp)" >&2; exit 2 ;;
esac
LOG="${VAULT_LOG:-/volume1/docker/$label-copy.log}"
DRY_RUN="${DRY_RUN:-}"

# One line to $LOG, and to the terminal only when there is one (B27/B35): under
# the nohup launch form stdout IS the log, so a `tee -a $LOG` would double it.
log() {
  if [ -t 1 ]; then
    echo "$@" | tee -a "$LOG"
  else
    echo "$@" >> "$LOG"
  fi
}

# Plain words, not arrays: under `set -u` bash 3.2 (the Mac that runs the tests)
# treats an empty array expansion as unbound. Empty when they do not apply.
extra=""
if [ -n "$DRY_RUN" ]; then
  extra="--dry-run"
fi

log "[$(date)] starting $label → media copy from $SRC${DRY_RUN:+ (DRY RUN)}"

failures=0
run_copy() {
  local disk="$1"
  local folder="$2"
  shift 2
  log ""
  log "[$(date)] === $folder → $disk ==="
  # dedupe (kipp only): content already in the archive under another row is
  # recorded as a deduped row, not copied again. --on-collision
  # rename-mtime-year: a name clash with different bytes lands beside the
  # original. A verified file is never overwritten regardless.
  if ! docker run --rm \
    -v /volume1:/volume1 \
    -v /mnt/@usb:/usb:ro \
    -e VAULT_CONFIG=/volume1/docker/vault-nas-config \
    "$IMG" copy "$disk" "$SRC/$folder" "/volume1/media/$folder" \
    "$@" $dedupe --on-collision rename-mtime-year $extra >> "$LOG" 2>&1; then
    log "[$(date)] $folder FAILED"
    failures=$((failures + 1))
    return 1
  fi
  log "[$(date)] $folder done"
}

# Per-folder routing flags reproduce the layout each camera already has under
# /volume1/media (runbook step 2, Tester #23).
table_tars() {
  run_copy media-djiflip   DJIFlip   --prefix DCIM --rule MP4=Videos --rule SRT=FlightLogs --rule JPG=Photos
  run_copy media-gopro     GoPro     --prefix DCIM --rule MP4=Videos --rule LRV=Videos --rule THM=Videos --rule JPG=Photos --rule sav=Other
  run_copy media-sonya6700 SonyA6700
  run_copy media-sonyzve10 SonyZVE10
}
table_kipp() {
  run_copy media-sonya6700  SonyA6700
  run_copy media-backup     Backup
  run_copy media-multicam   Multicam
  run_copy media-auditorium Auditorium
  run_copy media-gopro      GoPro      --prefix DCIM --rule MP4=Videos --rule LRV=Videos --rule THM=Videos --rule JPG=Photos --rule sav=Other
  run_copy media-sonyzve10  SonyZVE10
  # Every LeanTank file is already archived by content: this pass records
  # kipp's provenance as deduped rows and copies nothing.
  run_copy media-leantank   LeanTank
}

"table_$label"

log ""
log "[$(date)] all $label copies done — $failures folder(s) FAILED"
[ "$failures" -eq 0 ]
