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
trap 'chmod -R u+w "$work" 2>/dev/null; rm -rf "$work"' EXIT
# The script's defaults live under $HOME; none of them may be real. The
# managed build confines its caches under STATE_DIR (review #28-5), so the
# harness does NOT preserve external GOCACHE/GOMODCACHE - it verifies the
# boundary instead (see the build case). The trap chmods before rm because
# Go writes the module cache read-only.
export HOME="$work/home" PYTHONDONTWRITEBYTECODE=1; mkdir -p "$HOME"
export BACKUP_TEST_MODE=1   # the script's BACKUP_SLUG_HOOK / BACKUP_FAIL_AT seams are inert without this (review #56)

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
check "tick: report and tsv files written per disk" ls "$state"/gap-74617273-*.txt "$state"/gap-74617273-*.tsv "$state"/gap-456464792773204d65646961205661756c74-*.tsv
check "tick: the tsv lists the absent file" grep -q "SonyA6700/DCIM/new.ARW	15	" "$state"/gap-74617273-*.tsv
check "tick: line 1 of the report is the full disk name (slug-collision guard, review #43)" test "$(head -1 "$state"/gap-74617273-*.txt)" = "disk: tars"
check "tick: line 1 of the tsv is the full disk name" test "$(head -1 "$state"/gap-74617273-*.tsv)" = "disk: tars"
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
  check "build: caches landed under STATE_DIR (write boundary)" test -d "$state/go/mod" -a -d "$state/go/cache"
  check "build: Go wrote nothing under \$HOME (telemetry, config, GOPATH) — review #38" test ! -e "$HOME/Library/Application Support/go" -a ! -e "$HOME/go" -a ! -e "$HOME/.config/go"
else
  echo "  skip build case: go not on PATH"
fi

# ── 8. a torn manifest: refused, nothing reported ────────────────────────
head -c 3000 /dev/urandom > "$work/torn.db"
MANIFEST_DB="$work/torn.db" run --force > "$out" 2>&1
rc=$?
before=$(count 'manifest snapshot failed' "$logf")
check "torn manifest: exit 2 and says the snapshot failed" test "$rc" -eq 2 -a "$before" -ge 1
check "torn manifest: lock released" test ! -e "$state/backup.lock"
# review #28-4: a corrupt manifest recurs every tick; the failure is
# throttled to once an hour, not logged on every five-minute tick.
MANIFEST_DB="$work/torn.db" run --force > "$out" 2>&1
check "torn manifest again within the hour: exit 2, not logged twice" test $? -eq 2 -a "$(count 'manifest snapshot failed' "$logf")" -eq "$before"
# review #38: a live lock holder is refused; a dead or info-less lock is
# NOT taken over - reported STALE, left for a human, the run does not
# proceed.
mkdir -p "$state/backup.lock"; echo "$$ holding" > "$state/backup.lock/info"
run --force > "$out" 2>&1
check "live lock holder: exit 1, not taken over" test $? -eq 1 -a -d "$state/backup.lock" -a "$(cat "$state/backup.lock/info")" = "$$ holding"
rm -rf "$state/backup.lock"; mkdir -p "$state/backup.lock"; echo "999999 dead" > "$state/backup.lock/info"; touch -t "$(date -v-2H +%Y%m%d%H%M)" "$state/backup.lock"
: > "$logf"
run --force > "$out" 2>&1
check "dead lock: exit 1, STALE LOCK, not taken over" test $? -eq 1 -a -d "$state/backup.lock" -a "$(count 'STALE LOCK' "$logf")" -eq 1
rm -rf "$state/backup.lock"; mkdir -p "$state/backup.lock"
run --force > "$out" 2>&1
check "info-less lock: exit 1, treated as held, not taken over" test $? -eq 1 -a -d "$state/backup.lock" -a ! -e "$state/backup.lock/info"
rm -rf "$state/backup.lock"

