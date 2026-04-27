#!/bin/bash
# Copy all 4 active cameras from tars (USB) into /volume1/media/<camera>/
# with the canonical Videos/Photos/etc. layout. Sequential — copying in
# parallel just thrashes the SATA pool.
set -u

IMG=ghcr.io/eddyvarelae/media-vault:latest
LOG=/volume1/docker/tars-copy.log

echo "[$(date)] starting tars → media copy" | tee -a "$LOG"

run_copy() {
  local disk="$1"
  local folder="$2"
  shift 2
  echo | tee -a "$LOG"
  echo "[$(date)] === $folder → $disk ===" | tee -a "$LOG"
  /usr/bin/time -f "wall %e s" docker run --rm \
    -v /volume1:/volume1 \
    -v /mnt/@usb:/usb:ro \
    -e VAULT_CONFIG=/volume1/docker/vault-nas-config \
    "$IMG" copy "$disk" "/usb/sdc1/$folder" "/volume1/media/$folder" \
    "$@" --on-collision rename-mtime-year >> "$LOG" 2>&1
  echo "[$(date)] $folder done" | tee -a "$LOG"
}

run_copy media-djiflip   DJIFlip   --prefix DCIM --rule MP4=Videos --rule SRT=FlightLogs --rule JPG=Photos
run_copy media-gopro     GoPro     --prefix DCIM --rule MP4=Videos --rule LRV=Videos --rule THM=Videos --rule JPG=Photos --rule sav=Other
run_copy media-sonya6700 SonyA6700
run_copy media-sonyzve10 SonyZVE10

echo | tee -a "$LOG"
echo "[$(date)] all tars copies done" | tee -a "$LOG"
