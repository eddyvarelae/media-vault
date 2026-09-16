# review-requests.md - PM <-> Reviewer

Protocol: the PM stages each request at the top (what to review, its inputs, and **what would falsify it**). The Reviewer - a non-Claude model (Codex CLI), run on demand by the human or a wired command - answers inline below the request: `APPROVE`, `FINDINGS` (numbered), or `CANNOT VERIFY`. Every note dated and signed: **Role (YYYY-MM-DD):**. An unresolved FINDINGS means the claim or diff does not ship.

How to run: from the repo root, `codex "You are the Reviewer for media-vault. Read team/actors/reviewer.md, then answer the top request in team/channels/review-requests.md by appending your verdict to that file. Write nowhere else."`

## OPEN REQUESTS

### #1 - F4: `vault verify --only-unverified` (branch `verify-incremental`, `f964b60`)

**PM (2026-09-16):** Review the diff `git diff main..verify-incremental` (3 files, +91/-10: `cmd/vault/main.go`, `internal/manifest/manifest.go`, `internal/verify/verify.go`). Design notes and the agreed "done when" list are in `team/archive/2026-09-15-handoff-f3-f4.md` § F4. There are no tests in this repo (B3 is Dev's current order), so trace the code; `CANNOT VERIFY` on anything that needs a live manifest is an honest answer - say what's missing.

**Claims:**
1. With `--only-unverified`, only rows whose `status != 'verified'` are hashed; a bare `vault verify` still hashes every row.
2. `mismatch`, `deduped`, and `inventoried` rows are all included in the incremental pass.
3. The output states how many verified rows were skipped and the newest `verified_at`, labeled as a single-row date, not a full-sweep date.
4. A disk whose rows are all `copied` behaves identically under both modes.
5. `certify` is untouched; an incremental pass that promotes the last row makes `certify` succeed.

**This is wrong if:**
- The SQL predicate or the `list` selection lets any non-verified row escape the incremental pass, or lets a verified row into it.
- The flag loop mishandles `--only-unverified` placed before/between the positionals, or changes behavior for the two-positional bare call (`len(pos) != 2` must still reject wrong arity).
- Any path marks a row `verified` without hashing it (compare the promote logic in `verify.Run`/`RunWithOptions` - the diff must not change it).
- `CountVerifiedInDisk` can return a stale or wrong count relative to the rows about to be listed (both read the same DB without a transaction - decide whether that matters for a single-writer manifest).
- `time.Unix(0, newest)` mislabels the unit (`verified_at` is nanoseconds - confirm against the writer in `verify.go`).
- The `inventoried` fallback to `source_path` regresses.

Verdict goes below this line.

## Resolved

(none yet)
