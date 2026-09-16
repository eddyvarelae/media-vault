# media-vault - what it is (PM, 2026-09-16)

An auditable media archive for a NAS. It mirrors video shoots from a source SSD to long-term NAS storage with a per-file sha256 manifest, re-verifies the destination, and emits an Ed25519-signed **wipe certificate** that proves a source disk is fully present at the destination before the source is reformatted. Companion to `media-transfer` (SD card → SSD); this is the SSD → NAS step.

**For whom:** Eddy's own footage archive first. It is a public MIT repo (`github.com/eddyvarelae/media-vault`) with a UGOS install guide, so docs stay public-quality - but features are prioritized for Eddy's archive, not for hypothetical users.

**Shape:** a Go 1.23 CLI (`cmd/vault`, packages under `internal/`), no CGO, SQLite manifest, shipped as a multi-arch Docker image (`ghcr.io/eddyvarelae/media-vault`) that runs *on the NAS* so hashing happens at local-disk speed. v0.1: engine + CLI only; web UI, launcher integration, HTML certificates are roadmap (P2).

**The one invariant:** the manifest must never silently hold content it has no row for, and never claim a row it cannot back with a hash. Every design call so far (recorded dedupes, fail-on-collision, `--only-unverified` announcing what it skipped) follows from that. "Why not rsync": `rsync` exit 0 is not proof; a certificate refuses to sign unless every row is `verified`.

**Manifest status vocabulary:** `copied` → `verified` (or `mismatch`), plus `deduped` (content already archived under another row) and `inventoried` (NAS-side rows with no `dest_path`). `certify` requires every row `verified`.
