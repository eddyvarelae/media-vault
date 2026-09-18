#!/bin/bash
# Tests the shell side of the F3 contract: when `vault copy` exits non-zero,
# scripts/nas-tars-copy-all.sh must log the card as FAILED, and when it exits
# zero, as done. Docker is shadowed by a stub on PATH, so nothing is pulled,
# mounted, or run; VAULT_LOG points the log at a temp file, so /volume1 is
# never opened. Safe to run anywhere; `go test ./scripts/test/` runs it.
set -u

here=$(cd "$(dirname "$0")" && pwd)
script="$here/../nas-tars-copy-all.sh"
work=$(mktemp -d "${TMPDIR:-/tmp}/tars-copy-test.XXXXXX")
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

# The production default must survive: the override is for tests only.
check "LOG default is still the production path" \
  grep -qF 'LOG="${VAULT_LOG:-/volume1/docker/tars-copy.log}"' "$script"

run_case() { # run_case <name> <stub-exit-code>
  local name=$1 code=$2
  local log="$work/$name.log" calls="$work/$name.calls"
  : > "$calls"
  cat > "$work/bin/docker" <<STUB
#!/bin/bash
echo "\$*" >> "$calls"
exit $code
STUB
  chmod +x "$work/bin/docker"
  PATH="$work/bin:$PATH" VAULT_LOG="$log" bash "$script" > "$work/$name.stdout" 2>&1
  echo "exit $? (script)" >> "$work/$name.stdout"
}

cards="DJIFlip GoPro SonyA6700 SonyZVE10"

echo "case: every card's docker run exits 1"
run_case failing 1
log="$work/failing.log"
check "log exists at VAULT_LOG" test -s "$log"
check "the stub was what ran, four times" test "$(wc -l < "$work/failing.calls")" -eq 4
check "stub saw the real copy arguments" \
  grep -q 'copy media-djiflip /usb/sdc1/DJIFlip /volume1/media/DJIFlip --prefix DCIM .*--on-collision rename-mtime-year' "$work/failing.calls"
for card in $cards; do
  check "$card logged FAILED" grep -qE "^\[.*\] $card FAILED\$" "$log"
  check "$card not logged done" bash -c "! grep -qE '^\[.*\] $card done\$' '$log'"
done
check "exactly four FAILED lines" test "$(grep -c ' FAILED$' "$log")" -eq 4
check "the pass still runs to the end" grep -q 'all tars copies done' "$log"

echo "case: every card's docker run exits 0"
run_case passing 0
log="$work/passing.log"
check "the stub ran four times" test "$(wc -l < "$work/passing.calls")" -eq 4
for card in $cards; do
  check "$card logged done" grep -qE "^\[.*\] $card done\$" "$log"
done
check "no FAILED lines" bash -c "! grep -q 'FAILED' '$log'"

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed; script output:"
  cat "$work/failing.stdout" "$work/passing.stdout"
  exit 1
fi
echo "all checks passed"
