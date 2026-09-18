#!/bin/bash
# Tests scripts/nas-kipp-copy-all.sh with docker shadowed by a stub on PATH
# (nothing is pulled, mounted or run) and VAULT_LOG at a temp file, so
# /volume1 is never opened. The stub records every argv vector one argument
# per line, terminated by a marker, so the test compares the seven calls
# as ordered vectors with boundaries - order, quoting and flags all pinned.
# The source path carries a space so lost quoting shows as a split vector.
set -u

here=$(cd "$(dirname "$0")" && pwd)
script="$here/../nas-kipp-copy-all.sh"
work=$(mktemp -d "${TMPDIR:-/tmp}/kipp-copy-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"

SRC="/usb/kipp disk"
IMG="ghcr.io/eddyvarelae/media-vault:v0.2.1"

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
same() { diff -q "$1" "$2" > /dev/null; }

check "IMG default is v0.2.1" \
  grep -qF 'IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.1}"' "$script"
check "LOG default is the kipp log" \
  grep -qF 'LOG="${VAULT_LOG:-/volume1/docker/kipp-copy.log}"' "$script"
check "KIPP_SRC has no default" \
  grep -qF 'SRC="${KIPP_SRC:?' "$script"
check "the dry-run usage line runs under sudo -E like the real launch" \
  grep -qF 'DRY_RUN=1 sudo -E ./nas-kipp-copy-all.sh' "$script"
check "the usage comment does not claim the dry run writes nothing" \
  test "$(grep -c 'writes nothing' "$script")" -eq 0

stub() { # stub <exit-code> <calls-file>: one argument per line, then a marker
  cat > "$work/bin/docker" <<STUB
#!/bin/bash
printf '%s\\n' "\$@" >> "$2"
echo '--END--' >> "$2"
exit $1
STUB
  chmod +x "$work/bin/docker"
}

# expected <image> <dry-run-flag-or-empty>: the seven vectors, in order.
expected() {
  local img=$1 dry=$2
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
  local tail="--dedupe-content
--on-collision
rename-mtime-year"
  [ -n "$dry" ] && tail="$tail
$dry"
  local gopro="--prefix
DCIM
--rule
MP4=Videos
--rule
LRV=Videos
--rule
THM=Videos
--rule
JPG=Photos
--rule
sav=Other"
  for pair in media-sonya6700:SonyA6700 media-backup:Backup media-multicam:Multicam media-auditorium:Auditorium media-gopro:GoPro media-sonyzve10:SonyZVE10 media-leantank:LeanTank; do
    echo "$common"
    echo "${pair%%:*}"
    echo "$SRC/${pair#*:}"
    echo "/volume1/media/${pair#*:}"
    [ "${pair#*:}" = GoPro ] && echo "$gopro"
    echo "$tail"
    echo '--END--'
  done
}

# 1. No KIPP_SRC: refuses before any docker call.
calls="$work/none.calls"; : > "$calls"; stub 0 "$calls"
( unset KIPP_SRC; PATH="$work/bin:$PATH" VAULT_LOG="$work/none.log" bash "$script" > "$work/none.out" 2>&1 )
check "without KIPP_SRC the script exits non-zero" test $? -ne 0
check "without KIPP_SRC docker is never called" test ! -s "$calls"
check "without KIPP_SRC the message names the variable" grep -q "KIPP_SRC" "$work/none.out"

# 2. Real run: the seven ordered argv vectors, exactly.
calls="$work/run.calls"; log="$work/run.log"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$log" KIPP_SRC="$SRC" bash "$script" >> "$log" 2>&1
check "exits 0 when every copy succeeds" test $? -eq 0
expected "$IMG" "" > "$work/run.want"
if ! diff -u "$work/run.want" "$calls" > "$work/run.diff"; then cat "$work/run.diff"; fi
check "seven calls, in order, argument by argument (source with a space intact)" test ! -s "$work/run.diff"
check "seven per-folder done lines" test "$(grep -Ec '\] [A-Za-z0-9]+ done$' "$log")" -eq 7
check "no per-folder FAILED lines" test "$(grep -Ec '\] [A-Za-z0-9]+ FAILED$' "$log")" -eq 0
check "final line reports 0 failures" grep -q 'all kipp copies done — 0 folder(s) FAILED' "$log"
check "nohup form logs the start line exactly once (B27)" test "$(count 'starting kipp' "$log")" -eq 1
check "start line names the source" grep -qF "starting kipp → media copy from $SRC" "$log"

# 3. DRY_RUN=1: the same seven vectors, each with --dry-run last.
calls="$work/dry.calls"; log="$work/dry.log"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$log" KIPP_SRC="$SRC" DRY_RUN=1 bash "$script" > /dev/null 2>&1
expected "$IMG" "--dry-run" > "$work/dry.want"
if ! diff -u "$work/dry.want" "$calls" > "$work/dry.diff"; then cat "$work/dry.diff"; fi
check "DRY_RUN=1: seven vectors, each ending in --dry-run" test ! -s "$work/dry.diff"
check "DRY_RUN=1: the start line says so" grep -q '(DRY RUN)' "$log"

# 4. Failures: each FAILED folder is logged, the pass continues, exit 1.
calls="$work/fail.calls"; log="$work/fail.log"; : > "$calls"; stub 1 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$log" KIPP_SRC="$SRC" bash "$script" > /dev/null 2>&1
check "stub exit 1: script exits non-zero" test $? -ne 0
check "stub exit 1: all seven still attempted, in order" same "$work/run.want" "$calls"
check "stub exit 1: seven per-folder FAILED lines" test "$(grep -Ec '\] [A-Za-z0-9]+ FAILED$' "$log")" -eq 7
check "stub exit 1: SonyA6700 logged FAILED" grep -q 'SonyA6700 FAILED' "$log"
check "stub exit 1: no per-folder done lines" test "$(grep -Ec '\] [A-Za-z0-9]+ done$' "$log")" -eq 0
check "stub exit 1: final line counts 7" grep -q 'all kipp copies done — 7 folder(s) FAILED' "$log"

# 5. VAULT_IMAGE override: same vectors with the other image.
calls="$work/img.calls"; : > "$calls"; stub 0 "$calls"
PATH="$work/bin:$PATH" VAULT_LOG="$work/img.log" KIPP_SRC="$SRC" VAULT_IMAGE=example/vault:sha-abc bash "$script" > /dev/null 2>&1
expected example/vault:sha-abc "" > "$work/img.want"
check "VAULT_IMAGE overrides the tag in every vector" same "$work/img.want" "$calls"

echo
if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all checks passed"
