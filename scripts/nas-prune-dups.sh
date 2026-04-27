#!/bin/bash
# Delete in-folder " 2." duplicates from the NAS, with hash verification.
#
# For every (disk, "X 2.ext") pair in the manifest where (disk, "X.ext")
# exists with the same sha256:
#   1. Re-read "X.ext" from disk and re-hash it.
#   2. If the keeper's hash matches the manifest, delete "X 2.ext".
#   3. Remove the deleted row from the manifest.
#
# Designed to run as root via sudo. Logs every action to prune.log.

set -u
IMG=ghcr.io/eddyvarelae/media-vault:latest
CFG=/volume1/docker/vault-nas-config
LOG=/volume1/docker/prune.log
LIST=/volume1/docker/prune-list.tsv

# Map disk name → host path (must align with what the inventory pass used)
get_root() {
  case "$1" in
    nas-gopro)       echo "/volume1/@home/figmaboi/GoPro" ;;
    nas-tempfootage) echo "/volume1/@home/figmaboi/Temp Footage" ;;
    nas-lastone)     echo "/volume1/@home/figmaboi/Last one" ;;
    nas-backups)     echo "/volume1/@home/figmaboi/Backups" ;;
    nas-recycle)     echo "/volume1/@home/figmaboi/#recycle" ;;
    *) return 1 ;;
  esac
}

echo "=== prune started $(date) ===" | tee -a "$LOG"

# Step 1: pull the deletion list from the manifest in one query.
docker run --rm -v "$CFG":/config --entrypoint sh "$IMG" -c \
  "apk add --no-cache sqlite > /dev/null 2>&1 && sqlite3 -separator '|' /config/manifest.db \"
    SELECT a.source_disk, a.source_path, b.source_path, a.sha256, a.size
    FROM files a
    JOIN files b ON a.sha256=b.sha256
                 AND a.source_path != b.source_path
                 AND a.source_disk = b.source_disk
    WHERE a.source_path LIKE '% 2.%'
      AND b.source_path NOT LIKE '% 2.%'
      AND replace(a.source_path, ' 2.', '.') = b.source_path
    ORDER BY a.size DESC;\"" > "$LIST"

count=$(wc -l < "$LIST")
echo "Plan: $count files queued for deletion (hash-verified before each rm)" | tee -a "$LOG"

if [ "$count" -eq 0 ]; then
  echo "Nothing to do." | tee -a "$LOG"
  exit 0
fi

# Step 2: for each row, verify the keeper, then rm the duplicate.
DELETIONS=()
SKIPS=0
DELS=0
BYTES=0
while IFS='|' read -r disk to_delete keeper sha size; do
  [ -z "$disk" ] && continue
  ROOT=$(get_root "$disk") || { echo "SKIP (unknown disk: $disk)" | tee -a "$LOG"; SKIPS=$((SKIPS+1)); continue; }
  KEEPER="$ROOT/$keeper"
  DUP="$ROOT/$to_delete"

  if [ ! -f "$KEEPER" ]; then
    echo "SKIP (keeper missing on disk): $disk: $keeper" | tee -a "$LOG"
    SKIPS=$((SKIPS+1))
    continue
  fi
  if [ ! -f "$DUP" ]; then
    echo "SKIP (duplicate already gone): $disk: $to_delete" | tee -a "$LOG"
    SKIPS=$((SKIPS+1))
    continue
  fi

  echo "VERIFY $disk: $keeper ..." | tee -a "$LOG"
  actual_sha=$(sha256sum "$KEEPER" | awk '{print $1}')
  if [ "$actual_sha" != "$sha" ]; then
    echo "SKIP (keeper hash mismatch: got $actual_sha, expected $sha): $disk: $keeper" | tee -a "$LOG"
    SKIPS=$((SKIPS+1))
    continue
  fi

  if rm -- "$DUP"; then
    echo "DELETED $disk: $to_delete (matched $keeper sha ${sha:0:12}…, freed $size bytes)" | tee -a "$LOG"
    DELETIONS+=("$disk|$to_delete")
    DELS=$((DELS+1))
    BYTES=$((BYTES + size))
  else
    echo "FAILED rm $disk: $DUP" | tee -a "$LOG"
    SKIPS=$((SKIPS+1))
  fi
done < "$LIST"

# Step 3: remove deleted rows from the manifest in one round-trip.
if [ ${#DELETIONS[@]} -gt 0 ]; then
  SQL=""
  for entry in "${DELETIONS[@]}"; do
    disk="${entry%%|*}"
    path="${entry#*|}"
    # Escape single quotes in path
    path_esc=${path//\'/\'\'}
    SQL+="DELETE FROM files WHERE source_disk='$disk' AND source_path='$path_esc';"
  done
  docker run --rm -v "$CFG":/config --entrypoint sh "$IMG" -c \
    "apk add --no-cache sqlite > /dev/null 2>&1 && sqlite3 /config/manifest.db \"$SQL\""
  echo "Removed $DELS rows from manifest" | tee -a "$LOG"
fi

echo "=== prune done $(date) — deleted $DELS, skipped $SKIPS, freed $BYTES bytes ===" | tee -a "$LOG"
