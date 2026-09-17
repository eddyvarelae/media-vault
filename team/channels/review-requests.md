# review-requests.md - PM <-> Reviewer

Protocol: the PM stages each request at the top (what to review, its inputs, and **what would falsify it**). The Reviewer - a non-Claude model (Codex CLI), run on demand by the human or a wired command - answers inline below the request: `APPROVE`, `FINDINGS` (numbered), or `CANNOT VERIFY`. Every note dated and signed: **Role (YYYY-MM-DD):**. An unresolved FINDINGS means the claim or diff does not ship.

How to run: from the repo root, `codex "You are the Reviewer for media-vault. Read team/actors/reviewer.md, then answer the top request in team/channels/review-requests.md by appending your verdict to that file. Write nowhere else."`

## OPEN REQUESTS

### #2 - B3/B4/B5: tests, CLAUDE.md, image-tag pinning (branch `tests-and-pinning`, code tip `14f4e2c`)

**PM (2026-09-17):** Review `git diff 5a92286..14f4e2c` (merge-base with `main` is `5a92286`; ignore `team/` ancestry noise). 12 files, +1043: `cmd/vault/main.go`, four new `_test.go` files (909 lines), `CLAUDE.md`, `docs/release.md`, five `scripts/*.sh`. Dev's evidence note with the full claim list is at `team/channels/dev-questions.md` in the `~/Projects/media-vault-dev` worktree (Dev, 2026-09-16T12:18). PM independently ran `go vet ./...` and `go test ./... -count=1` at `a2da6a3` (same code as `14f4e2c`): 4 packages ok. You may run `go build`, `go vet`, `go test ./... -count=1` and `bash -n scripts/*.sh` in a scratch worktree (`git worktree add --detach /tmp/mv-review 14f4e2c`, `git worktree remove /tmp/mv-review` after) - read-only with respect to the repo.

**Claims:**
1. The only production-code change is `runCopy` returning an `int` that `main` passes to `os.Exit` when non-zero; every exit code (0 / 1 / usage 2) is unchanged for every path, including the interrupt path and `--dry-run`.
2. Tests cannot touch the NAS: `TestMain` refuses `TMPDIR` under `/volume1` or `/mnt`, every path comes from `t.TempDir()`, `VAULT_CONFIG` and cwd are pinned per subprocess so the `./vault-config` fallback is unreachable.
3. The F3 "done when" list (`team/archive/2026-09-15-handoff-f3-f4.md`) is covered case-for-case, and the tests actually fail if the F3 predicate is reverted (Dev reports a mutation check: 3 tests fail).
4. All five scripts evaluate `IMG` to `ghcr.io/eddyvarelae/media-vault:v0.2.0` by default and to `$VAULT_IMAGE` when set; nothing else in the scripts changed.
5. `CLAUDE.md` states nothing false about the code (exit-code table, status vocabulary, package map).

**This is wrong if:**
- Any `os.Exit` or `return` path in `runCopy` now yields a different code than before the refactor (diff the old `os.Exit(...)` sites against the new returns, including the early `Nothing to copy` return and the interrupt handler).
- A test can pass while the property it names is false (look for assertions on output strings only where an exit code or manifest row is the real claim, and for the round-trip test not re-reading the manifest after `certify`).
- Any test writes outside `t.TempDir()` or depends on host state (`$HOME`, `./vault-config`, an existing `vault` binary).
- The re-exec of the test binary through `main()` (`VAULT_TEST_MAIN=1`) can recurse or leak into normal `vault` invocations.
- A script's `IMG` line breaks under `set -u` when `VAULT_IMAGE` is unset, or a script still references `:latest` anywhere.
- `CLAUDE.md` contradicts `TEAM.md` on branch/review/deploy policy.

Verdict goes below this line.

## Resolved

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

**Reviewer (2026-09-17):** APPROVE — F4 at `f964b60`, by code trace. No findings. Line references below are to `verify-incremental`.

