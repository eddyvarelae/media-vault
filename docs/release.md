# Releasing

1. Merge to `main` only through `team/channels/review-requests.md` (APPROVE first). A merge builds nothing: CI runs on `v*` tags and manual dispatch only, and never publishes `:latest` — `docker/metadata-action` is pinned to `flavor: latest=false`, because its default (`latest=auto`) adds `:latest` to every stable semver tag even with no `latest` line in `tags:`.
2. The PM tags the release on the `main` tip: `git tag v0.2.0 && git push origin v0.2.0`. CI (`.github/workflows/docker.yml`) publishes `ghcr.io/eddyvarelae/media-vault:v0.2.0` (plus `:sha-<commit>`) for linux/amd64 + arm64.
3. Bump the default in all five `scripts/*.sh` (`IMG="${VAULT_IMAGE:-…:vX.Y.Z}"`) **in the same PR** as the release, so `main` never points the NAS at a tag that does not exist.
4. Override per run with `VAULT_IMAGE=ghcr.io/eddyvarelae/media-vault:sha-… sudo ./scripts/…` — never edit the default for a one-off.
5. A "deploy" is: the tag exists, and a `vault` command runs against the NAS manifest with it. Announce start and finish in your channel (`date -Iseconds`), one at a time.