# review #41: the slug is single-case (hex), so case-only names never share
# a report file on a case-insensitive $STATE_DIR. "Disk" (4469736b) and
# "disk" (6469736b) hex-differ; a per-name %HH/underscore slug would not.
# They need not coexist under /Volumes: report one, detach, attach the other.
cat > "$work/mini3.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="Disk|disk"
ENV
runslug() { MINI_ENV="$work/mini3.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --force > "$out" 2>&1; }
rm -rf "$vols/disk"; mkdir -p "$vols/Disk"; mk "$vols/Disk/x.mov" "aaa"
runslug
rm -rf "$vols/Disk"; mkdir -p "$vols/disk"; mk "$vols/disk/y.mov" "bbb"   # the distinct disk, attached after
runslug
check "hex slug: 'Disk' report survives 'disk' being reported later" test -f "$state"/gap-4469736b-*.tsv
check "hex slug: 'disk' gets its own distinct report file" test -f "$state"/gap-6469736b-*.tsv
check "hex slug: the two reports are different files" test "$(ls "$state"/gap-4469736b-*.tsv "$state"/gap-6469736b-*.tsv 2>/dev/null | sort -u | wc -l | tr -d ' ')" -eq 2
rm -rf "$vols/disk"

# review #43: the slug is bounded - hex of the first 24 bytes then 16 hex of
# the whole name's sha256. A long (but legal, <=255-byte) volume name still
# lands a report; the unbounded per-byte hex overran the 255-byte filename
# limit so the report - the whole point of the run - never landed. Two names
# sharing their first 24 bytes are still distinct, told apart by the hash of
# the whole name (the shared head alone cannot).
head24="AAAAAAAAAAAAAAAAAAAAAAAA"                       # 24 bytes -> 48 hex '41'
pfx=$(printf '%s' "$head24" | od -An -v -tx1 | tr -d ' \n')
longA="$head24$(printf 'a%.0s' $(seq 176))"             # 200-byte names, shared head
longB="$head24$(printf 'b%.0s' $(seq 176))"
cat > "$work/mini4.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="$longA|$longB"
ENV
runbound() { MINI_ENV="$work/mini4.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --force > "$out" 2>&1; }
rm -rf "$vols"/*; mkdir -p "$vols/$longA"; mk "$vols/$longA/x.mov" "aaa"
runbound
rm -rf "$vols/$longA"; mkdir -p "$vols/$longB"; mk "$vols/$longB/y.mov" "bbb"   # distinct disk, attached after
runbound
check "bounded slug: a 200-byte name still lands a report (unbounded hex would overrun the filename)" test -n "$(ls "$state"/gap-"$pfx"-*.tsv 2>/dev/null)"
check "bounded slug: names sharing a 24-byte head get distinct files" test "$(ls "$state"/gap-"$pfx"-*.tsv 2>/dev/null | sort -u | wc -l | tr -d ' ')" -eq 2
check "bounded slug: each report filename stays well under the 255-byte limit" test "$(ls "$state"/gap-"$pfx"-*.tsv 2>/dev/null | head -1 | xargs -n1 basename | wc -c | tr -d ' ')" -lt 120
rm -rf "$vols"/*

# review #52: slugs are ASSIGNED and PERSISTED in slugs.tsv, so two names can
# never share a slug however their bases hash. A BACKUP_SLUG_HOOK forces Alpha
# and Beta to one base; assign_slug gives Alpha the base and Beta base-2.
cat > "$work/slughook" <<'HOOK'
#!/bin/bash
case "$1" in
  Alpha|Beta) printf 'aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44-0011223344556677' ;;
  *) printf '%s-%s' "$(printf '%s' "$1" | head -c 24 | od -An -v -tx1 | tr -d ' \n')" "$(printf '%s' "$1" | shasum -a 256 | cut -c1-16)" ;;
esac
HOOK
chmod +x "$work/slughook"
BASE='aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44-0011223344556677'
D=$(date +%Y-%m-%d)

# --- both names one base: distinct slugs, both report, both files survive ---
rm -rf "$vols"/*; rm -f "$state"/gap-* "$state"/backup.unknown-* "$state/slugs.tsv"; : > "$state/backup-state.tsv"
mkvol Alpha; mk "$vols/Alpha/DCIM/a" "aaa"; mkvol Beta; mk "$vols/Beta/DCIM/b" "bbb"
cat > "$work/mini-asg.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="Alpha|Beta"
ENV
runasg() { MINI_ENV="$work/mini-asg.env" BACKUP_SLUG_HOOK="$work/slughook" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" "$@" > "$out" 2>&1; }
: > "$logf"; runasg; arc=$?
check "assign: both disks report in one tick (exit 0)" test "$arc" -eq 0
check "assign: Alpha is recorded at the base slug" grep -qF "Alpha	$BASE" "$state/slugs.tsv"
check "assign: Beta is recorded at the disambiguated -2 slug" grep -qF "Beta	$BASE-2" "$state/slugs.tsv"
check "assign: Alpha's report is at the base slug, owned by Alpha" test "$(head -1 "$state/gap-$BASE-$D.txt" 2>/dev/null)" = "disk: Alpha"
check "assign: Beta's report is at the -2 slug, owned by Beta" test "$(head -1 "$state/gap-$BASE-2-$D.txt" 2>/dev/null)" = "disk: Beta"
check "assign: both reports and both TSVs exist" test -f "$state/gap-$BASE-$D.tsv" -a -f "$state/gap-$BASE-2-$D.tsv"
cp "$state/slugs.tsv" "$work/slugs-after"
: > "$logf"; runasg
check "assign: the mapping persists unchanged across ticks" cmp -s "$state/slugs.tsv" "$work/slugs-after"
rm -rf "$vols/Alpha" "$vols/Beta"; mkvol Alpha; mk "$vols/Alpha/DCIM/a" "aaa"; mkvol Beta; mk "$vols/Beta/DCIM/b" "bbb"   # re-attach: new mounts, same names
: > "$logf"; runasg
check "assign: the mapping survives a re-attach unchanged" cmp -s "$state/slugs.tsv" "$work/slugs-after"
rm -rf "$vols"/*; rm -f "$state"/gap-*

# --- a hand-corrupted registry aborts, touching nothing ---
mkvol Alpha; mk "$vols/Alpha/DCIM/a" "aaa"
: > "$state/backup-state.tsv"; cp "$state/backup-state.tsv" "$work/state-base"
printf 'Alpha\tslugone\nAlpha\tslugtwo\n' > "$state/slugs.tsv"   # one name -> two slugs
: > "$logf"; runasg --force; corc=$?
check "corrupt registry (name->2 slugs): aborts exit 1" test "$corc" -eq 1
check "corrupt registry: the diagnostic says corrupt" grep -q "slugs.tsv is corrupt" "$logf"
check "corrupt registry: no report written" test -z "$(ls "$state"/gap-*.txt "$state"/gap-*.tsv 2>/dev/null)"
check "corrupt registry: backup-state.tsv byte-identical" cmp -s "$state/backup-state.tsv" "$work/state-base"
printf 'AAA\tsameslug\nBBB\tsameslug\n' > "$state/slugs.tsv"   # one slug -> two names
: > "$logf"; runasg --force; corc=$?
check "corrupt registry (slug->2 names): aborts exit 1" test "$corc" -eq 1
rm -rf "$vols"/*; rm -f "$state"/gap-* "$state/slugs.tsv"

# --- defense in depth (review #52): a foreign line-1 header at a disk's own
# assigned slug is corruption/tamper - abort, touch nothing. Three isolated
# cases (report-only, TSV-only, marker-only foreign file), each cmp-exact.
runcol() { MINI_ENV="$work/mini-col.env" BACKUP_SLUG_HOOK="$work/slughook" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --force > "$out" 2>&1; }
cat > "$work/mini-col.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="Beta"
ENV
: > "$state/backup-state.tsv"; cp "$state/backup-state.tsv" "$work/state-base"
# report-only
rm -rf "$vols"/*; rm -f "$state"/gap-* "$state"/backup.unknown-* "$state/slugs.tsv"; mkvol Beta; mk "$vols/Beta/DCIM/x" "z"
printf 'disk: Alpha\nforeign report body\n' > "$state/gap-$BASE-$D.txt"; cp "$state/gap-$BASE-$D.txt" "$work/exp"
: > "$logf"; runcol; crc=$?
check "foreign report-only: exit 1" test "$crc" -eq 1
check "foreign report-only: diagnostic names the foreign owner" grep -q "FOREIGN OUTPUT:.*gap-$BASE-$D.txt.*disk: Alpha" "$logf"
check "foreign report-only: foreign bytes byte-exact" cmp -s "$state/gap-$BASE-$D.txt" "$work/exp"
check "foreign report-only: no sibling tsv written" test ! -e "$state/gap-$BASE-$D.tsv"
check "foreign report-only: backup-state.tsv byte-identical" cmp -s "$state/backup-state.tsv" "$work/state-base"
# TSV-only
rm -f "$state"/gap-* "$state/slugs.tsv"
printf 'disk: Alpha\npath\tsize\tsha256\n' > "$state/gap-$BASE-$D.tsv"; cp "$state/gap-$BASE-$D.tsv" "$work/exp"
: > "$logf"; runcol; crc=$?
check "foreign tsv-only: exit 1" test "$crc" -eq 1
check "foreign tsv-only: diagnostic names the tsv" grep -q "FOREIGN OUTPUT:.*gap-$BASE-$D.tsv" "$logf"
check "foreign tsv-only: foreign bytes byte-exact" cmp -s "$state/gap-$BASE-$D.tsv" "$work/exp"
check "foreign tsv-only: no report written" test ! -e "$state/gap-$BASE-$D.txt"
check "foreign tsv-only: backup-state.tsv byte-identical" cmp -s "$state/backup-state.tsv" "$work/state-base"
# marker-only (Beta unknown)
rm -rf "$vols"/*; rm -f "$state"/gap-* "$state"/backup.unknown-* "$state/slugs.tsv"; mkvol Beta; mk "$vols/Beta/DCIM/x" "z"
cat > "$work/mini-colm.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="tars"
ENV
printf 'volume: Alpha\n' > "$state/backup.unknown-$BASE"; cp "$state/backup.unknown-$BASE" "$work/exp"
: > "$logf"; MINI_ENV="$work/mini-colm.env" BACKUP_SLUG_HOOK="$work/slughook" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" > "$out" 2>&1; crc=$?
check "foreign marker-only: exit 1" test "$crc" -eq 1
check "foreign marker-only: diagnostic names the marker" grep -q "FOREIGN OUTPUT: unknown-volume marker.*backup.unknown-$BASE.*volume: Alpha" "$logf"
check "foreign marker-only: foreign bytes byte-exact" cmp -s "$state/backup.unknown-$BASE" "$work/exp"
check "foreign marker-only: backup-state.tsv byte-identical" cmp -s "$state/backup-state.tsv" "$work/state-base"
rm -rf "$vols"/*; rm -f "$state"/gap-* "$state"/backup.unknown-* "$state/slugs.tsv"

# --- clean-tick pruning (review #50/#51/#52): live-owner retained, stale pruned ---
mkvol Random; mk "$vols/Random/z" "u"
cat > "$work/mini-clean.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="tars"
ENV
runclean() { MINI_ENV="$work/mini-clean.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" > "$out" 2>&1; }
printf 'volume: Ghost\n' > "$state/backup.unknown-ghost0slug0literal"   # a stale marker, owner not mounted
: > "$logf"; runclean; clrc=$?
rmk=$(ls "$state"/backup.unknown-* 2>/dev/null | grep -v ghost0slug0literal | head -1)
check "clean tick: exits 0" test "$clrc" -eq 0
check "clean tick: a live unknown volume's marker is created, owner on line 1" test "$(head -1 "$rmk" 2>/dev/null)" = "volume: Random"
check "clean tick: the stale-owner marker is pruned" test ! -e "$state/backup.unknown-ghost0slug0literal"
: > "$logf"; runclean
check "clean tick: the live-owner marker is retained on the next clean tick" test -e "$rmk" -a "$(head -1 "$rmk" 2>/dev/null)" = "volume: Random"
rm -rf "$vols"/*; rm -f "$state"/backup.unknown-* "$state/slugs.tsv"

# review #53: the lock is held before the registry is read, on unknown-only ticks
# too. A live lock makes even a nothing-due, unknown-only tick refuse, writing no
# marker and no registry.
rm -rf "$vols"/*; rm -f "$state"/backup.unknown-* "$state/slugs.tsv"; mkvol Random; mk "$vols/Random/z" "u"
cat > "$work/mini-lk.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="tars"
ENV
mkdir -p "$state/backup.lock"; echo "$$ started test" > "$state/backup.lock/info"   # a live lock (this test's pid)
: > "$logf"; MINI_ENV="$work/mini-lk.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" > "$out" 2>&1; lkrc=$?
check "lock first: an unknown-only tick refuses when the lock is held" test "$lkrc" -ne 0
check "lock first: no marker written while the lock was held" test -z "$(ls "$state"/backup.unknown-* 2>/dev/null)"
check "lock first: no registry written while the lock was held" test ! -e "$state/slugs.tsv"
rm -rf "$state/backup.lock"; rm -rf "$vols"/*

# review #53: assign_slug installs the full registry + exactly one row; a row
# appearing between the count and the write (a hook appends during base
# computation) trips the line-count guard, so no row is lost and the tick aborts.
rm -f "$state"/gap-* "$state/slugs.tsv" "$state/slugs.tsv.tmp"; : > "$state/backup-state.tsv"
printf 'Existing\texistingslug\n' > "$state/slugs.tsv"
cat > "$work/slughook-grow" <<HOOK
#!/bin/bash
[ "\$1" = Grow ] && printf 'Sneaky\tsneakyslug\n' >> "$state/slugs.tsv"
printf growbase
HOOK
chmod +x "$work/slughook-grow"
mkvol Grow; mk "$vols/Grow/DCIM/g" "g"
cat > "$work/mini-grow.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="Grow"
ENV
: > "$logf"; MINI_ENV="$work/mini-grow.env" BACKUP_SLUG_HOOK="$work/slughook-grow" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --force > "$out" 2>&1; grc=$?
check "registry-preserve: the tick aborts (exit non-zero)" test "$grc" -ne 0
check "registry-preserve: the pre-existing row survives" grep -q Existing "$state/slugs.tsv"
check "registry-preserve: the concurrently-added row survives" grep -q Sneaky "$state/slugs.tsv"
check "registry-preserve: Grow was not recorded (write refused)" test "$(nm=Grow awk -F'\t' 'BEGIN{n=ENVIRON["nm"]} $1==n{c++} END{print c+0}' "$state/slugs.tsv")" -eq 0
rm -rf "$vols"/*; rm -f "$state"/gap-* "$state/slugs.tsv" "$state/slugs.tsv.tmp"

# review #53: fail-closed on the write path - an un-creatable temp aborts, and the
# registry is byte-identical.
printf 'Keep\tkeepslug\n' > "$state/slugs.tsv"; cp "$state/slugs.tsv" "$work/reg-base"
rm -rf "$state/slugs.tsv.tmp"; mkdir "$state/slugs.tsv.tmp"   # a directory where the temp must be written
mkvol WriteFail; mk "$vols/WriteFail/DCIM/x" "z"
cat > "$work/mini-wf.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="WriteFail"
ENV
: > "$logf"; MINI_ENV="$work/mini-wf.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --force > "$out" 2>&1; wrc=$?
check "unwritable temp: the tick aborts (exit non-zero)" test "$wrc" -ne 0
check "unwritable temp: diagnostic mentions the slug assignment" grep -q "could not assign a slug" "$logf"
check "unwritable temp: no report written" test -z "$(ls "$state"/gap-*.txt 2>/dev/null)"
check "unwritable temp: the registry is byte-identical" cmp -s "$state/slugs.tsv" "$work/reg-base"
rm -rf "$state/slugs.tsv.tmp"; rm -rf "$vols"/*; rm -f "$state"/gap-* "$state/slugs.tsv"

# review #53: fail-closed on the read path - an unreadable registry aborts, and it
# is byte-identical.
printf 'Keep\tkeepslug\n' > "$state/slugs.tsv"; cp "$state/slugs.tsv" "$work/reg-base"; chmod 000 "$state/slugs.tsv"
mkvol ReadFail; mk "$vols/ReadFail/DCIM/x" "z"
cat > "$work/mini-rf.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="ReadFail"
ENV
: > "$logf"; MINI_ENV="$work/mini-rf.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --force > "$out" 2>&1; rrc=$?
chmod 644 "$state/slugs.tsv"
check "unreadable registry: the tick aborts (exit non-zero)" test "$rrc" -ne 0
check "unreadable registry: the registry is byte-identical" cmp -s "$state/slugs.tsv" "$work/reg-base"
rm -rf "$vols"/*; rm -f "$state/slugs.tsv"

# review #53: names reach awk via ENVIRON, not `awk -v` (which un-escapes), so a
# volume literally named a\tb (backslash-t, not a tab) records as ONE row and is
# found again idempotently rather than duplicated.
weird='a\tb'
rm -f "$state"/gap-* "$state/slugs.tsv" "$state/slugs.tsv.tmp"; : > "$state/backup-state.tsv"
mkdir -p "$vols/$weird/DCIM"; mk "$vols/$weird/DCIM/x" "z"
cat > "$work/mini-bs.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="$weird"
ENV
runbs() { MINI_ENV="$work/mini-bs.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --force > "$out" 2>&1; }
: > "$logf"; runbs; runbs
check "backslash-t name: exactly one registry row (no awk -v un-escaping)" test "$(nm="$weird" awk -F'\t' 'BEGIN{n=ENVIRON["nm"]} $1==n{c++} END{print c+0}' "$state/slugs.tsv")" -eq 1
check "backslash-t name: the row's name field is the literal name" test "$(awk -F'\t' 'NR==1{print $1}' "$state/slugs.tsv")" = "$weird"
rm -rf "$vols"/*; rm -f "$state/slugs.tsv"

# review #54/#55: the volume name is read with ${mp##*/} (not $(basename), whose
# command substitution strips a trailing newline). A name with a trailing tab or
# newline, a REAL interior tab, or an interior newline is refused at discovery
# (exit 2) and writes no report, marker or registry.
cat > "$work/mini-nm.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="tars"
ENV
runnm() { MINI_ENV="$work/mini-nm.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" > "$out" 2>&1; }
for badname in $'trail\n' $'a\tb' $'a\nb'; do
  case "$badname" in *$'\n'*) kind="newline";; *) kind="tab";; esac
  rm -rf "$vols"/*; rm -f "$state/slugs.tsv" "$state"/gap-*.txt "$state"/gap-*.tsv "$state"/backup.unknown-*; mkdir -p "$vols/$badname/DCIM"; mk "$vols/$badname/DCIM/x" "z"
  : > "$logf"; runnm; nmrc=$?
  check "bad name ($kind): refused at discovery, exit 2" test "$nmrc" -eq 2
  check "bad name ($kind): the diagnostic says tab or newline" grep -q "tab or newline" "$out"
  check "bad name ($kind): no report, marker or registry" test -z "$(ls "$state"/gap-*.txt "$state"/gap-*.tsv "$state"/backup.unknown-* "$state/slugs.tsv" 2>/dev/null)"
done
rm -rf "$vols"/*; rm -f "$state/slugs.tsv"

# review #54/#55/#56: the base computation is checked component by component - an
# empty base, a failing hook, or a broken shasum aborts fail-closed against a
# SEEDED registry, which stays byte-identical, and writes no report (either
# extension) or marker.
rm -f "$state"/gap-*.txt "$state"/gap-*.tsv "$state"/backup.unknown-*; mkvol Base; mk "$vols/Base/DCIM/x" "z"; : > "$state/backup-state.tsv"
printf 'Keep\tkeepslug\n' > "$state/slugs.tsv"; cp "$state/slugs.tsv" "$work/reg-base"
cat > "$work/mini-b.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="Base"
ENV
nowritten() { test -z "$(ls "$state"/gap-*.txt "$state"/gap-*.tsv "$state"/backup.unknown-* 2>/dev/null)"; }
# Each base-failure hook records its own invocation to $srec (review #57), so a
# fixture proves the intended failure mechanism actually fired - not that the
# base failed for some unrelated reason. runb clears $srec + $logf before each.
srec="$work/seam-record"
runb() { rm -f "$state"/gap-*.txt "$state"/gap-*.tsv "$state"/backup.unknown-*; : > "$logf" "$srec"; MINI_ENV="$work/mini-b.env" PATH="$1" BACKUP_SLUG_HOOK="$2" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --force > "$out" 2>&1; }
cat > "$work/hook-empty" <<HOOK
#!/bin/bash
echo "SEAM base:empty reached" >> "$srec"
HOOK
cat > "$work/hook-fail" <<HOOK
#!/bin/bash
echo "SEAM base:exit3 reached" >> "$srec"
exit 3
HOOK
mkdir -p "$work/binfail"
cat > "$work/binfail/shasum" <<HOOK
#!/bin/bash
echo "SEAM base:shasum reached" >> "$srec"
exit 1
HOOK
chmod +x "$work/hook-empty" "$work/hook-fail" "$work/binfail/shasum"
runb "$work/bin:$PATH" "$work/hook-empty"; check "empty base: aborts" test "$?" -ne 0
check "empty base: the empty-base hook was invoked" grep -q "SEAM base:empty reached" "$srec"
check "empty base: registry byte-identical" cmp -s "$state/slugs.tsv" "$work/reg-base"
check "empty base: no report or marker" nowritten
runb "$work/bin:$PATH" "$work/hook-fail"; check "failing hook base: aborts" test "$?" -ne 0
check "failing hook base: the exit-3 hook was invoked" grep -q "SEAM base:exit3 reached" "$srec"
check "failing hook base: registry byte-identical" cmp -s "$state/slugs.tsv" "$work/reg-base"
check "failing hook base: no report or marker" nowritten
runb "$work/binfail:$work/bin:$PATH" ""; check "shasum fails: aborts" test "$?" -ne 0
check "shasum fails: the shadowed shasum was invoked" grep -q "SEAM base:shasum reached" "$srec"
check "shasum fails: registry byte-identical" cmp -s "$state/slugs.tsv" "$work/reg-base"
check "shasum fails: no report or marker" nowritten
rm -rf "$vols"/*; rm -f "$state/slugs.tsv"

# review #55/#56: BACKUP_FAIL_AT injects a failure at a specific assignment op
# AFTER check_slugs has passed; the "SEAM <op> reached" log proves the real op
# was hit. `held` forces the tri-state held-check into its error state. Each op
# aborts with the registry byte-identical and no report/marker.
mkvol OpFail; mk "$vols/OpFail/DCIM/x" "z"; : > "$state/backup-state.tsv"
cat > "$work/mini-op.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="OpFail"
ENV
for op in lookup held copy rename; do
  rm -f "$state/slugs.tsv" "$state/slugs.tsv.tmp" "$state"/gap-*.txt "$state"/gap-*.tsv "$state"/backup.unknown-*
  printf 'Keep\tkeepslug\n' > "$state/slugs.tsv"; cp "$state/slugs.tsv" "$work/reg-base"
  : > "$logf"; BACKUP_FAIL_AT="$op" MINI_ENV="$work/mini-op.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --force > "$out" 2>&1; oprc=$?
  check "fail-at $op: the tick aborts (exit non-zero)" test "$oprc" -ne 0
  check "fail-at $op: SEAM $op reached (the real op)" grep -q "SEAM $op reached" "$logf"
  check "fail-at $op: diagnostic mentions the slug assignment" grep -q "could not assign a slug" "$logf"
  check "fail-at $op: the registry is byte-identical" cmp -s "$state/slugs.tsv" "$work/reg-base"
  check "fail-at $op: no report or marker written" test -z "$(ls "$state"/gap-*.txt "$state"/gap-*.tsv "$state"/backup.unknown-* 2>/dev/null)"
  check "fail-at $op: OpFail was not recorded" test "$(nm=OpFail awk -F'\t' 'BEGIN{n=ENVIRON["nm"]} $1==n{c++} END{print c+0}' "$state/slugs.tsv")" -eq 0
done
rm -rf "$vols"/*; rm -f "$state/slugs.tsv" "$state/slugs.tsv.tmp"

# review #56: the seams are inert without BACKUP_TEST_MODE=1 - a stray
# BACKUP_FAIL_AT in production changes nothing. A normal tick with FAIL_AT=copy
# but the mode unset reports as usual, records the disk, and logs no SEAM line.
rm -f "$state"/gap-*.txt "$state"/gap-*.tsv "$state"/backup.unknown-* "$state/slugs.tsv"; : > "$state/backup-state.tsv"
mkvol Inert; mk "$vols/Inert/DCIM/new.ARW" "brand new"
cat > "$work/mini-in.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="Inert"
ENV
: > "$logf"; BACKUP_TEST_MODE= BACKUP_FAIL_AT=copy MINI_ENV="$work/mini-in.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$state" BACKUP_LOG_FILE="$logf" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" --force > "$out" 2>&1; inrc=$?
check "seam inert: a normal tick with FAIL_AT set but mode off exits 0" test "$inrc" -eq 0
check "seam inert: the report was written normally" test -n "$(ls "$state"/gap-*.txt 2>/dev/null)"
check "seam inert: the disk was recorded in the registry" test "$(nm=Inert awk -F'\t' 'BEGIN{n=ENVIRON["nm"]} $1==n{c++} END{print c+0}' "$state/slugs.tsv")" -eq 1
check "seam inert: no SEAM line logged" test -z "$(grep SEAM "$logf")"
rm -rf "$vols"/*; rm -f "$state/slugs.tsv"


# review #43/#49: a configured disk name longer than 255 bytes can never be a
# mount point; refuse it at discovery, before any report path is built AND
# before any log call. Use a FRESH state dir whose log directory does not
# exist, and a real (non-dry-run) tick so log() would write to that file: the
# refusal must reach stderr with exit 2 and NOT try to log into the missing
# dir (the old die()->log() would print "No such file" and create nothing).
long255="$head24$(printf 'c%.0s' $(seq 300))"   # 324 bytes
fresh="$work/freshstate"; rm -rf "$fresh"
cat > "$work/mini6.env" <<ENV
SCRATCH_DIR="$scratch"
BACKUP_DISKS="$long255"
ENV
MINI_ENV="$work/mini6.env" PATH="$work/bin:$PATH" MANIFEST_DB="$manifest" BACKUP_STATE_DIR="$fresh" BACKUP_LOG_FILE="$fresh/log/media-backup.log" VOLUMES_DIR="$vols" VAULT_BIN="$vault" bash "$script" > "$out" 2>&1; longrc=$?
check "over-long name: refused with exit 2" test "$longrc" -eq 2
check "over-long name: the message says 255 bytes" grep -q "longer than 255 bytes" "$out"
check "over-long name: no broken log write (refused before any log call)" test "$(grep -c 'No such file' "$out")" -eq 0
check "over-long name: no state dir created" test ! -e "$fresh"

echo
if [ "$failures" -ne 0 ]; then echo "$failures check(s) failed"; exit 1; fi
echo "all checks passed"
