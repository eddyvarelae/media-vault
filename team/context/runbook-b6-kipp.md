# Runbook: B6 for the seven `kipp-*` disks - verify + certify (draft 2026-09-19 11:46, PM)

Status: **pass A running since 2026-09-19 13:48 (Tester rev 8a). Pass B = `kipp-backup` + `kipp-leantank` on `v0.2.8` (B51 merged `7b0ccc8`, tagged 2026-09-19 14:40), Tester rev 8b, starts only after pass A's `pass A done` line and a successful `v0.2.8` pull.**

## Facts
- After the kipp copy (Tester #41, Reviewer #83): 9,872 `copied` rows on `kipp-sonya6700` (5,978), `kipp-backup` (3,648 + 1 deduped), `kipp-multicam` (34), `kipp-auditorium` (10), `kipp-gopro` (184), `kipp-sonyzve10` (18); `kipp-leantank` has 520 `deduped` rows and nothing copied. 1,921,695,784,449 B to re-hash from the NAS disk (≈ 3-4 h at the copy's 155 MB/s; verify reads only).
- `verify <disk> <root> --only-unverified` re-hashes rows not yet `verified` and promotes them; exit 1 on any mismatch/missing. `certify <disk> <out.cert.json> --root <root>` signs the disk's verified set with `key.pem` from `VAULT_CONFIG`; refuses an output under the root (B25); exits 1 for a disk with no verifiable rows - **`kipp-leantank` (deduped only) may do exactly that: report, do not "fix".**
- `vaultagent`: `sudo -n docker` only; `/volume1/docker/` and `vault-nas-config/` are mode 777, so `cp -p` of the manifest and writing the certs need no sudo. Image `ghcr.io/eddyvarelae/media-vault:v0.2.7`.
- The single-writer rule: nothing else runs `vault` against the manifest during this pass. Lock note by the PM.

## Steps (Tester, `vaultagent`, every command with `date -Iseconds` and its output in `tester-feedback.md`)
0. Pre: `sudo -n docker ps -q | wc -l` = 0; `sha256sum /volume1/docker/vault-nas-config/manifest.db` must be `dd29e115bb007cdd4406dfc24b90b4e6eecf3a894c58f25fe7d989f44c6c2758` (the post-copy state, #41/#83); `ls -la /volume1/docker/vault-certs/`.
1. **Backup**: `cp -p /volume1/docker/vault-nas-config/manifest.db /volume1/docker/vault-nas-config/manifest.db.bak-b6kipp-$(date +%Y%m%d-%H%M%S)`; `sha256sum` both.
2. **Run pass A**, detached (five disks; exit codes logged explicitly; `ssh -n` + `</dev/null` so the call returns):
   ```
   ssh -n vaultagent@192.168.1.167 'cd /volume1/docker && nohup bash -c '"'"'
   IMG=ghcr.io/eddyvarelae/media-vault:v0.2.7; LOG=/volume1/docker/verify-certify.log; CERTS=/volume1/docker/vault-certs
   for e in kipp-sonya6700:SonyA6700 kipp-multicam:Multicam kipp-auditorium:Auditorium kipp-gopro:GoPro kipp-sonyzve10:SonyZVE10; do
     d=${e%%:*}; r=/volume1/media/${e#*:}
     echo "[$(date)] === VERIFY $d at $r ===" >> $LOG
     sudo -n docker run --rm -v /volume1:/volume1 -e VAULT_CONFIG=/volume1/docker/vault-nas-config $IMG verify $d $r --only-unverified >> $LOG 2>&1; rc=$?; echo "[$(date)] verify $d exit=$rc" >> $LOG
     [ $rc -eq 0 ] || { echo "[$(date)] VERIFY FAILED for $d - skipping certify" >> $LOG; continue; }
     echo "[$(date)] === CERTIFY $d ===" >> $LOG
     sudo -n docker run --rm -v /volume1:/volume1 -e VAULT_CONFIG=/volume1/docker/vault-nas-config $IMG certify $d $CERTS/$d.cert.json --root $r >> $LOG 2>&1; rc=$?; echo "[$(date)] certify $d exit=$rc" >> $LOG
     echo "[$(date)] $d done" >> $LOG
   done; echo "[$(date)] kipp verify+certify pass A done" >> $LOG'"'"' >/dev/null 2>&1 </dev/null &'
   ```
   Note the log byte offset before starting. **Pass B** (`kipp-backup:Backup`, `kipp-leantank:LeanTank`) is the same loop with those two entries and image `v0.2.8`, after B51 merges - not before: on `v0.2.7`, `kipp-backup`'s one `deduped` row (`PRIVATE/M4ROOT/STATUS.BIN`, owner `media-leantank`) resolves under the wrong root → `Missing: 1`, exit 1, no cert; and `kipp-leantank`'s 520 `deduped` rows would be rewritten to `verified`, losing the by-reference provenance (Tester #42 A/B).
3. **Witness** every ~30 min: containers, log size, the last `=== VERIFY|CERTIFY … ===` / `done` lines, any `FAILED|Mismatch: [1-9]|Missing: [1-9]|error` line verbatim. Do not intervene.
4. **Finish (per pass)**: the `Verified: N   Mismatch: 0   Missing: 0   Errors: 0` lines verbatim (expected N = 5978 / 3648 / 34 / 10 / 184 / 18 / and whatever `kipp-leantank` says), the seven certify results, `ls -la vault-certs/` (new files, sizes), manifest sha after, snapshot to `/Volumes/Scratch1/tester/b6-kipp/manifest-after.db`, per-`kipp-*` row counts by status (expected: 9,872 `verified`, 521 `deduped`, 0 `copied`), pre-existing 78,128−10,393 = 67,735 rows unchanged. Rung `witnessed` → Reviewer.

## Rollback
`cp -p <the .bak from step 1> /volume1/docker/vault-nas-config/manifest.db` while nothing runs; delete only the seven new `kipp-*.cert.json` if a re-run is needed - never the `media-*` certs.
