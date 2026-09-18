#!/bin/bash
# Quick NAS-side smoke test of media-vault.
# Pulls the image, runs scan/copy/verify/certify against /mnt/@usb/sdc1/Test
# with destination at /volume1/docker/vault-nas-test.
set -e

# Pinned release tag; override with VAULT_IMAGE=... for a one-off run.
IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.1}"
SRC=/mnt/@usb/sdc1/Test
DST=/volume1/docker/vault-nas-test
CFG=/volume1/docker/vault-nas-config

echo "=== sudo cache + pull ==="
sudo -v
sudo docker pull "$IMG"
sudo mkdir -p "$DST" "$CFG"

echo
echo "=== SCAN ==="
sudo docker run --rm \
  -v "$SRC":/sources:ro \
  -v "$DST":/dest \
  -v "$CFG":/config \
  "$IMG" scan tars /sources /dest

echo
echo "=== COPY ==="
time sudo docker run --rm \
  -v "$SRC":/sources:ro \
  -v "$DST":/dest \
  -v "$CFG":/config \
  "$IMG" copy tars /sources /dest

echo
echo "=== VERIFY ==="
time sudo docker run --rm \
  -v "$DST":/dest \
  -v "$CFG":/config \
  "$IMG" verify tars /dest

echo
echo "=== CERTIFY ==="
sudo docker run --rm \
  -v "$DST":/dest \
  -v "$CFG":/config \
  "$IMG" certify tars /config/tars-test.cert.json --root /dest   # beside the manifest, never under /dest (B25)

echo
echo "Done. Certificate at $CFG/tars-test.cert.json"
