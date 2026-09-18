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
# Restored from the pre-merge test (review #25): every certify call, not
# just DJIFlip's, passes --root, and the root it passes is the very root
# its own verify ran against.
check "all six certify calls pass --root under /volume1/media/" \
  test "$(grep ' certify ' "$calls" | grep -c -- ' --root /volume1/media/[A-Za-z0-9]*$')" -eq 6
# The six (disk, root) pairs are the expected ones, spelled out - not read
# back from the recording, which a script that verified and certified the
# wrong root would have satisfied (review #29). Each pair must have exactly
# one certify call, with its cert under CERTS and --root that very root.
expected_pairs="media-djiflip /volume1/media/DJIFlip
media-djimini2 /volume1/media/DJIMini2
media-iphone /volume1/media/iPhone
media-sonya6700 /volume1/media/SonyA6700
media-sonyzve10 /volume1/media/SonyZVE10
media-gopro /volume1/media/GoPro"
pairs_ok=0; pairs_bad=0
while read -r disk root; do
  n=$(grep -c " certify $disk $certs/$disk.cert.json --root $root\$" "$calls" || true)
  if [ "$n" -eq 1 ]; then pairs_ok=$((pairs_ok + 1)); else pairs_bad=$((pairs_bad + 1)); echo "       pair $disk $root: $n certify call(s), want 1"; fi
done <<< "$expected_pairs"
check "six distinct expected (disk, root) pairs, one certify call each" \
  test "$pairs_ok" -eq 6 -a "$pairs_bad" -eq 0
check "no certify call outside those six" test "$(grep -c ' certify ' "$calls")" -eq 6
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
