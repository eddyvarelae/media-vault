#!/bin/bash
# Quick NAS-side smoke test of media-vault.
# Pulls the image, runs scan/copy/verify/certify against /mnt/@usb/sdc1/Test
# with destination at /volume1/docker/vault-nas-test.
set -e

IMG=ghcr.io/eddyvarelae/media-vault:latest
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
  "$IMG" certify tars /dest/tars-test.cert.json

echo
echo "Done. Certificate at $DST/tars-test.cert.json"
