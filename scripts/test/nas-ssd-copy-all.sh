#!/bin/bash
# Tests scripts/nas-ssd-copy-all.sh for both labels (tars, kipp) with docker
# shadowed by a stub on PATH (nothing is pulled, mounted or run) and VAULT_LOG
# at a temp file, so /volume1 is never opened. The stub records every argv
# vector one argument per line, terminated by a marker, so each label's calls
# are compared as ordered vectors with boundaries — order, quoting and flags all
# pinned. The source path carries a space so lost quoting shows as a split.
set -u

here=$(cd "$(dirname "$0")" && pwd)
script="$here/../nas-ssd-copy-all.sh"
work=$(mktemp -d "${TMPDIR:-/tmp}/ssd-copy-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"

IMG="ghcr.io/eddyvarelae/media-vault:v0.2.4"

failures=0
check() { local desc=$1; shift; if "$@"; then echo "  ok   $desc"; else echo "  FAIL $desc"; failures=$((failures + 1)); fi; }
count() { grep -cF -- "$1" "$2" || true; }
same() { diff -q "$1" "$2" > /dev/null; }

check "IMG default is v0.2.4" \
  grep -qF 'IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.4}"' "$script"
check "kipp requires SSD_SRC (no default)" grep -qF 'SSD_SRC:?' "$script"
check "one tee, in the log helper only (B35; comments excluded)" \
  test "$(grep -v '^[[:space:]]*#' "$script" | grep -c 'tee -a')" -eq 1

stub() { # stub <exit-code> <calls-file>: one argument per line, then a marker
  cat > "$work/bin/docker" <<STUB
#!/bin/bash
printf '%s\\n' "\$@" >> "$2"
echo '--END--' >> "$2"
exit $1
STUB
  chmod +x "$work/bin/docker"
}

# folder_flags <folder>: the per-folder routing flags, one per line (empty for
# the flat cameras). GoPro's flags are the same in both tables.
folder_flags() {
  case "$1" in
    GoPro)   printf '%s\n' --prefix DCIM --rule MP4=Videos --rule LRV=Videos --rule THM=Videos --rule JPG=Photos --rule sav=Other ;;
    DJIFlip) printf '%s\n' --prefix DCIM --rule MP4=Videos --rule SRT=FlightLogs --rule JPG=Photos ;;
  esac
}

# expected <label> <src> <img> <dry-flag-or-empty>: the ordered argv vectors.
expected() {
  local label=$1 src=$2 img=$3 dry=$4 pairs dedupe pair folder
  local common="run
--rm
-v
/volume1:/volume1
-v
/mnt/@usb:/usb:ro
-e
VAULT_CONFIG=/volume1/docker/vault-nas-config
$img
copy"
  case "$label" in
    tars) pairs="media-djiflip:DJIFlip media-gopro:GoPro media-sonya6700:SonyA6700 media-sonyzve10:SonyZVE10"; dedupe="" ;;
    kipp) pairs="media-sonya6700:SonyA6700 media-backup:Backup media-multicam:Multicam media-auditorium:Auditorium media-gopro:GoPro media-sonyzve10:SonyZVE10 media-leantank:LeanTank"; dedupe="--dedupe-content" ;;
  esac
  for pair in $pairs; do
    folder=${pair#*:}
    echo "$common"
    echo "${pair%%:*}"
    echo "$src/$folder"
    echo "/volume1/media/$folder"
    folder_flags "$folder"
    [ -n "$dedupe" ] && echo "$dedupe"
    echo "--on-collision"
    echo "rename-mtime-year"
    [ -n "$dry" ] && echo "$dry"
    echo '--END--'
  done
}

# usage / bad label: exit 2, no docker call.
calls="$work/none.calls"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$work/none.log" bash "$script" > "$work/none.out" 2>&1
check "no label: exits 2" test $? -eq 2
PATH="$work/bin:$PATH" VAULT_LOG="$work/none.log" bash "$script" bogus > "$work/bad.out" 2>&1
check "unknown label: exits 2" test $? -eq 2
check "unknown label: usage names both labels" grep -q "tars | kipp" "$work/bad.out"
check "bad labels never called docker" test ! -s "$calls"

# kipp with no SSD_SRC: refuses before any docker call.
calls="$work/nosrc.calls"; : > "$calls"; stub 0 "$calls"
( unset SSD_SRC; PATH="$work/bin:$PATH" VAULT_LOG="$work/nosrc.log" bash "$script" kipp > "$work/nosrc.out" 2>&1 )
check "kipp without SSD_SRC exits non-zero" test $? -ne 0
check "kipp without SSD_SRC never called docker" test ! -s "$calls"
check "kipp without SSD_SRC names the variable" grep -q "SSD_SRC" "$work/nosrc.out"

