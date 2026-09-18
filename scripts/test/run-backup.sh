#!/bin/bash
# Tests scripts/backup/run-backup.sh (B22) against a REAL manifest built by
# this repo's own `vault copy` / `verify`, a fake /Volumes directory, and a
# `df` stub that puts each fake volume on its own device (the boot disk and
# the scratch volume are excluded by device). The snapshot goes through
# tagging-helper.py, the report through `vault gap`. Nothing outside $work
# is touched; the fake volumes are proven byte-identical afterwards.
# Needs VAULT_BIN (the Go wrapper builds it) and a `go` on PATH for the
# build-into-state case.
set -u

here=$(cd "$(dirname "$0")" && pwd)
script="$here/../backup/run-backup.sh"
vault="${VAULT_BIN:?set VAULT_BIN to a built vault binary}"
work=$(mktemp -d "${TMPDIR:-/tmp}/backup-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
# The script's defaults live under $HOME; none of them may be real. Go's
# caches must stay where they are, or the build case re-downloads modules
# into the temp dir (and cannot remove the read-only module cache after).
export GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE 2>/dev/null || true)}" GOCACHE="${GOCACHE:-$(go env GOCACHE 2>/dev/null || true)}"
export HOME="$work/home"; mkdir -p "$HOME"

failures=0
check() { local desc=$1; shift; if "$@"; then echo "  ok   $desc"; else echo "  FAIL $desc"; failures=$((failures + 1)); fi; }
count() { grep -cF -- "$1" "$2" || true; }

