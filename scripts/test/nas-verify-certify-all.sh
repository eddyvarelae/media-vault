#!/bin/bash
# Tests B27: scripts/nas-verify-certify-all.sh writes each log line once,
# whether it is launched the nohup way (stdout redirected into the very same
# log) or with stdout going elsewhere. Docker is shadowed by a stub on PATH;
# VAULT_LOG points at a temp file, so /volume1 is never opened. The terminal
# form (tee to a tty) needs a pty and is not exercised here.
set -u

here=$(cd "$(dirname "$0")" && pwd)
script="$here/../nas-verify-certify-all.sh"
work=$(mktemp -d "${TMPDIR:-/tmp}/verify-certify-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
cat > "$work/bin/docker" <<'STUB'
#!/bin/bash
echo "docker $*"
exit 0
STUB
chmod +x "$work/bin/docker"

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
count() { grep -cF "$1" "$2"; }

check "LOG default is still the production path" \
  grep -qF 'LOG="${VAULT_LOG:-/volume1/docker/verify-certify.log}"' "$script"
check "no tee outside the log helper (comments excluded)" \
  test "$(grep -v '^\s*#' "$script" | grep -c 'tee -a')" -eq 1

# The nohup launch: stdout and stderr are the log itself.
log="$work/nohup.log"
PATH="$work/bin:$PATH" VAULT_LOG="$log" bash "$script" >> "$log" 2>&1
check "nohup form: exits 0" test $? -eq 0
check "nohup form: 'starting' logged exactly once" test "$(count 'starting verify+certify pass' "$log")" -eq 1
check "nohup form: 'all done' logged exactly once" test "$(count 'all done' "$log")" -eq 1
check "nohup form: six 'done — cert at' lines, one per disk" test "$(count 'done — cert at' "$log")" -eq 6
check "nohup form: docker output still lands in the log" test "$(count 'docker run' "$log")" -eq 12

# Stdout elsewhere (a file, not a terminal): the log is complete, once.
log2="$work/plain.log"; out2="$work/plain.stdout"
PATH="$work/bin:$PATH" VAULT_LOG="$log2" bash "$script" > "$out2" 2>&1
check "plain form: 'starting' logged exactly once" test "$(count 'starting verify+certify pass' "$log2")" -eq 1
check "plain form: 'all done' logged exactly once" test "$(count 'all done' "$log2")" -eq 1
check "plain form: nothing echoed to a non-terminal stdout" test ! -s "$out2"

echo
if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all checks passed"
