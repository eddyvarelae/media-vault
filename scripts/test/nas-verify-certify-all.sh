#!/bin/bash
# Tests that scripts/nas-verify-certify-all.sh writes every certificate under
# $CERTS (beside the manifest), never under the camera root it certifies -
# `vault certify` refuses the latter since B25, so a script that still did it
# would fail every disk. Docker is shadowed by a stub on PATH; VAULT_LOG and
# VAULT_CERTS point at a temp dir, so /volume1 is never opened.
set -u

here=$(cd "$(dirname "$0")" && pwd)
script="$here/../nas-verify-certify-all.sh"
work=$(mktemp -d "${TMPDIR:-/tmp}/verify-certify-test.XXXXXX")
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

check "LOG default is still the production path" \
  grep -qF 'LOG="${VAULT_LOG:-/volume1/docker/verify-certify.log}"' "$script"
check "CERTS default is beside the manifest" \
  grep -qF 'CERTS="${VAULT_CERTS:-/volume1/docker/vault-certs}"' "$script"

calls="$work/calls"; log="$work/log"; certs="$work/certs"
: > "$calls"
cat > "$work/bin/docker" <<STUB
#!/bin/bash
echo "\$*" >> "$calls"
exit 0
STUB
chmod +x "$work/bin/docker"
PATH="$work/bin:$PATH" VAULT_LOG="$log" VAULT_CERTS="$certs" bash "$script" > "$work/stdout" 2>&1
check "script exits 0 when every docker call succeeds" test $? -eq 0
check "certs dir was created" test -d "$certs"
check "six certify calls" test "$(grep -c ' certify ' "$calls")" -eq 6
check "every certify output is under CERTS" \
  test "$(grep ' certify ' "$calls" | grep -c " $certs/media-[a-z0-9]*\.cert\.json --root /volume1/media/")" -eq 6
# Negations and pipelines are computed first, then asserted: `check ! a | b`
# would hand `!` to check as a command and run check inside a subshell,
# where its failure count is lost (review #12).
outs=$(grep ' certify ' "$calls" | sed -E 's/.* certify [^ ]+ ([^ ]+) --root .*/\1/')
under_media=$(printf '%s\n' "$outs" | grep -c '^/volume1/media/' || true)
check "six certify output paths extracted" test "$(printf '%s\n' "$outs" | grep -c .)" -eq 6
check "no certify output under a camera root" test "$under_media" -eq 0
check "certify for media-djiflip names its cert and its root" \
  grep -q " certify media-djiflip $certs/media-djiflip.cert.json --root /volume1/media/DJIFlip\$" "$calls"
check "log reports the cert location under CERTS" \
  grep -q "media-djiflip done — cert at $certs/media-djiflip.cert.json" "$log"
check "verify still runs against the camera roots" \
  grep -q ' verify media-djiflip /volume1/media/DJIFlip$' "$calls"

echo
if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed"
  exit 1
fi
echo "all checks passed"
