#!/bin/bash
# Re-hash every file under /volume1/media/<camera>/ and confirm the bytes
# still match what the manifest recorded. Then sign a per-camera
# certificate. Designed to run as root via sudo nohup; survives SSH
# disconnect.
set -u

IMG=ghcr.io/eddyvarelae/media-vault:latest
LOG=/volume1/docker/verify-certify.log

# (disk, host root) pairs — keep aligned with the migration.
disks=(
  "media-djiflip:/volume1/media/DJIFlip"
  "media-djimini2:/volume1/media/DJIMini2"
  "media-iphone:/volume1/media/iPhone"
  "media-sonya6700:/volume1/media/SonyA6700"
  "media-sonyzve10:/volume1/media/SonyZVE10"
  "media-gopro:/volume1/media/GoPro"
)

echo "[$(date)] starting verify+certify pass" | tee -a "$LOG"

failures=0
for entry in "${disks[@]}"; do
  disk="${entry%%:*}"
  root="${entry#*:}"

  echo | tee -a "$LOG"
  echo "[$(date)] === VERIFY $disk at $root ===" | tee -a "$LOG"
  if ! docker run --rm -v /volume1:/volume1 -e VAULT_CONFIG=/volume1/docker/vault-nas-config "$IMG" \
      verify "$disk" "$root" >> "$LOG" 2>&1; then
    echo "[$(date)] VERIFY FAILED for $disk — skipping certify" | tee -a "$LOG"
    failures=$((failures+1))
    continue
  fi

  echo "[$(date)] === CERTIFY $disk ===" | tee -a "$LOG"
  if ! docker run --rm -v /volume1:/volume1 -e VAULT_CONFIG=/volume1/docker/vault-nas-config "$IMG" \
      certify "$disk" "$root/$disk.cert.json" >> "$LOG" 2>&1; then
    echo "[$(date)] CERTIFY FAILED for $disk" | tee -a "$LOG"
    failures=$((failures+1))
    continue
  fi
  echo "[$(date)] $disk done — cert at $root/$disk.cert.json" | tee -a "$LOG"
done

echo | tee -a "$LOG"
echo "[$(date)] all done — $failures failures across ${#disks[@]} disks" | tee -a "$LOG"