# ── the machine ───────────────────────────────────────────────────────────
vols="$work/Volumes"; scratch="$vols/Scratch1"; state="$work/state"; logf="$work/log/media-backup.log"; cfg="$work/cfg"
mkdir -p "$work/bin" "$vols" "$scratch" "$state" "$work/log" "$cfg"
cat > "$work/bin/df" <<STUB
#!/bin/bash
# df -P <path>: every fake volume is its own device, named after it; the rest is the boot disk.
p="\${2:-\$1}"
echo "Filesystem 512-blocks Used Available Capacity Mounted on"
case "\$p" in
  $vols/*) v="\${p#$vols/}"; v="\${v%%/*}"; echo "/dev/vol-\$(echo "\$v" | tr -c 'A-Za-z0-9\\n' _) 1 1 1 1% $vols/\$v" ;;
  *)       echo "/dev/boot 1 1 1 1% /" ;;
esac
STUB
chmod +x "$work/bin/df"
cat > "$work/mini.env" <<ENV
SCRATCH_DIR="$scratch"
ENV

# ── the archive: real rows ───────────────────────────────────────────────
export VAULT_CONFIG="$cfg"
src="$work/src"; media="$work/media"
mk() { mkdir -p "$(dirname "$1")"; printf '%s' "$2" > "$1"; }
mk "$src/DCIM/a.ARW" "archived raw"; mk "$src/DCIM/b.ARW" "archived two"
"$vault" copy media-sonya6700 "$src" "$media/SonyA6700" > /dev/null
"$vault" verify media-sonya6700 "$media/SonyA6700" > /dev/null || { echo "fixture: verify failed"; exit 1; }
manifest="$cfg/manifest.db"

# ── the disks ────────────────────────────────────────────────────────────
mkvol() { mkdir -p "$vols/$1"; }
mkvol tars;  mk "$vols/tars/SonyA6700/DCIM/renamed.ARW" "archived raw"; mk "$vols/tars/SonyA6700/DCIM/new.ARW" "brand new photo"
mkvol case;  mk "$vols/case/Backup/x.bin" "archived two"
mkvol "Eddy's Media Vault"; mk "$vols/Eddy's Media Vault/Backups/y.bin" "not archived either"
mkvol Random;  mk "$vols/Random/z.bin" "unknown disk"
mk "$scratch/tester/junk.bin" "scratch is never a source"
before=$(cd "$vols" && find . -type f -exec shasum -a 256 {} \; | sort)

run() {  # the script with the machine set up; VAULT_BIN is the test's binary unless MANAGED_BIN=1 (then the job builds its own)
  local bin="$vault"; [[ -n "${MANAGED_BIN:-}" ]] && bin=""
  PATH="$work/bin:$PATH" MINI_ENV="$work/mini.env" MANIFEST_DB="${MANIFEST_DB:-$manifest}" BACKUP_STATE_DIR="$state" \
    BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$bin" REPO_DIR="${REPO_DIR:-$here/../..}" \
    bash "$script" "$@"
}
out="$work/out"

# ── 1. dry run: what is due, what is ignored; nothing created ─────────────
run --dry-run > "$out" 2>&1
check "dry run exits 0" test $? -eq 0
check "dry run: known list is the decided default" grep -q "known disks: tars|kipp|case|Eddy's Media Vault   excluded: Scratch1" "$out"
check "dry run: the three known mounted disks are due, in directory order" grep -q "due now: Eddy's Media Vault case tars" "$out"
check "dry run: the unknown volume is named and ignored" grep -q "unknown mounted volumes (ignored): Random" "$out"
check "dry run: Scratch1 is neither due nor unknown" test "$(count 'Scratch1' "$out")" -eq 1
check "dry run: no state, no log, no snapshot" test ! -e "$state/backup-state.tsv" -a ! -e "$logf" -a ! -e "$state/gap-manifest"

# ── 2. precondition: manifest missing, with disks due → exit 2, once an hour ─
MANIFEST_DB="$work/none.db" run > "$out" 2>&1
check "manifest missing: exit 2" test $? -eq 2
check "manifest missing: says so and names the due disks" grep -q "REFUSING TO RUN: manifest not found.*Eddy's Media Vault case tars due" "$logf"
MANIFEST_DB="$work/none.db" run > "$out" 2>&1
rc=$?
n=$(count 'REFUSING TO RUN' "$logf")
check "manifest missing again within the hour: exit 2, not logged twice" test "$rc" -eq 2 -a "$n" -eq 1

# ── 3. a tick: one report per due disk, once ──────────────────────────────
run > "$out" 2>&1
check "tick: exits 0" test $? -eq 0
check "tick: nothing on stdout (launchd log gets the file)" test ! -s "$out"
check "tick: manifest snapshot logged as 'as of'" grep -q "manifest snapshot: 2 rows, newest copied_at" "$logf"
check "tick: tars needs archiving, with the arithmetic" \
  grep -q "GAP tars needs archiving: yes, 1 files, 15 bytes (of 2 files / 27 bytes on the disk; 1 files / 12 bytes archived by content; 1 files / 12 bytes hashed to prove it; check 1+1=2)" "$logf"
check "tick: case needs nothing (hashed to prove it)" \
  grep -q "GAP case needs archiving: no, 0 files, 0 bytes (of 1 files / 12 bytes on the disk; 1 files / 12 bytes archived by content; 1 files / 12 bytes hashed to prove it; check 1+0=1)" "$logf"
check "tick: the disk with a space in its name is reported" grep -q "GAP Eddy's Media Vault needs archiving: yes, 1 files, 19 bytes" "$logf"
check "tick: report and tsv files written per disk" ls "$state"/gap-tars-*.txt "$state"/gap-tars-*.tsv "$state"/gap-Eddy_s_Media_Vault-*.tsv
check "tick: the tsv lists the absent file" grep -q "SonyA6700/DCIM/new.ARW	15	" "$state"/gap-tars-*.tsv
check "tick: three state lines" test "$(wc -l < "$state/backup-state.tsv" | tr -d ' ')" -eq 3
check "tick: the unknown volume logged once" test "$(count "volume 'Random' is not in BACKUP_DISKS" "$logf")" -eq 1
check "tick: lock released" test ! -e "$state/backup.lock"
check "tick: the fake volumes are byte-identical (nothing copied, staged or written)" test "$(cd "$vols" && find . -type f -exec shasum -a 256 {} \; | sort)" = "$before"
check "tick: the snapshot dir under state is where vault read from" test -f "$state/gap-manifest/manifest.db"

# ── 4. the next tick: nothing due, silent ────────────────────────────────
lines=$(wc -l < "$logf")
run > "$out" 2>&1
rc=$?
after=$(wc -l < "$logf")
check "second tick: exit 0, nothing logged, nothing on stdout" test "$rc" -eq 0 -a "$after" -eq "$lines" -a ! -s "$out"
run --dry-run > "$out" 2>&1
check "second tick dry run: due now: (none)" grep -q "due now: (none)" "$out"

# ── 5. --force, a re-attach, and a new day ───────────────────────────────
run --force > "$out" 2>&1
check "--force: reports again" test "$(count 'GAP tars needs archiving' "$logf")" -eq 2
rm -rf "$vols/case"; mkvol case; mk "$vols/case/Backup/x.bin" "archived two"     # a new mount point: new attach identity
run > "$out" 2>&1
check "re-attach: only the re-attached disk is reported" test "$(count 'GAP case needs archiving' "$logf")" -eq 3 -a "$(count 'GAP tars needs archiving' "$logf")" -eq 2
python3 - "$state/backup-state.tsv" <<'PY'
import sys
p = sys.argv[1]; lines = open(p).read().splitlines()
open(p, "w").write("\n".join(l.rsplit("\t", 1)[0] + "\t2020-01-01" for l in lines) + "\n")   # every report is from another day
PY
run > "$out" 2>&1
check "new day: every attached known disk is reported again" test "$(count 'GAP tars needs archiving' "$logf")" -eq 3

# ── 6. policy override from mini.env: applied and logged ─────────────────
cat > "$work/mini2.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="tars|Random"
ENV
MINI_ENV="$work/mini2.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --dry-run > "$out" 2>&1
check "override: Random is now known (tars already reported today) and case/EMV are unknown" test "$(count 'due now: Random' "$out")" -eq 1 -a "$(count "unknown mounted volumes (ignored): Eddy's Media Vault case" "$out")" -eq 1
check "override: logged against the default with its source" grep -q "POLICY OVERRIDE: BACKUP_DISKS=\[tars|Random\] (default \[tars|kipp|case|Eddy's Media Vault\], from $work/mini2.env)" "$out"

# ── 7. the binary: built into the state dir from the checkout when stale ─
if command -v go > /dev/null; then
  MANAGED_BIN=1 run --force > "$out" 2>&1
  rc=$?
  check "build: exits 0 with the binary built into the state dir" test "$rc" -eq 0 -a -x "$state/bin/vault"
  check "build: stamped with the checkout's HEAD and logged" test "$(cat "$state/bin/vault.commit")" = "$(git -C "$here/../.." rev-parse HEAD)" -a "$(count 'built vault at' "$logf")" -eq 1
  MANAGED_BIN=1 run --force > "$out" 2>&1
  check "build: not rebuilt when the stamp matches HEAD" test "$(count 'built vault at' "$logf")" -eq 1
  check "build: a binary handed in from outside is never rebuilt" test "$(count "built vault at $vault" "$logf")" -eq 0
else
  echo "  skip build case: go not on PATH"
fi

# ── 8. a torn manifest: refused, nothing reported ────────────────────────
head -c 3000 /dev/urandom > "$work/torn.db"
MANIFEST_DB="$work/torn.db" run --force > "$out" 2>&1
rc=$?
check "torn manifest: exit 2 and says the snapshot failed" test "$rc" -eq 2 -a "$(count 'manifest snapshot failed' "$logf")" -ge 1
check "torn manifest: lock released" test ! -e "$state/backup.lock"

echo
if [ "$failures" -ne 0 ]; then echo "$failures check(s) failed"; exit 1; fi
echo "all checks passed"
