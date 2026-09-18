#!/bin/bash
# Re-hash every file under /volume1/media/<camera>/ and confirm the bytes
# still match what the manifest recorded. Then sign a per-camera
# certificate. Designed to run as root via sudo nohup; survives SSH
# disconnect.
set -u

# Pinned release tag; override with VAULT_IMAGE=... for a one-off run.
# v0.2.0 does not exist yet: the PM tags it on main after the B3/B4/B5 PR and
# F4 (verify --only-unverified) merge. See docs/release.md.
IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.0}"
LOG="${VAULT_LOG:-/volume1/docker/verify-certify.log}"

# One line to $LOG, and to the terminal only when there is one. Launched as
# `sudo nohup ./nas-verify-certify-all.sh >> $LOG 2>&1 &`, a `tee -a $LOG`
# wrote every line twice (B27): once itself, once through stdout. Now stdout
# only gets a copy when someone is watching it.
log() {
  if [ -t 1 ]; then
    echo "$@" | tee -a "$LOG"
  else
    echo "$@" >> "$LOG"
  fi
}

# (disk, host root) pairs — keep aligned with the migration.
disks=(
  "media-djiflip:/volume1/media/DJIFlip"
  "media-djimini2:/volume1/media/DJIMini2"
  "media-iphone:/volume1/media/iPhone"
  "media-sonya6700:/volume1/media/SonyA6700"
  "media-sonyzve10:/volume1/media/SonyZVE10"
  "media-gopro:/volume1/media/GoPro"
)

log "[$(date)] starting verify+certify pass"

failures=0
for entry in "${disks[@]}"; do
  disk="${entry%%:*}"
  root="${entry#*:}"

  log ""
  log "[$(date)] === VERIFY $disk at $root ==="
  if ! docker run --rm -v /volume1:/volume1 -e VAULT_CONFIG=/volume1/docker/vault-nas-config "$IMG" \
      verify "$disk" "$root" >> "$LOG" 2>&1; then
    log "[$(date)] VERIFY FAILED for $disk — skipping certify"
    failures=$((failures+1))
    continue
  fi

  log "[$(date)] === CERTIFY $disk ==="
  if ! docker run --rm -v /volume1:/volume1 -e VAULT_CONFIG=/volume1/docker/vault-nas-config "$IMG" \
      certify "$disk" "$root/$disk.cert.json" >> "$LOG" 2>&1; then
    log "[$(date)] CERTIFY FAILED for $disk"
    failures=$((failures+1))
    continue
  fi
  log "[$(date)] $disk done — cert at $root/$disk.cert.json"
done

log ""
log "[$(date)] all done — $failures failures across ${#disks[@]} disks"
