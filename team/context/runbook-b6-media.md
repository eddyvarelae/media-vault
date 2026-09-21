# Runbook: B6 full sweep of the six `media-*` disks - verify (full) + re-certify (draft 2026-09-21 00:28, PM)

Status: **drafted; waits on Eddy's go.** Executor: Tester as `vaultagent` under a PM lock. ~8.2 TiB to re-hash from the pool at ~190 MB/s ≈ 12-13 h, unattended. Writes: manifest `verified_at` (and `status` for anything that fails), six new certificates, one manifest backup, log appends. Nothing under `/volume1/media` is written.

## Why
The archive's rows were last verified 2026-09-13 (single newest row) and certified 2026-09-02. A full sweep turns that into "verified today" and catches any bit-rot or torn write since (B40 was found by a tail scan, not by verify - the sweep is the real check).

## Steps (Tester, `vaultagent`, `date -Iseconds` + output per command)
0. Pre: no `vault` container running; `sha256sum manifest.db` recorded; `ls -la vault-certs/`.
1. Backup: `cp -p manifest.db manifest.db.bak-b6media-$(date +%Y%m%d-%H%M%S)`; sha both.
2. Preserve the Sep-2 certificates: `mkdir -p /volume1/docker/vault-certs/archive-2026-09-02 && mv /volume1/docker/vault-certs/media-*.cert.json /volume1/docker/vault-certs/archive-2026-09-02/` (five files; list them before and after).
3. Run, detached, same loop shape as `runbook-b6-kipp.md` step 2 but **without `--only-unverified`** (full sweep) and with the six entries `media-djiflip:DJIFlip media-djimini2:DJIMini2 media-iphone:iPhone media-sonya6700:SonyA6700 media-sonyzve10:SonyZVE10 media-gopro:GoPro`, image `v0.2.8`, log offset noted. (`media-backup` and `media-leantank` have certs but are not in the script's disk list - PM to confirm from the manifest whether they carry rows; if they do, add them.)
4. Witness every ~60 min (long job): container, log size, last `=== …`/`done`/`exit=` lines, any `Mismatch: [1-9]|Missing: [1-9]|FAILED|error` verbatim. A non-zero `Mismatch`/`Missing` is a P0: post immediately, do not intervene, the loop continues to the next disk by design.
5. Finish: six `Verified:` lines, six certify results, cert file list, manifest sha after, snapshot to `/Volumes/Scratch1/tester/b6-media/`, rows changed = only `verified_at` on verified rows (0 status changes expected), kipp rows untouched. Rung `witnessed` → Reviewer.

## Rollback
Manifest: `cp -p <bak>` while nothing runs. Certs: move the archived five back.
