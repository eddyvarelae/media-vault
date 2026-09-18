#!/bin/bash
# Copy all 4 active cameras from tars (USB) into /volume1/media/<camera>/
# with the canonical Videos/Photos/etc. layout. Sequential — copying in
# parallel just thrashes the SATA pool.
set -u

# Pinned release tag; override with VAULT_IMAGE=... for a one-off run.
# v0.2.0 does not exist yet: the PM tags it on main after the B3/B4/B5 PR and
# F4 (verify --only-unverified) merge. See docs/release.md.
IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.0}"
LOG="${VAULT_LOG:-/volume1/docker/tars-copy.log}"

# One line to $LOG, and to the terminal only when there is one (B35, the
# same fix as B27): launched as `sudo nohup ./nas-tars-copy-all.sh >> $LOG
# 2>&1 &`, a `tee -a $LOG` wrote every line twice.
log() {
  if [ -t 1 ]; then
    echo "$@" | tee -a "$LOG"
  else
    echo "$@" >> "$LOG"
  fi
}

log "[$(date)] starting tars → media copy"

run_copy() {
  local disk="$1"
  local folder="$2"
  shift 2
  log ""
  log "[$(date)] === $folder → $disk ==="
  if ! docker run --rm \
    -v /volume1:/volume1 \
    -v /mnt/@usb:/usb:ro \
    -e VAULT_CONFIG=/volume1/docker/vault-nas-config \
    "$IMG" copy "$disk" "/usb/sdc1/$folder" "/volume1/media/$folder" \
    "$@" --on-collision rename-mtime-year >> "$LOG" 2>&1; then
    log "[$(date)] $folder FAILED"
    return 1
  fi
  log "[$(date)] $folder done"
}

run_copy media-djiflip   DJIFlip   --prefix DCIM --rule MP4=Videos --rule SRT=FlightLogs --rule JPG=Photos
run_copy media-gopro     GoPro     --prefix DCIM --rule MP4=Videos --rule LRV=Videos --rule THM=Videos --rule JPG=Photos --rule sav=Other
run_copy media-sonya6700 SonyA6700
run_copy media-sonyzve10 SonyZVE10

log ""
log "[$(date)] all tars copies done"
