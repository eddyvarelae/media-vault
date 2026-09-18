#!/bin/bash
# Tests scripts/nas-kipp-copy-all.sh with docker shadowed by a stub on PATH
# (nothing is pulled, mounted or run) and VAULT_LOG at a temp file, so
# /volume1 is never opened. Checks the runbook step-3 contract: KIPP_SRC
# required, one copy per folder with the dedupe + rename flags, the new
# disk names, DRY_RUN=1 on every call, FAILED logging, single logging.
set -u

here=$(cd "$(dirname "$0")" && pwd)
script="$here/../nas-kipp-copy-all.sh"
work=$(mktemp -d "${TMPDIR:-/tmp}/kipp-copy-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"

failures=0
check() { # check <description> <command...>
  local desc=$1; shift
  if "$@"; then
    echo "  ok   $desc"
  else
    echo "  FAIL $desc"
    failures=$((failures + 1))
  fi
}
count() { grep -cF -- "$1" "$2" || true; }

check "IMG default is v0.2.1" \
  grep -qF 'IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.1}"' "$script"
check "LOG default is the kipp log" \
  grep -qF 'LOG="${VAULT_LOG:-/volume1/docker/kipp-copy.log}"' "$script"
check "KIPP_SRC has no default" \
  grep -qF 'SRC="${KIPP_SRC:?' "$script"

stub() { # stub <exit-code> <calls-file>
  cat > "$work/bin/docker" <<STUB
#!/bin/bash
echo "\$*" >> "$2"
exit $1
STUB
  chmod +x "$work/bin/docker"
}

# 1. No KIPP_SRC: refuses before any docker call.
calls="$work/none.calls"; : > "$calls"; stub 0 "$calls"
( unset KIPP_SRC; PATH="$work/bin:$PATH" VAULT_LOG="$work/none.log" bash "$script" > "$work/none.out" 2>&1 )
check "without KIPP_SRC the script exits non-zero" test $? -ne 0
check "without KIPP_SRC docker is never called" test ! -s "$calls"
check "without KIPP_SRC the message names the variable" grep -q "KIPP_SRC" "$work/none.out"

# 2. Real run shape.
calls="$work/run.calls"; log="$work/run.log"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$log" KIPP_SRC=/usb/sdd1 bash "$script" >> "$log" 2>&1
check "exits 0 when every copy succeeds" test $? -eq 0
check "seven copy calls, one per folder" test "$(count ' copy ' "$calls")" -eq 7
for pair in media-sonya6700:SonyA6700 media-backup:Backup media-multicam:Multicam media-auditorium:Auditorium media-gopro:GoPro media-sonyzve10:SonyZVE10 media-leantank:LeanTank; do
  disk="${pair%%:*}"; folder="${pair#*:}"
  check "$folder copies from KIPP_SRC to /volume1/media/$folder under $disk" \
    grep -q " copy $disk /usb/sdd1/$folder /volume1/media/$folder " "$calls"
done
for folder in SonyA6700 Backup Multicam Auditorium SonyZVE10 LeanTank; do
  check "$folder is copied verbatim (no routing flags, runbook step 2)" \
    grep -q " /volume1/media/$folder --dedupe-content --on-collision rename-mtime-year\$" "$calls"
done
check "every call carries --dedupe-content" test "$(count '--dedupe-content' "$calls")" -eq 7
check "every call carries --on-collision rename-mtime-year" test "$(count '--on-collision rename-mtime-year' "$calls")" -eq 7
check "no call carries --dry-run" test "$(count '--dry-run' "$calls")" -eq 0
check "GoPro keeps the tars routing flags" \
  grep -q " copy media-gopro /usb/sdd1/GoPro /volume1/media/GoPro --prefix DCIM --rule MP4=Videos --rule LRV=Videos --rule THM=Videos --rule JPG=Photos --rule sav=Other --dedupe-content" "$calls"
check "the image is the pinned tag" test "$(count 'ghcr.io/eddyvarelae/media-vault:v0.2.1 copy' "$calls")" -eq 7
check "the manifest config dir is mounted read-write and /mnt/@usb read-only" \
  grep -q -- '-v /volume1:/volume1 -v /mnt/@usb:/usb:ro -e VAULT_CONFIG=/volume1/docker/vault-nas-config' "$calls"
check "seven per-folder done lines" test "$(grep -Ec '\] [A-Za-z0-9]+ done$' "$log")" -eq 7
check "no per-folder FAILED lines" test "$(grep -Ec '\] [A-Za-z0-9]+ FAILED$' "$log")" -eq 0
check "final line reports 0 failures" grep -q 'all kipp copies done — 0 folder(s) FAILED' "$log"
check "nohup form logs the start line exactly once (B27)" test "$(count 'starting kipp' "$log")" -eq 1
check "start line names the source" grep -q 'starting kipp → media copy from /usb/sdd1$' "$log"

# 3. DRY_RUN=1: every call gets --dry-run, nothing else changes.
calls="$work/dry.calls"; log="$work/dry.log"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$log" KIPP_SRC=/usb/sdd1 DRY_RUN=1 bash "$script" > /dev/null 2>&1
check "DRY_RUN=1: seven calls" test "$(count ' copy ' "$calls")" -eq 7
check "DRY_RUN=1: every call ends with --dry-run" test "$(grep -c -- ' --on-collision rename-mtime-year --dry-run$' "$calls")" -eq 7
check "DRY_RUN=1: the start line says so" grep -q '(DRY RUN)' "$log"

# 4. Failures: each FAILED folder is logged, the pass continues, exit 1.
calls="$work/fail.calls"; log="$work/fail.log"; : > "$calls"; stub 1 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$log" KIPP_SRC=/usb/sdd1 bash "$script" > /dev/null 2>&1
check "stub exit 1: script exits non-zero" test $? -ne 0
check "stub exit 1: all seven still attempted" test "$(count ' copy ' "$calls")" -eq 7
check "stub exit 1: seven per-folder FAILED lines" test "$(grep -Ec '\] [A-Za-z0-9]+ FAILED$' "$log")" -eq 7
check "stub exit 1: SonyA6700 logged FAILED" grep -q 'SonyA6700 FAILED' "$log"
check "stub exit 1: no per-folder done lines" test "$(grep -Ec '\] [A-Za-z0-9]+ done$' "$log")" -eq 0
check "stub exit 1: final line counts 7" grep -q 'all kipp copies done — 7 folder(s) FAILED' "$log"

# 5. VAULT_IMAGE override.
calls="$work/img.calls"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$work/img.log" KIPP_SRC=/usb/sdd1 VAULT_IMAGE=example/vault:sha-abc bash "$script" > /dev/null 2>&1
check "VAULT_IMAGE overrides the tag" test "$(count 'example/vault:sha-abc copy' "$calls")" -eq 7

echo
if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all checks passed"
