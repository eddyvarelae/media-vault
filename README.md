# Media Vault

Auditable media archive for your NAS. Mirror your video shoots from an SSD
(or any source disk) to long-term NAS storage with per-file sha256
verification and a tamper-evident **wipe certificate** that proves your
footage made it across before you reformat the source.

> **Status: v0.1** — engine + CLI only. Web UI, Docker container polish, and
> NAS app integration are coming.

## Why not just rsync

`rsync` is great. But when you're about to wipe a 4 TB SSD full of footage,
"rsync exited 0" is not the same as "every byte of every file is on the NAS,
and I can prove it." Media Vault keeps a per-file sha256 manifest and (soon)
generates a signed certificate attesting that a given source disk is fully
present at the destination — so you can wipe with confidence.

Companion to [media-transfer](https://github.com/eddyvarelae/media-transfer)
(SD card → SSD). Media Vault is the next step (SSD → NAS).

## Roadmap

- [x] Scan: walk source, diff against manifest, plan copy/skip/recopy
- [x] Copy: stream copy with in-line sha256, write to manifest, atomic rename
- [x] Verify: re-hash destination files, mark verified / mismatch
- [x] Wipe certificate: Ed25519-signed JSON, refuses to sign on mismatch
- [ ] HTML rendering of the certificate
- [ ] Web UI (mobile-first, PWA-installable)
- [ ] Single Docker container running on the NAS itself (`docker run` quickstart)
- [ ] UGOS / Synology / QNAP launcher integration
- [ ] Tailscale-friendly remote access docs

## Quick try (CLI on your Mac/Linux host)

Requires Go 1.23+.

```bash
git clone https://github.com/eddyvarelae/media-vault.git
cd media-vault
go build -o vault ./cmd/vault

# 1. Plan: see what would be copied
./vault scan tars /Volumes/tars /Volumes/nas-share/archive

# 2. Copy: do the work
./vault copy tars /Volumes/tars /Volumes/nas-share/archive

# 3. Verify: re-hash every file at the destination, compare to manifest
./vault verify tars /Volumes/nas-share/archive

# 4. Certify: emit a signed JSON proving the disk is fully archived
./vault certify tars ./tars-cert.json
```

The manifest and signing key live under `$VAULT_CONFIG` (default
`./vault-config/`). Re-runs are idempotent — files already in the manifest
with the same size + mtime are skipped. The certificate refuses to sign
unless every file is in `verified` status.

> **Wi-Fi caveat**: running on your laptop, `verify` is bottlenecked by
> SMB read speed (~700 KB/s in our testing for re-hashing footage on the
> NAS). The fix is to run vault *on the NAS itself* in a Docker container,
> where verify becomes a local disk read at hundreds of MB/s — see roadmap.

## How it works

1. **Scan** walks the source directory and looks each file up in the manifest
   (keyed on `source_disk` + relative path). Files not in the manifest are
   queued to copy. Files whose size or mtime changed are queued to recopy.
   Everything else is skipped.

2. **Copy** streams each file from source to destination through a
   `sha256.Hash`. The destination is written to a `.vault-partial` file and
   atomically renamed on success — so a crash mid-copy never leaves a
   half-written file claiming to be the real thing. The mtime is preserved so
   future scans skip cleanly. The manifest gets a new row with the hash.

3. **Verify** *(coming next)* re-reads the destination file, hashes it,
   compares to the manifest. Catches bit-rot, partial writes, silent disk
   errors. Updates `verified_at` on success, flips status to `mismatch` on
   failure.

4. **Wipe certificate** *(coming next)* takes a `source_disk` name and emits
   a signed report listing every file with its destination path and last
   verification timestamp. Refuses to certify if anything is unverified or
   mismatched.

## License

MIT — see [LICENSE](LICENSE).
