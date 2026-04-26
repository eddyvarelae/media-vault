#!/bin/bash
# Inventory the existing 5.6 TB on the NAS, split per top-folder so dedup
# can answer cross-folder questions cleanly. Reuses the existing
# /volume1/docker/vault-nas-config manifest so results pile up alongside
# whatever's already there.
set -e

IMG=ghcr.io/eddyvarelae/media-vault:latest
HOME_ROOT=/volume1/@home/figmaboi
CFG=/volume1/docker/vault-nas-config

sudo -v
sudo docker pull "$IMG"

run_inv() {
  local name="$1"
  local relpath="$2"
  echo
  echo "=== inventory $name ==="
  sudo docker run --rm \
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
echo "=== DEDUP REPORT (>= 1 MiB) ==="
sudo docker run --rm \
  -v "$CFG":/config \
  "$IMG" dedup --min-size 1048576

echo
echo "=== UNIQUE TO #recycle (would be lost if emptied) ==="
sudo docker run --rm \
  -v "$CFG":/config \
  "$IMG" unique nas-recycle

echo
echo "=== UNIQUE TO Temp Footage ==="
sudo docker run --rm \
  -v "$CFG":/config \
  "$IMG" unique nas-tempfootage

echo
echo "Done."