# tars real run: 4 ordered vectors, source /usb/sdc1 by default, no dedupe.
calls="$work/tars.calls"; log="$work/tars.log"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$log" bash "$script" tars >> "$log" 2>&1
check "tars: exits 0 when every copy succeeds" test $? -eq 0
expected tars /usb/sdc1 "$IMG" "" > "$work/tars.want"
if ! diff -u "$work/tars.want" "$calls" > "$work/tars.diff"; then cat "$work/tars.diff"; fi
check "tars: four vectors, in order, argument by argument (no --dedupe-content)" test ! -s "$work/tars.diff"
check "tars: four per-folder done lines" test "$(grep -Ec '\] [A-Za-z0-9]+ done$' "$log")" -eq 4
check "tars: start line names the label and source" grep -qF "starting tars → media copy from /usb/sdc1" "$log"
check "tars: final line reports 0 failures" grep -q 'all tars copies done — 0 folder(s) FAILED' "$log"

# kipp real run: 7 ordered vectors, source from SSD_SRC, --dedupe-content each,
# a space in the source proves quoting survives.
SRC="/usb/kipp disk"
calls="$work/kipp.calls"; log="$work/kipp.log"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$log" SSD_SRC="$SRC" bash "$script" kipp >> "$log" 2>&1
check "kipp: exits 0 when every copy succeeds" test $? -eq 0
expected kipp "$SRC" "$IMG" "" > "$work/kipp.want"
if ! diff -u "$work/kipp.want" "$calls" > "$work/kipp.diff"; then cat "$work/kipp.diff"; fi
check "kipp: seven vectors, in order, argument by argument (source with a space intact)" test ! -s "$work/kipp.diff"
check "kipp: seven per-folder done lines" test "$(grep -Ec '\] [A-Za-z0-9]+ done$' "$log")" -eq 7
check "kipp: final line reports 0 failures" grep -q 'all kipp copies done — 0 folder(s) FAILED' "$log"

# DRY_RUN: the same vectors, each with --dry-run last.
calls="$work/dry.calls"; log="$work/dry.log"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$log" SSD_SRC="$SRC" DRY_RUN=1 bash "$script" kipp > /dev/null 2>&1
expected kipp "$SRC" "$IMG" "--dry-run" > "$work/dry.want"
if ! diff -u "$work/dry.want" "$calls" > "$work/dry.diff"; then cat "$work/dry.diff"; fi
check "DRY_RUN=1: seven vectors, each ending in --dry-run" test ! -s "$work/dry.diff"
check "DRY_RUN=1: the start line says so" grep -q '(DRY RUN)' "$log"

# Failures: each FAILED folder logged, the pass continues, exit non-zero, count.
calls="$work/fail.calls"; log="$work/fail.log"; : > "$calls"; stub 1 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$log" bash "$script" tars > /dev/null 2>&1
check "stub exit 1: tars exits non-zero" test $? -ne 0
check "stub exit 1: all four still attempted, in order" same "$work/tars.want" "$calls"
check "stub exit 1: four per-folder FAILED lines" test "$(grep -Ec '\] [A-Za-z0-9]+ FAILED$' "$log")" -eq 4
check "stub exit 1: DJIFlip logged FAILED" grep -q 'DJIFlip FAILED' "$log"
check "stub exit 1: no per-folder done lines" test "$(grep -Ec '\] [A-Za-z0-9]+ done$' "$log")" -eq 0
check "stub exit 1: final line counts 4" grep -q 'all tars copies done — 4 folder(s) FAILED' "$log"

# VAULT_IMAGE override: same vectors with the other image.
calls="$work/img.calls"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$work/img.log" VAULT_IMAGE=example/vault:sha-abc bash "$script" tars > /dev/null 2>&1
expected tars /usb/sdc1 example/vault:sha-abc "" > "$work/img.want"
check "VAULT_IMAGE overrides the tag in every vector" same "$work/img.want" "$calls"

# SSD_SRC override for tars: the source root moves off /usb/sdc1.
calls="$work/src.calls"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$work/src.log" SSD_SRC=/usb/other bash "$script" tars > /dev/null 2>&1
expected tars /usb/other "$IMG" "" > "$work/src.want"
check "SSD_SRC overrides the tars source root" same "$work/src.want" "$calls"

echo
if [ "$failures" -ne 0 ]; then echo "$failures check(s) failed"; exit 1; fi
echo "all checks passed"
