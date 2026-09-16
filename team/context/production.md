# Production topology (PM, 2026-09-16 - update when the NAS changes)

**NAS:** UGREEN DXP series running UGOS, Docker app installed. Reached from the Mac mini over ethernet (exact access method: see open ACTION in `channels/tester-feedback.md`).

**Paths on the NAS (all sacred, see TEAM.md):**
- `/volume1/docker/vault-nas-config/` - manifest DB + signing key. Single writer.
- `/volume1/media/<Camera>/` - the archive. Disk names in the manifest: `media-djiflip`, `media-djimini2`, `media-iphone`, `media-sonya6700`, `media-sonyzve10`, `media-gopro` (the `nas-*` disks are inventory-only rows from the pre-migration NAS folders).
- `/mnt/@usb/sdc1/` - the currently plugged source SSD (`tars` holds the four camera folders).
- `/volume1/docker/{tars-copy,verify-certify,prune}.log` - run logs.

**How work runs on the NAS:** `scripts/*.sh`, executed as root via `sudo nohup` so they survive SSH disconnect. Each wraps `docker run --rm ... ghcr.io/eddyvarelae/media-vault:<tag> <command>`. Sequential by design - parallel runs thrash the SATA pool and the manifest is single-writer.

**Deploy pipeline:** `.github/workflows/docker.yml` builds `linux/amd64,linux/arm64` on every push to `main` (→ `:latest`, `:main`, `:sha-*`) and on `v*` tags (→ semver). **Until B4 lands, the scripts pull `:latest`, so a merge to `main` is a production deploy.** After B4: scripts pin a version; the PM cuts tags.

**Machines:** Mac mini M4 (all seats, ethernet, hostname "Eddy's Mac mini") and MacBook Pro M3 (not a seat host; may reach seats via Remote Control). The 5th SSD is now "Scratch1", extra storage on the Mac mini.

**Speed facts:** verify over Wi-Fi from a laptop ≈ 700 KB/s; on the NAS ≈ 400+ MB/s. A full verify of the 3.3 TiB archive ≈ 11 h. That is why `--only-unverified` exists and why nothing verifies from a laptop.
