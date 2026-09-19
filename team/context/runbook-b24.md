# Runbook: B24 - repair 195 `media-sonya6700` dest_paths on the live manifest (draft 2026-09-17T22:13:58-07:00, PM)

Status: **APPROVED 2026-09-18 18:01** - review #23 accepted; Eddy named the Tester (`figmaboi`) as executor (DECISIONS.md 2026-09-18 18:01). PM lock posted in dev-questions.md.

## Facts
- 195 `copied` rows have `dest_path` without `CLIP/` (10) or `DCIM/` (185); the bytes are on the NAS one directory down and hash-match the rows (Tester #7, 195/195; re-confirmed by the dry-run comparison, Tester #26).
- Tool: `vault repair-dest <disk> <dest-root> [--dry-run]` on `main` `88d75d7` (Reviewer #15); published as `v0.2.2` and `v0.2.3` (use `v0.2.3`). It rewrites `dest_path` only, one row at a time, after a size + sha256 match; exits 1 if any row stays unresolved.
- Dry-run on a snapshot (Tester #26): `195 REPAIR`, 0 other outcomes, every path equal to the independently hash-located file.
- The manifest is single-writer; the live run must be the only `vault` process. Nothing is running on the NAS (Tester #16, #25).

## Steps (on the NAS over SSH as `figmaboi`; every command logged in `tester-feedback.md` with `date -Iseconds`)
1. **Lock note** by the PM in `dev-questions.md` (deploy = any vault command against the live manifest).
2. **Backup the manifest** (root-owned file): `sudo cp -p /volume1/docker/vault-nas-config/manifest.db /volume1/docker/vault-nas-config/manifest.db.bak-b24-$(date +%Y%m%d-%H%M%S)` and `sha256sum` both.
3. **Dry-run on the live manifest** (confirms the image and the mounts; writes no row):
   `sudo docker run --rm -v /volume1:/volume1 -e VAULT_CONFIG=/volume1/docker/vault-nas-config ghcr.io/eddyvarelae/media-vault:v0.2.6 repair-dest media-sonya6700 /volume1/media/SonyA6700 --dry-run`
   Must print exactly `Repairable: 195   Not found: 0   Ambiguous: 0   Owned: 0   Not a file: 0   Unsafe: 0   Conflict: 0`. Anything else → stop, post, no step 4.
4. **Live run**: same command without `--dry-run`. Expected: 195 `repaired` lines, `Repaired 195 row(s)`, exit 0.
5. **Verify**: `… ghcr.io/eddyvarelae/media-vault:v0.2.6 verify media-sonya6700 /volume1/media/SonyA6700 --only-unverified` → `Verified: 195 … Missing: 0`, exit 0 (this is the first B6 step; ~1.15 GB hashed, minutes).
6. **Witness**: Tester snapshots the manifest again, confirms 195 rows now `verified` with `CLIP/`/`DCIM/` prefixes and 0 `copied` rows on `media-sonya6700`; PM releases the lock.

## Rollback
`sudo cp -p <the .bak from step 2> /volume1/docker/vault-nas-config/manifest.db` (only while no vault process runs).