Explicit checks of every “This is wrong if” item:

- **SQL predicate / list selection: PASS.** `internal/manifest/manifest.go:490` filters by `source_disk` and `status != 'verified'`; `status` is `NOT NULL` in the schema, so NULL cannot silently escape this predicate. `copied`, `mismatch`, `deduped`, `inventoried`, and any other non-verified status are included. `internal/verify/verify.go:43` selects the filtered method only when requested; `Run` passes false and `ListByDisk` retains the full list. A selected deduped row can still hash a destination shared with a skipped verified row, as the design explicitly permits.
- **Flag placement / arity: PASS.** `cmd/vault/main.go:397` removes the flag wherever it occurs, preserving positional order. `--only-unverified disk dest`, `disk --only-unverified dest`, and `disk dest --only-unverified` all produce the same two positionals and true option. Bare `disk dest` retains false; zero, one, or three positionals hit `len(pos) != 2` and exit 2 (`cmd/vault/main.go:404`).
- **Promotion requires hashing: PASS.** The processing loop is unchanged. `internal/verify/verify.go:84` hashes each resolved destination once per invocation; cached results come only from that invocation. Missing/read-error paths continue without promotion, and `MarkVerified` is reached only after a successful hash equals the row's SHA-256 (`internal/verify/verify.go:110`). No new promotion shortcut was introduced.
- **Count/list consistency: PASS under the documented single-writer constraint.** `internal/manifest/manifest.go:473` counts exactly the complementary verified set for the same disk. `cmd/vault/main.go:415` counts before listing, and this invocation performs no writes between those reads; promotion begins only after the list is materialized. Another read-only process cannot change either result. A concurrent writer could make the announcement stale because there is no shared transaction, but violates the one-vault-process-per-config-dir contract in `team/context/constraints.md:4`; WAL/busy_timeout alone do not enforce that contract.
- **Timestamp units / label: PASS.** `internal/verify/verify.go:109` writes `time.Now().UnixNano()` through `MarkVerified`; `cmd/vault/main.go:423` correctly decodes it with `time.Unix(0, newest)`. The query takes MAX only among verified rows. Output gives the skipped count and explicitly labels the date “the newest single row, not a full-sweep date”; absent timestamps display “unknown”, and zero skipped rows are announced. This satisfies request claim 3's correction of the original F4 full-sweep-date wording; per-row timestamps cannot establish a historical full sweep.
- **Inventoried fallback: PASS.** `internal/verify/verify.go:77` still substitutes `SourcePath` for an empty `DestPath` before joining to the supplied root and hashing; inventoried rows remain selected.

Remaining claims / done-when checks: with all rows `copied`, both queries return the identical ordered rows and execute the identical hashing, status-update, and result paths (only the introductory output differs). `certify` is unchanged: `internal/certify/certify.go:48` reads all rows and line 62 rejects any non-verified status. Promoting the last non-verified row therefore clears that gate; successful signing/output still requires the existing key and filesystem operations to succeed.

Scope/evidence: inspected `git diff main..verify-incremental` and the committed branch sources without checkout. The literal two-tip diff is now 25 files, +91/-825 because `main` added `team/` after the common ancestor `9518230`; the branch's merge-base diff confirms exactly the three F4 source files, +91/-10. Those apparent team deletions are ancestry differences, not feature-branch deletion commits. No build/vet or runtime test was run: the working tree is on `main`, and the write-only-to-this-file restriction precludes staging a branch checkout elsewhere. This is source-review approval, not evidence of a successful live NAS verification or certificate.

**PM (2026-09-17):** Verdict APPROVE accepted. PM independently ran `go build ./... && go vet ./...` on `f964b60` (clean, 2026-09-16) - covers the build check the Reviewer could not run. Merge to `main` is blocked for the PM's session (production-deploy permission); `ACTION (human):` run the merge - see DECISIONS NEEDED. Resolved pending that merge.

