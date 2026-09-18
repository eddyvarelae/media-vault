#!/bin/bash
# Tests scripts/nas-verify-certify-all.sh, both contracts it carries:
#   B25 - every certificate is written under $CERTS (beside the manifest) with
#         --root <camera root>, never under the camera root it certifies;
#   B27 - every log line is written once, whether launched the nohup way
#         (stdout redirected into the very same log) or with stdout elsewhere.
# Docker is shadowed by a stub on PATH (nothing is pulled, mounted or run);
# VAULT_LOG and VAULT_CERTS point at a temp dir, so /volume1 is never opened.
# The terminal form (tee to a tty) needs a pty and is not exercised here.
set -u

here=$(cd "$(dirname "$0")" && pwd)
script="$here/../nas-verify-certify-all.sh"
work=$(mktemp -d "${TMPDIR:-/tmp}/verify-certify-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
cat > "$work/bin/docker" <<'STUB'
#!/bin/bash
echo "docker $*"
echo "$*" >> "${CALLS:?}"
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
count() { grep -cF -- "$1" "$2" || true; }

check "LOG default is still the production path" \
  grep -qF 'LOG="${VAULT_LOG:-/volume1/docker/verify-certify.log}"' "$script"
check "CERTS default is beside the manifest" \
  grep -qF 'CERTS="${VAULT_CERTS:-/volume1/docker/vault-certs}"' "$script"
check "no tee outside the log helper (comments excluded)" \
  test "$(grep -v '^\s*#' "$script" | grep -c 'tee -a')" -eq 1

# ── The nohup launch: stdout and stderr are the log itself. ──────────────
log="$work/nohup.log"; calls="$work/nohup.calls"; certs="$work/certs"; : > "$calls"
PATH="$work/bin:$PATH" CALLS="$calls" VAULT_LOG="$log" VAULT_CERTS="$certs" bash "$script" >> "$log" 2>&1
check "nohup form: exits 0 when every docker call succeeds" test $? -eq 0

# B25: where the certificates go.
check "certs dir was created" test -d "$certs"
check "six certify calls" test "$(count ' certify ' "$calls")" -eq 6
outs=$(grep ' certify ' "$calls" | sed -E 's/.* certify [^ ]+ ([^ ]+) --root .*/\1/')
check "six certify output paths extracted" test "$(printf '%s\n' "$outs" | grep -c .)" -eq 6
check "every certify output is under CERTS" \
  test "$(printf '%s\n' "$outs" | grep -c "^$certs/media-[a-z0-9]*\.cert\.json\$")" -eq 6
# Negations are computed first, then asserted: `check ! a | b` would hand
# `!` to check as a command and run check in a subshell (review #12).
under_media=$(printf '%s\n' "$outs" | grep -c '^/volume1/media/' || true)
check "no certify output under a camera root" test "$under_media" -eq 0
check "certify for media-djiflip names its cert and its root" \
  grep -q " certify media-djiflip $certs/media-djiflip.cert.json --root /volume1/media/DJIFlip\$" "$calls"
check "verify still runs against the camera roots" \
  grep -q ' verify media-djiflip /volume1/media/DJIFlip$' "$calls"

# B27: each line once, under the launch form that used to double them.
check "nohup form: 'starting' logged exactly once" test "$(count 'starting verify+certify pass' "$log")" -eq 1
check "nohup form: 'all done' logged exactly once" test "$(count 'all done' "$log")" -eq 1
check "nohup form: six 'done — cert at' lines, one per disk, naming CERTS" \
  test "$(grep -c "done — cert at $certs/media-[a-z0-9]*\.cert\.json\$" "$log")" -eq 6
check "nohup form: docker output still lands in the log" test "$(count 'docker run' "$log")" -eq 12

# ── Stdout elsewhere (a file, not a terminal): the log is complete, once. ─
log2="$work/plain.log"; out2="$work/plain.stdout"; calls2="$work/plain.calls"; : > "$calls2"
PATH="$work/bin:$PATH" CALLS="$calls2" VAULT_LOG="$log2" VAULT_CERTS="$work/certs2" bash "$script" > "$out2" 2>&1
check "plain form: 'starting' logged exactly once" test "$(count 'starting verify+certify pass' "$log2")" -eq 1
check "plain form: 'all done' logged exactly once" test "$(count 'all done' "$log2")" -eq 1
check "plain form: nothing echoed to a non-terminal stdout" test ! -s "$out2"

echo
if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all checks passed"
