#!/bin/bash
# Inventory the existing 5.6 TB on the NAS, split per top-folder so dedup
# can answer cross-folder questions cleanly. Reuses the existing
# /volume1/docker/vault-nas-config manifest so results pile up alongside
# whatever's already there.
#
# Run as root (script is invoked via sudo). Designed to be detachable with
# nohup so it survives SSH disconnect — the full pass takes hours.
set -e

IMG=ghcr.io/eddyvarelae/media-vault:latest
HOME_ROOT=/volume1/@home/figmaboi
CFG=/volume1/docker/vault-nas-config

echo "[$(date)] starting inventory pass"
docker pull "$IMG"

run_inv() {
  local name="$1"
  local relpath="$2"
  echo
  echo "[$(date)] === inventory $name ($relpath) ==="
  docker run --rm \
    -v "$HOME_ROOT/$relpath":/sources:ro \
    -v "$CFG":/config \
    "$IMG" inventory "$name" /sources
}

run_inv nas-gopro       "GoPro"
run_inv nas-lastone     "Last one"
run_inv nas-backups     "Backups"
run_inv nas-tempfootage "Temp Footage"
run_inv nas-recycle     "#recycle"

echo
echo "[$(date)] === DEDUP REPORT (>= 1 MiB) ==="
docker run --rm \
  -v "$CFG":/config \
  "$IMG" dedup --min-size 1048576

echo
echo "[$(date)] === UNIQUE TO #recycle (would be lost if emptied) ==="
docker run --rm \
  -v "$CFG":/config \
  "$IMG" unique nas-recycle

echo
echo "[$(date)] === UNIQUE TO Temp Footage ==="
docker run --rm \
  -v "$CFG":/config \
  "$IMG" unique nas-tempfootage

echo
echo "[$(date)] done"
