# Production topology (PM, 2026-09-16 - update when the NAS changes)

**NAS:** UGREEN **DXP2800** (`DXP2800-43F8`), UGOS, Docker app installed. **`192.168.1.167`** (DHCP-reserved; `ugreen.local` does not resolve). SMB user `figmaboi`, password in the Mini's Keychain (`figmaboi @ DXP2800-43F8.local`) - never echoed. **SSH: never set up by anyone** as of 2026-09-17 (B2b). Shares mount on the Mini under `~/mounts/<share>` via the `com.varela.mount-nas` LaunchAgent every 5 min (`/Volumes` needs root); `media` is mounted, `docker` is pending `NAS_SHARES="media docker"` in mini-server's `config/mini.env` (B2a).

**Paths on the NAS (all sacred, see TEAM.md):**
- `/volume1/docker/vault-nas-config/` - manifest DB + signing key. Single writer.
- `/volume1/media/<Camera>/` - the archive, **8.20 TiB, 67,735 rows** (Tester, 2026-09-17 snapshot). `source_disk` is **per camera, not per physical SSD**: `media-djiflip`, `media-djimini2`, `media-iphone`, `media-sonya6700`, `media-sonyzve10`, `media-gopro`, plus `media-backup`, `media-leantank`, and `files-kolab-videos` (inventory-only). There are no `nas-*` rows. Physical SSDs → cameras is known only from the logs: `tars` (Apr 27), `noahsarc` (Apr 28), `case` (Sep 1, from the Mini), `Eddy's Media Vault` (Sep 2, from the Mini); `kipp` never copied. Certificates: `/volume1/docker/vault-certs/` (correct place) and, wrongly, inside each camera tree.
- `/mnt/@usb/sdc1/` - the currently plugged source SSD (`tars` holds the four camera folders).
- `/volume1/docker/{tars-copy,verify-certify,prune}.log` - run logs.

**How work runs on the NAS:** `scripts/*.sh`, executed as root via `sudo nohup` so they survive SSH disconnect. Each wraps `docker run --rm ... ghcr.io/eddyvarelae/media-vault:<tag> <command>`. Sequential by design - parallel runs thrash the SATA pool and the manifest is single-writer.

**Deploy pipeline:** `.github/workflows/docker.yml` builds `linux/amd64,linux/arm64` on every push to `main` (→ `:latest`, `:main`, `:sha-*`) and on `v*` tags (→ semver). **Until B4 lands, the scripts pull `:latest`, so a merge to `main` is a production deploy.** After B4: scripts pin a version; the PM cuts tags.

**Machines:** Mac mini M4 (all seats, ethernet, hostname "Eddy's Mac mini") and MacBook Pro M3 (not a seat host; may reach seats via Remote Control).

**The Mini as job host (owned by the mini-server team - ask them via Eddy for machine changes; we own the jobs):**
- Scratch: `/Volumes/Scratch1` (2 TB SSD, `SCRATCH_DIR`) - **the former `noahsarc` source SSD, device-erased 2026-09-02 17:58** (Tester #17). Rule: pull to scratch, process locally, write back - never process over SMB (~7× slower).
- `~/vault-config/manifest.db` on the Mini is a **stale snapshot** (last write 2026-09-02, 67,735 rows, PM-verified 2026-09-17). Never a source of truth; the live manifest is on the NAS.
- Ollama (`127.0.0.1:11434`, llava), `~/Projects/video-tagger` (`.venv`, python 3.13). Logs `~/Library/Logs/mini-server/`; tagger state `~/Library/Application Support/mini-server/tagging-state.db`.
- LaunchAgent templates in mini-server `launchd/`; installed = `com.varela.mount-nas`, `com.varela.tea-daemon`. Reboot chain verified 2026-09-16; tmux and seat windows die on reboot.
- Source SSDs attach to the **Mini** (e.g. `tars`), not only the NAS - see B7.

**Speed facts:** verify over Wi-Fi from a laptop ≈ 700 KB/s; on the NAS ≈ 400+ MB/s. The Sep 13 full pass over all six camera disks took 02:42-12:50 (≈10 h) and ended with 1 failure (sonya6700, B24). That is why `--only-unverified` exists and why nothing verifies from a laptop.
