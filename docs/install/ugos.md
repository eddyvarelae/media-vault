# Install on UGREEN UGOS (DXP series)

Tested on a UGREEN DXP2800 running UGOS. Should work the same on the
DXP4800 / DXP6800 / DXP8800 — they all share the same Docker app.

## Prerequisites

- UGOS with the **Docker** app installed (Control Panel → App Center)
- A shared folder where the manifest + signing key will live
  (e.g. `/volume1/personal_folder/vault-config/`)
- A shared folder for the destination archive
  (e.g. `/volume1/personal_folder/archive/`)
- The source disk plugged into one of the NAS's USB ports
  (it'll appear in UGOS as something like `/mnt/usb1/`)

## Step 1 — Pull the image

In the **Docker** app:

1. Open **Image** in the left sidebar
2. Click **Pull** (or "Add" / "+", depending on the firmware)
3. Image name: `ghcr.io/eddyvarelae/media-vault`
4. Tag: `latest`
5. Wait for the pull to finish (~30 s on home internet)

The image is multi-arch (`linux/amd64` for the DXP2800/4800/6800,
`linux/arm64` for any future ARM-based units). UGOS picks the right one
automatically.

## Step 2 — Create the container

In the **Docker** app, **Container** → **Create** (or "+"):

| Field           | Value                                                            |
| --------------- | ---------------------------------------------------------------- |
| Image           | `ghcr.io/eddyvarelae/media-vault:latest`                         |
| Container name  | `media-vault`                                                    |
| Restart policy  | `No` (v0.1 is one-shot CLI, not a long-running service)          |

### Volume mounts

You'll need three mounts. The container expects:

| Container path | What goes here                          | Example host path                          |
| -------------- | --------------------------------------- | ------------------------------------------ |
| `/sources`     | The source disk (read-only is fine)     | `/mnt/usb1/tars` (USB-attached SSD)        |
| `/dest`        | Where archived files land               | `/volume1/personal_folder/archive`         |
| `/config`      | Manifest DB and signing key (persistent)| `/volume1/personal_folder/vault-config`    |

In UGOS, this is usually under **Volume** → **Add Folder**:

- Click **Add Folder**, browse to the host folder, set the container path
- Mark `/sources` as **Read-only** if your UI offers it (vault never writes
  to the source, but defense in depth)
- Leave `/dest` and `/config` as **Read/Write**

### Command (the important part)

Each container run executes one vault command. v0.1 is one-shot — the
container does its work and exits. The four useful commands:

```
# Scan: see what would be copied
scan tars /sources /dest

# Copy: do the work, hashing as it goes
copy tars /sources /dest

# Verify: re-hash everything at /dest, compare to manifest
verify tars /dest

# Certify: emit a signed JSON proving the disk is fully archived
certify tars /dest/tars-cert.json
```

Set the container's **Command** field to one of those (without the leading
`vault` — that's the entrypoint).

> **Tip**: in UGOS, set up four containers — one for each command — so you
> can fire them with one click each. They all share the same image and the
> same `/config` mount, so they share the same manifest.

## Step 3 — First run

1. Start the `media-vault-scan` container
2. Open its **Log** panel — you'll see the plan (files to copy, total bytes)
3. Stop it
4. Start the `media-vault-copy` container — log shows live per-file progress
5. When it exits, start `media-vault-verify` — log shows verify status
6. When that's clean, start `media-vault-certify` — emits the signed JSON

The certificate ends up at `/volume1/personal_folder/archive/tars-cert.json`
(or wherever you point the certify command). Open it in a text editor, or
copy it off the NAS for safekeeping.

## Speed expectations

Running on the NAS itself (not over Wi-Fi), expect:

- **Copy**: limited by the slower of (a) the source USB bus and (b) the
  destination disk write speed. For a USB 3 SSD → internal SATA SSD,
  typically 200-400 MB/s.
- **Verify**: limited by destination disk read speed. Internal SATA: 400+
  MB/s. For comparison, doing the same verify over Wi-Fi from a laptop in
  our testing got **~700 KB/s** — about 500x slower.

This is the whole reason vault belongs on the NAS rather than on a laptop.

## Troubleshooting

**"manifest is locked" or similar SQLite errors**
→ Two containers tried to use `/config` at the same time. Stop one. Vault's
manifest is single-writer.

**Copy says "permission denied" on `/sources`**
→ The USB volume on the NAS may be mounted with restrictive permissions.
Check the container's **User** setting matches the file ownership on the
source disk, or run as root (uid 0) for testing.

**Verify is missing files that copied successfully**
→ Make sure `/dest` in verify points at the same host folder as `/dest` in
copy. The manifest stores destination paths *relative to the dest root*,
so the root must match across runs.
