#!/bin/bash
# Archive the `kipp` USB disk (B26, team/context/runbook-kipp.md) into
# /volume1/media/<Folder>/, one folder at a time - copying in parallel just
# thrashes the SATA pool. Same shape as nas-tars-copy-all.sh.
#
#   KIPP_SRC=/usb/sdd1 sudo -E nohup ./nas-kipp-copy-all.sh >> /volume1/docker/kipp-copy.log 2>&1 &
#   KIPP_SRC=/usb/sdd1 DRY_RUN=1 sudo -E ./nas-kipp-copy-all.sh
#     (plans only: no archive file, no manifest row - but the log is
#      appended and every container still opens the live manifest, B31)
#
# docker needs root on the NAS, hence sudo -E for both; the outer >> is
# opened by the invoking shell, so that user must be able to write the log.
#
# KIPP_SRC is the disk's path INSIDE the container (/mnt/@usb is mounted at
# /usb read-only), found in runbook step 1. There is no default on purpose:
# the wrong disk under a right-looking folder name is how content gets
# copied under the wrong provenance.
set -u

# Pinned release tag; override with VAULT_IMAGE=... for a one-off run.
IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.1}"
LOG="${VAULT_LOG:-/volume1/docker/kipp-copy.log}"
SRC="${KIPP_SRC:?set KIPP_SRC to the container path of the kipp disk, e.g. /usb/sdd1 (runbook step 1)}"
DRY_RUN="${DRY_RUN:-}"

# One line to $LOG, and to the terminal only when there is one (B27).
log() {
  if [ -t 1 ]; then
    echo "$@" | tee -a "$LOG"
  else
    echo "$@" >> "$LOG"
  fi
}

# A plain word, not an array: under `set -u` bash 3.2 (the Mac that runs the
# tests) treats an empty array expansion as unbound.
extra=""
if [ -n "$DRY_RUN" ]; then
  extra="--dry-run"
fi

log "[$(date)] starting kipp → media copy from $SRC${DRY_RUN:+ (DRY RUN)}"

failures=0
run_copy() {
  local disk="$1"
  local folder="$2"
  shift 2
  log ""
  log "[$(date)] === $folder → $disk ==="
  # --dedupe-content: kipp overlaps the archive by content (all of LeanTank,
  # one Backup file); those get deduped rows, not second copies.
  # --on-collision rename-mtime-year: a name clash with different bytes lands
  # beside the original. A verified file is never overwritten regardless
  # (v0.2.1); the 13 known ones are skipped here and handled in step 6.
  if ! docker run --rm \
    -v /volume1:/volume1 \
    -v /mnt/@usb:/usb:ro \
    -e VAULT_CONFIG=/volume1/docker/vault-nas-config \
    "$IMG" copy "$disk" "$SRC/$folder" "/volume1/media/$folder" \
    "$@" --dedupe-content --on-collision rename-mtime-year $extra >> "$LOG" 2>&1; then
    log "[$(date)] $folder FAILED"
    failures=$((failures + 1))
    return 1
  fi
  log "[$(date)] $folder done"
}

# Per-folder routing flags reproduce the layout each camera already has
# under /volume1/media (runbook step 2, Tester #23): Sony, Backup and
# LeanTank were copied flat; GoPro with the tars run's routing; Multicam and
# Auditorium are new disks copied verbatim.
run_copy media-sonya6700  SonyA6700
run_copy media-backup     Backup
run_copy media-multicam   Multicam
run_copy media-auditorium Auditorium
run_copy media-gopro      GoPro      --prefix DCIM --rule MP4=Videos --rule LRV=Videos --rule THM=Videos --rule JPG=Photos --rule sav=Other
run_copy media-sonyzve10  SonyZVE10
# Every LeanTank file is already archived by content: this pass records
# kipp's provenance as deduped rows and copies nothing.
run_copy media-leantank   LeanTank

log ""
log "[$(date)] all kipp copies done — $failures folder(s) FAILED"
[ "$failures" -eq 0 ]
