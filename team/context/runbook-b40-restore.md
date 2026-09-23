# Runbook: B40/B41 - restore the torn `SonyA6700/DCIM/DSC04868_2025.JPG` from `Eddy's Media Vault` (2026-09-22 17:12, PM)

Status: **GO from Eddy 2026-09-22 17:12** ("restore the file … through the Mac Mini"). Executor: Tester (`vaultagent` on the NAS; SMB from the Mini for the staging copy) under a PM lock.

## Facts
- Torn file on the NAS: `/volume1/media/SonyA6700/DCIM/DSC04868_2025.JPG`, 7,285,047 B = 3 MiB of JPEG + 4,139,319 zero bytes (Tester #24). Manifest row **id 243518**: `media-sonya6700`, `source_path DCIM/DSC04868_2025.JPG`, `dest_path` empty (a B39-type row), sha `41ba38962efa4d03bb7970f596d473816a6b5d92e7ae220d80ab7c0e63a8e612`, status `verified` (attested by the Sep-21 `media-sonya6700` cert - that cert is superseded after this).
- Intact original: `/Volumes/Eddy's Media Vault/Backups/SonyA6700/DCIM/DSC04868.JPG`, 7,285,047 B, sha **`b7ecf8081e28b3a1c38a02620bec11f45894838063bf1b64b5834ff24f5f3a69`** (gap-emv.tsv, the one GAP file on EMV). Same length as the torn file - a torn write, not a truncation.
- Tool: `vault restore <source-disk> <source-path> <replacement-file> <dest-dir> --expect-sha <sha> [--dry-run]` (B40, `b1277bd`, #45): replaces ONE row's destination file on purpose, containment + identity-only claimants, the row drops to `copied` for `verify` to promote. Never opens the manifest over SMB: the command runs **on the NAS**; the Mini only stages the bytes.

## Steps (Tester; `date -Iseconds` + output per command)
0. Pre: 0 containers; `sha256sum manifest.db` (expect `39b91e93c33e774e3b5f85a358399cd83144dedc656a9f4f208bf5f351a7ecaa`); `sha256sum /volume1/media/SonyA6700/DCIM/DSC04868_2025.JPG` (expect `41ba3896…`, 7,285,047 B) - read-only, over SSH.
1. Backup: `cp -p manifest.db manifest.db.bak-b40-$(date +%Y%m%d-%H%M%S)`; sha both.
2. **Stage through the Mini**: `mkdir -p ~/mounts/docker/restore-staging && cp "/Volumes/Eddy's Media Vault/Backups/SonyA6700/DCIM/DSC04868.JPG" ~/mounts/docker/restore-staging/DSC04868.JPG` (SMB write into `/volume1/docker/`, not a sacred path); `shasum -a 256` on the SSD file and on the staged copy over SSH: both `b7ecf808…`, 7,285,047 B.
3. **Dry-run on the NAS**: `sudo -n docker run --rm -v /volume1:/volume1 -e VAULT_CONFIG=/volume1/docker/vault-nas-config ghcr.io/eddyvarelae/media-vault:v0.2.8 restore media-sonya6700 DCIM/DSC04868_2025.JPG /volume1/docker/restore-staging/DSC04868.JPG /volume1/media/SonyA6700 --expect-sha b7ecf8081e28b3a1c38a02620bec11f45894838063bf1b64b5834ff24f5f3a69 --dry-run` - must name exactly row 243518 and the destination `/volume1/media/SonyA6700/DCIM/DSC04868_2025.JPG`, exit 0; anything else → stop, post.
4. **Live**: same without `--dry-run`. Expected: one file replaced, row 243518 → `copied` with sha `b7ecf808…`; manifest sha changes; `sha256sum` of the NAS file = `b7ecf808…`.
5. **Verify**: `… verify media-sonya6700 /volume1/media/SonyA6700 --only-unverified` → `Verified: 1   Mismatch: 0   Missing: 0   Errors: 0`, exit 0.
6. **Re-certify**: `mkdir -p vault-certs/archive-2026-09-21 && mv vault-certs/media-sonya6700.cert.json vault-certs/archive-2026-09-21/` then `… certify media-sonya6700 /volume1/docker/vault-certs/media-sonya6700.cert.json --root /volume1/media/SonyA6700` → `Files: 39414`, exit 0.
7. Witness: snapshot to `/Volumes/Scratch1/tester/b40/`, rows changed = exactly row 243518 (`sha256`, `status`, `verified_at`, maybe `dest_path`), 0 others; remove nothing (the staging file stays until the PM says).

## Rollback
`cp -p <bak>` while nothing runs; the torn bytes are gone from the tree by design (the row's old sha is in the backup manifest and the archived cert).
