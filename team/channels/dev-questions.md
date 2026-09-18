# dev-questions.md - PM <-> Dev

Protocol: the PM's current WORK ORDER lives at the top (newest supersedes; work it top-down). Questions and answers below it, inline, newest question first. Every note dated and signed: **Role (YYYY-MM-DD):**.

Boot line (Mac mini, visible terminal, Opus-class, Remote Control on): `cd ~/Projects/media-vault-dev && claude --model opus --remote-control media-vault-dev "You are the Dev for media-vault. Read team/TEAM.md, team/actors/dev.md, then team/channels/dev-questions.md."`

## WORK ORDER - rev 4 (issued for after the 2026-09-17 restart; starts when review #3 resolves)

**PM (2026-09-17, 18:40):** **Review #3 came back FINDINGS (four) - read them in `review-requests.md` #3.** So item 0 first, on `tests-and-pinning`, new commits only:

0. **#3 findings.** (1) `testguard`: `EvalSymlinks` before any lexical cleaning - resolve the path as given (component by component if needed), then compare; reject a path that cannot be fully resolved; add the `link/../x` case to the guard test without touching real roots. (2) Also check `GOTMPDIR` (that is what `t.TempDir()` actually uses in this toolchain) - both env vars must resolve outside `/volume1` and `/mnt`; test it. (3) `docker.yml`: add `flavor: latest=false` to the metadata step; `docs/release.md` + CLAUDE.md say why. (4) CLAUDE.md exit table: `move --rule <malformed>` with wrong arity exits 2 (arity is checked first at `main.go:697`); say "no/unknown command or wrong arity → 2" and list the ordering only where it differs. READY FOR REVIEW → PM stages #4 (fixes only) → merge → `v0.2.0`.

Then: new branch `overwrite-guard` off the merged `main`. The Tester found data loss (its item #5, read it and #7, #9 in full first). Priority order, one PR per numbered item unless told otherwise:

1. **B23(b) - `copy` must never overwrite a `verified` destination with different content.** Today a row with the same `(source_disk, source_path)` but changed size/mtime is "recopy": the destination is replaced in place, no collision policy consulted, and the previous verified bytes are gone. That is how 2,668 photos died on 2026-09-01. First commit: a test that reproduces it (verified row → same path, different bytes → destination replaced, old hash unrecoverable). Second commit: the fix. **Default policy, pending Eddy's decision in DECISIONS.md (PM recommendation): treat it as a destination collision** - the existing destination is never touched; with `--on-collision rename-mtime-year` the *new* file lands under the renamed path and gets its own row; without it the file is skipped, counted in `INCOMPLETE:`, exit 1. The old row keeps its hash and `verified` status. If DECISIONS.md says otherwise by the time you get here, follow that.
2. **B24 - manifest dest_path repair.** A `vault repair-dest <disk> <dest-root> --dry-run` subcommand: for each row whose `dest_path` does not exist under the root, look for the same basename at one directory level down (here `CLIP/`, `DCIM/`), and only if that file's size **and sha256** match the row, rewrite `dest_path`. `--dry-run` prints the plan; without it, it writes, one row at a time, and prints each change. Tests on temp dirs. This is run on a **snapshot** first by the Tester, then on the live manifest by the PM as a logged operational act.
3. **B25 - `certify` refuses an output path inside the destination root** (it already blocked 39,219 files once). Tests.
4. **B9 - F4 tests** for `--only-unverified` (the F4 "done when" list in `team/archive/2026-09-15-handoff-f3-f4.md`).
5. **B27** - `nas-verify-certify-all.sh` double-logs; drop the inner `tee` or the outer redirect, whichever keeps the log readable when run by hand.

6. **B20 - `scan` skips `reports/` directories** at any depth under the root (they are the nightly tagger's output, never media). Decided by Eddy 2026-09-17. Test: a `reports/` dir with files inside a scanned tree yields no rows and no "unrowed file" noise; a file literally named `reports` is not a dir and is still scanned. *(Item added by the PM 2026-09-17T19:12:40-07:00; items 1-5 unchanged.)*

Evidence as usual (`go test ./... -count=1`, rung `tested`). READY FOR REVIEW per item; the PM stages one review per PR. B17 (tagger) comes after these.

## WORK ORDER - rev 3 (under review #3 - frozen)

**PM (2026-09-17):** Review #2 came back **FINDINGS** (five, read them in full in `team/channels/review-requests.md`). `tests-and-pinning` is unfrozen **for fixes only** - new commits on top of `14f4e2c`, never a rewrite. Do this before rev 2's `ci-and-docs` items; rev 2 is superseded below and its items 1-3 fold in here as item 6 so the CLAUDE.md edits land once.

1. **Finding 1 (P1) - NAS guard in every writing test package.** One shared guard (a tiny internal test helper package, or a copied `TestMain` if you'd rather avoid a new package - your call, say which) that refuses to run when the *resolved* absolute temp root (`filepath.EvalSymlinks` + `filepath.Abs`) is under `/volume1` or `/mnt`. Apply in `cmd/vault`, `internal/certify`, `internal/scan`, `internal/copy`. Prove it: a test-of-the-guard that sets `TMPDIR` to a symlink pointing at a temp dir named `.../volume1/...` is fine as long as it never touches the real roots - or explain why you can't test it safely and I'll accept a trace.
2. **Finding 2 - round-trip asserts persisted manifest state.** Reopen the manifest after `certify` and compare rows (path, hash, status, `verified_at`) against expectations; for the missing-file case, snapshot the row before and assert it is byte-identical after (status untouched).
3. **Finding 3 - shell `FAILED` branch test.** A bash test (`scripts/test/`, run by `go test` via `exec` or a `make test`-style target - keep it in `go test ./...` so one command covers everything) that runs `nas-tars-copy-all.sh` with `docker` shadowed by a stub on `PATH` that exits 1, `LOG` pointed at a temp file, and asserts the log contains `FAILED`. The script hard-codes `LOG=/volume1/docker/tars-copy.log` - make it `LOG="${VAULT_LOG:-/volume1/docker/tars-copy.log}"` (same shape as `IMG`), nothing else changes.
4. **Finding 4 - `CLAUDE.md:68`.** Replace "when the disk was last fully verified" with the approved F4 wording: the newest single-row `verified_at`, explicitly not a full-sweep date. A full sweep has no recorded date; say so.
5. **Finding 5 - exit-code table.** Exit 2 = wrong positional arity only; exit 1 = `INCOMPLETE:` runs *and* every `die` (bad flag value, config/scan error, signing/output error, empty manifest) *and* interrupt. One table, all commands, matching `cmd/vault/main.go` as it is.
6. **Former rev 2 items 1-3** (B16 CI on tags only + `ugos.md` tags; B15 gofmt as its own commit; B14 README). Same branch, after 1-5.

**Evidence expected:** `go test ./... -count=1` green including the new shell test; paste the guard's refusal output from a deliberately bad `TMPDIR`; `gofmt -l` empty; the resulting CI `on:` block. Rung: `tested`. READY FOR REVIEW with the tip SHA; the PM stages #3 as "check the fixes" only. Do not merge.

## WORK ORDER - rev 2 (Superseded by rev 3 on 2026-09-17 - folded into rev 3 item 6, not started)

**PM (2026-09-17):** Branch `ci-and-docs` off the current `main` tip (`f7df496` - team files only on top of `5a92286`, so code-identical to your rev-1 base). Switch your worktree to it; leave `tests-and-pinning` exactly as committed. Small PR, four items:

1. **B16 - CI publishes on tags only.** `.github/workflows/docker.yml`: drop the `branches: [main]` push trigger; keep `tags: ["v*"]` and `workflow_dispatch`. Drop the `type=raw,value=latest` and `type=ref,event=branch` tag lines so `:latest` is never republished by accident (keep `sha-` and semver). Update `docs/release.md` and the CLAUDE.md deploy line to match. Also `docs/install/ugos.md`: replace the two `latest` references (lines ~23/36) with `v0.2.0` and one sentence on where to find the current tag.
2. **B15 - `gofmt -w`** on the three flagged files, as its own commit, whitespace only.
3. **B14 - README "How it works"** steps 3 and 4 still say "coming next"; rewrite them as present tense matching the code. Add `--only-unverified` to the README's verify example with the one-line warning that it is not an integrity sweep.
4. Nothing else. B9 (F4 tests) is a separate branch once F4 is on `main` - I will say when.

**Evidence expected:** `bash -n` on nothing (no scripts change), `gofmt -l` empty, `go test ./... -count=1` still green, and for item 1 paste the resulting `on:` block. Rung: `tested`. Write READY FOR REVIEW with the tip SHA; do not merge.

## WORK ORDER - rev 1 (Superseded by rev 2 on 2026-09-17 - completed, under review #2)

**PM (2026-09-16):** You work in your own worktree, `~/Projects/media-vault-dev` (branch `tests-and-pinning`, created by the PM off `main`) - never in `~/Projects/media-vault`, which is the PM's checkout on `main`. `team/` files are shared through git: commit your channel notes on your branch and the PM merges them; if you need a note seen immediately, also write it into `~/Projects/media-vault/team/channels/dev-questions.md` (same file, PM's checkout). **Do not touch `verify-incremental`** - it is under Reviewer review (request #1). Three items, one PR, in this order:

1. **B3 - test harness.** `go test ./...` must exercise the real pipeline on a temp dir + temp manifest (`t.TempDir()`, no NAS, no network): scan → copy → verify → certify round-trip; recopy on changed size/mtime; `.vault-partial` never survives a failed copy; `certify` refuses on any non-`verified` row; and the F3 "done when" list (`team/archive/2026-09-15-handoff-f3-f4.md` § F3) as explicit cases - unresolved collision → exit 1 with `collisions` named in `INCOMPLETE:`; every-rename-succeeds → exit 0; dry-run / no-op → exit 0. `runCopy` calls `os.Exit` - refactor the minimum needed to make the exit decision testable (e.g. return a status), no framework, no behavior change. Also: tests must not reach `/volume1` or `/mnt` even if those exist on the host.
2. **B5 - `CLAUDE.md`** at repo root: how to build/test, the package map, the manifest status vocabulary, the exit-code contract, the "announce what you skipped" rule, and the branch/review policy from `team/TEAM.md`. Short; it is the contract external agents read.
3. **B4 - pin the image tag.** All five `scripts/*.sh` take `IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.0}"` (one variable each, env-overridable). `v0.2.0` will not exist until the PM tags it after this PR and F4 merge - say so in a comment. Add a `docs/release.md` (5 lines: tag on `main`, CI publishes semver, bump the scripts' default in the same PR as a release).

**Evidence expected:** paste `go test ./... -count=1` output and `go vet ./...` in this channel under your rev-1 note; state the rung (`tested` is the ceiling for you). When done, write "READY FOR REVIEW" with the branch tip SHA; the PM stages it in `review-requests.md`. Do not merge.

**Out of scope for rev 1:** F4 tests (B9 - after #1 merges), anything under `internal/verify` (frozen until #1 resolves), README roadmap items.

Flag in-progress local work at the top of your first note so the Tester knows your worktree is live.

## Dev notes

**PM (2026-09-17T20:22:28-07:00) - #9: FINDINGS (1, P1) on `overwrite-guard`; #10 not yet reviewed (Codex usage limit until 21:34).** Read #9 in full: a symlinked directory *component* under the root (`dst/alias -> real`) makes `alias/x.mov` and `real/x.mov` one physical file; the key does not see it and the writer's `Lstat` follows intermediate links. Fix on `overwrite-guard`, new commits: walk every destination path (staging and final) component by component from the root with `Lstat` and refuse any symlink component - before admitting a task in `scan.Build` and again in `copy.File` before any open or rename (same discipline as your `repair-dest` `containedDir`). Regression: the Reviewer's exact cross-disk recopy through `dst/alias`. READY FOR REVIEW → **#11**. Pause `certs-out` to do this first; it is the branch that unblocks `kipp`.

**PM (2026-09-17T20:20:48-07:00) - #9 (`abc863e`) and #10 (`c6d9d42`) both accepted at `tested` and staged; Codex runs them back to back.** PM re-ran vet/gofmt/tests at both tips: reproduce. Both branches frozen. Your `..`-in-rules note → **B34** (do it with item 3). Next, on a new branch **`certs-out` off `main`**: **item 3 (B25)** - `certify` refuses an output path inside the destination root (physical check, same key discipline), test; plus **B34** (refuse `..` in `--rule`, test) in the same PR since both are one-liners with a test. Then item 4 (B9 F4 tests) and item 5 (B27), item 6 (B20 `reports/`). READY FOR REVIEW per PR.

**PM (2026-09-17T20:12:11-07:00) - #7 on `repair-dest`: FINDINGS (3), accepted. Renumbering: your `overwrite-guard` fixes → review **#9**; your `repair-dest` fixes → **#10** (#8 is the Tester's numbers).** Order: finish `overwrite-guard` first (READY FOR REVIEW → #9), then `repair-dest`: (1) **P1** candidates via `Lstat`; reject a symlink leaf and any symlinked directory component (the row's own dir included); prove the accepted path is physically under the root; regression: leaf symlink to a matching file outside the root, and `root/Videos` symlinked outside. (2) **P2** "intact" requires a regular file; a directory or other object at the old `dest_path` → unresolved, `INCOMPLETE:`, exit 1, CLI test. (3) **P2** contract wording as in #5-2: dry-run output and CLAUDE.md say "no archive file, no manifest row"; initialization is pre-existing (B31). Plus B33 (`OWNED` outcome) since you are in that code. READY FOR REVIEW → #10.

**PM (2026-09-17T20:08:56-07:00) - #6: FINDINGS (1, P1) on `overwrite-guard`; item 2 accepted and staged as #7.** Read #6 in full. The guard compares *spellings*; the writer touches *physical paths* - case-insensitive roots (`X.mov.vault-partial` vs `x.mov.vault-partial`) and a `..` routing rule both slip past. Fix on `overwrite-guard`, new commits, then READY FOR REVIEW → **#8 (fixes only)**. Required outcome, mechanism yours: (a) the staging file is opened **`O_EXCL`** - nothing that exists is ever truncated - and a task is refused (reported, `INCOMPLETE:`, exit 1) when `Lstat` finds anything at the writer's actual staging path or final path that is not the task's own prior `.vault-partial` from an interrupted run (say how you tell them apart; if you cannot, refuse); (b) ownership compares the cleaned, root-joined physical path of every `verified` row against the writer's cleaned physical target, not the stored spelling (a `../archive/x` rule spelling and `archive/x` must collide). Regression cases: the Reviewer's two examples (case-insensitive root - use a temp dir on this Mac, APFS default is case-insensitive, and skip the test if the temp FS turns out case-sensitive; and the `--rule vault-partial=../archive` one). `repair-dest` (`e3c694b`) is accepted at `tested` and under #7 - frozen; PM re-ran vet/gofmt/tests: 7 ok. Your `repair-dest` "other row owns the candidate" note: report it as a fourth outcome `OWNED` (never chosen) - small follow-up on that branch only if #7 asks for changes, else B33.

**PM (2026-09-17T20:06:14-07:00) - fixes accepted at `tested`; staged as #6.** PM re-ran vet/gofmt/tests at `c03eb68`: reproduces. `move` not consulting `VerifiedOwner` → **B32**. `overwrite-guard` frozen under #6; resume `repair-dest` from `06abba3` meanwhile.

**PM (2026-09-17T19:59:31-07:00) - review #5: FINDINGS (3), read them in full in `review-requests.md`. Item 1 fixes first, on `overwrite-guard`, new commits only.** Pause `repair-dest` if you started it (commit what you have; it is not under review). (1) **P1 - destination-level guard:** before *any* write - final path or `.vault-partial` staging path - refuse if any `verified` row (any `source_disk`) references that `dest_path`; a `deduped` row recopy, a new file whose name collides with a verified `.vault-partial`, and the Reviewer's disk-A/disk-B scenario are the regression cases. This subsumes B30. (2) **P2 - contract wording:** `CLAUDE.md`: `--dry-run` writes no archive file and no manifest row; config-dir/manifest initialization is pre-existing and stays (B31 later). (3) **P2 - reporter:** call it unconditionally before the no-op return; tests for zero-count and retouched-only output. Evidence as usual; READY FOR REVIEW with the tip SHA → I stage #6 (fixes only). Then back to `repair-dest`.

**PM (2026-09-17T19:57:17-07:00) - item 1 accepted at `tested`; staged as review #5; start item 2 in parallel.** PM re-ran vet/gofmt/tests at `a6c74a5`: reproduces. Your schema reading is accepted as the implementation of the decision - the old file always wins, and the rename path is "copy the card under its own `<disk>` name", which the output now says; I am telling Eddy in one line, not reopening it. The same-size hash check stays (Deferred note in BACKLOG). Your `deduped`-recopy observation is **B30**. `overwrite-guard` is frozen under #5 - fixes only if it comes back FINDINGS. Meanwhile: **item 2 (B24 `repair-dest`) on a new branch `repair-dest` off `main`** (`f21b4c5` or later - `main` is team-only past `e4a4aed`), not off `overwrite-guard`. READY FOR REVIEW with the tip SHA as usual.

**PM (2026-09-17T19:25-07:00) - framework v2.3 applied** (`team-framework` `b142b11`; ours was v2.1 `276e893`): `actors/pm.md` and `actors/reviewer.md` byte-copied; `TEAM.md` gained the Reviewer `</dev/null` invocation, seat-to-seat messaging, "Talking to the human", daemon-machine hygiene, and startup step 0 (framework check every PM boot). Nothing changes for Dev. FYI only.

**PM (2026-09-17T19:13:40-07:00) - v0.2.0 image published, `observed`.** CI run 35298191638 success; metadata-action `tag-names` = `["v0.2.0","sha-e4a4aed"]` - **no `latest`** (B16's claim now holds on a real tag push, not just by trace). Image digest `sha256:fe3c2724…`. The NAS scripts' default `ghcr.io/eddyvarelae/media-vault:v0.2.0` now resolves.

**PM (2026-09-17T19:10:02-07:00) - #4 APPROVE; merged; v0.2.0 cut. Go to rev 4 item 1.** `tests-and-pinning` merged `--no-ff` as **`e4a4aed`** on `main`, pushed. **DEPLOY LOCK (PM) 2026-09-17T19:08:45-07:00 → released 2026-09-17T19:10:02-07:00:** `v0.2.0` tagged on `e4a4aed` and pushed; CI publishes `ghcr.io/eddyvarelae/media-vault:v0.2.0` (run 35298191638) - nothing on the NAS pulls it until B6 runs. Your branch is done; leave it. New branch **`overwrite-guard` off `main` at `e4a4aed`** in your worktree (`git checkout -b overwrite-guard main` after `git fetch`/`git merge` so `main` is current). Item 1 policy is now **decided** (DECISIONS.md 2026-09-17, last entry): a `verified` destination is never overwritten - build exactly that. Reproducing test first, fix second, one PR. READY FOR REVIEW with the tip SHA.

**Dev (2026-09-17T18:52-07:00) - rev 4 item 0 done. READY FOR REVIEW.** **Code tip `52d30b0`** on `tests-and-pinning`: `git merge main` (`2eb2195`, one trivial conflict in this channel file - took `main`'s headings, both note sets kept) as `0510072`, then three fix commits. No rewrite (`c1f7fbd` and `main` are both ancestors). Worktree clean, live but idle. **Rung: `tested`.** Nothing under `internal/verify` touched; the merged F4 code is `main`'s, byte for byte.

Finding → commit:
1. **#3-1 + #3-2 → `840c957`** - `testguard.resolve` no longer calls `filepath.Abs`/`Clean` at all. It walks the path one component at a time against an already-resolved base (cwd for a relative path, itself walked the same way), `Lstat`s each component, follows a symlink *where it occurs* (target walked recursively, absolute targets restart at `/`), and applies `..` to the directory actually reached. A component that does not exist, a dangling link, or a link loop (40 hops, the kernel's limit) is **refused as unresolvable** - per the order; the old "re-append the missing tail" is gone. `Require()` → `CheckEnv(Roots)`: checks `os.TempDir()` and, when set, `GOTMPDIR` (which `testing.T.TempDir` uses directly - confirmed at `$(go env GOROOT)/src/testing/testing.go:1613`); each refusal names the variable. **Reproduced first, then fixed:** with the new tests against the old resolver, `link/../x into root` was allowed (finding 1) and `GOTMPDIR bad` / `GOTMPDIR bad, TMPDIR unset` were allowed (finding 2) - 6 subtests failed; all pass now. Test paths are built by string concatenation, not `filepath.Join`, so the `..` reaches the guard (the Reviewer's point about the old test). New cases, all against a fake `volume1` under `t.TempDir()`: `link/../../tmp` where lexically it is the harmless `base/tmp` (exists) but the kernel lands in `volume1/tmp`; a symlink whose *target* is `../volume1/docker/tmp`; `base/tmp/../volume1/docker`; and the mirror image - `volume1/escape-link/../x` where the link leaves the root, which lexically looks forbidden and the kernel-order guard correctly **allows** (the old guard refused it). Unresolvable: dangling link, missing tail inside and outside the root, `nope/../volume1`, a two-link loop. `CheckEnv`: both safe / `GOTMPDIR` unset / `TMPDIR` bad / `GOTMPDIR` bad / `GOTMPDIR` bad with `TMPDIR` unset / both bad, via `t.Setenv` with fixtures taken before either variable moves. Refusal text is `t.Logf`'d so `-v` shows it.
2. **#3-3 → `20550c0`** - `flavor: latest=false` on the metadata step with a two-line comment; `docs/release.md` step 1 and the CLAUDE.md deploy line say why (`latest=auto` adds `:latest` to every stable semver tag on its own). Parsed the YAML: `with` = `{images, flavor: "latest=false", tags: "type=sha,prefix=sha-\ntype=semver,pattern=v{{version}}"}`. `on:` block unchanged from rev 3.
3. **#3-4 → `52d30b0`** - CLAUDE.md: "**2** — no command, an unknown command, or wrong positional arity"; ordering stated only where it differs: `scan`/`copy` validate every flag value before arity (bad `--rule`/`--on-collision` + wrong arity → 1); `move` validates missing values and `--on-collision` before arity but `--rule` values after it (`move --rule <malformed>` + wrong arity → 2). Pinned by `TestExitCodeOrdering` in `cmd/vault`: 11 cases through `main()` including the Reviewer's exact one (`move --rule bad a b c` → 2) and its control (`move --rule bad` with four positionals → 1).

Evidence (Mac mini, `2026-09-17T18:50:06-07:00`, go1.27.0 darwin/arm64, at `52d30b0`):
```
$ gofmt -l .                     → (empty)
$ go vet ./...                   → clean
$ bash -n scripts/*.sh scripts/test/*.sh   → clean
$ go test ./... -count=1
ok  	github.com/eddyvarelae/media-vault/cmd/vault	0.641s
ok  	github.com/eddyvarelae/media-vault/internal/certify	0.236s
ok  	github.com/eddyvarelae/media-vault/internal/copy	0.699s
ok  	github.com/eddyvarelae/media-vault/internal/scan	0.555s
ok  	github.com/eddyvarelae/media-vault/internal/testguard	0.830s
ok  	github.com/eddyvarelae/media-vault/scripts/test	1.297s
$ CGO_ENABLED=0 go test ./... -count=1    → same 6 ok
71 PASS lines (tests + subtests; was 44)
```

Guard refusals **against fake roots** (`go test ./internal/testguard -v`, `$T` = the host temp dir, fake root = `$T/…/001/volume1`):
```
link/../x into root:       refused: temp dir "$T/…/001/innocent-link/../../tmp" resolves to "/private$T/…/001/volume1/tmp", under forbidden root $T/…/001/volume1
symlink with .. in target: refused: temp dir "$T/…/001/sub/rel-link" resolves to "/private$T/…/001/volume1/docker/tmp", under forbidden root $T/…/001/volume1
GOTMPDIR bad:              refused: GOTMPDIR: temp dir "$T/…/001/volume1/review-tmp" resolves to "/private$T/…/001/volume1/review-tmp", under forbidden root $T/…/001/volume1
missing then ..:           refused: cannot resolve temp dir "$T/…/001/nope/../volume1": lstat /private$T/…/001/nope: no such file or directory
```
Compiled binaries (`go test -c`) against the **real** roots, run by hand; `/volume1` and `/mnt` absent on this host before and after, nothing created:
```
$ GOTMPDIR=/volume1/review-tmp TMPDIR=$SCRATCH/ok ./copy.test
testguard: refusing to run: GOTMPDIR: cannot resolve temp dir "/volume1/review-tmp": lstat /volume1: no such file or directory
exit 1
$ TMPDIR=/volume1/review-tmp ./vault.test   → refusing to run: TMPDIR: cannot resolve … lstat /volume1 …   exit 1
$ TMPDIR=/mnt/@usb/x ./copy.test            → refusing to run: TMPDIR: cannot resolve … lstat /mnt …       exit 1
$ TMPDIR=$SCRATCH/ok ./copy.test; GOTMPDIR=$SCRATCH/ok TMPDIR=$SCRATCH/ok ./copy.test   → PASS, exit 0
```
On this host the real-root message is "cannot resolve" because `/volume1` does not exist here; on the NAS, where it does, the same input takes the "under forbidden root" branch shown in the fake-root output. Both are exit 1 before any fixture is created.

Noticed, not acted on:
- A non-existent temp root is now refused where rev 3 allowed it (with the tail re-appended). Behavior change inside the guard only, per item 0(1) "reject a path that cannot be fully resolved"; `os.MkdirTemp` would have failed there anyway, so no passing configuration became a failing one except a misconfigured host - which now gets a reason on stderr instead of a `TempDir: mkdir … no such file` per test.

**PM (2026-09-17T18:50-07:00) - resumed after the restart.** Rev 4 stands exactly as written above; start at item 0 on `tests-and-pinning` (new commits on top of `c5c5e53`, no rewrite). `main` is `549ba08` = `7cca025` + team files, so no rebase is needed for item 0; branch `overwrite-guard` (item 1 onward) comes off the merged `main` after #4. When item 0 is READY FOR REVIEW, give me the tip SHA and the guard's refusal output for `GOTMPDIR=/volume1/...` and for a `link/../x` spelling, both against fake roots. I run Codex within minutes of the note.

**Dev (2026-09-17T18:31-07:00) - STOP for restart.** Nothing in flight: all rev-3 work is committed (code tip `c1f7fbd`, note `f764a75`), worktree clean apart from the PM's own unstaged `BACKLOG.md`/`review-requests.md`. Stopped at: READY FOR REVIEW, waiting on review #3. Next: whatever #3 returns, else B9 (F4 tests) on a fresh branch once `verify-incremental` is on `main`.

**Dev (2026-09-17T18:21-07:00) - rev 3 done. READY FOR REVIEW.** **Code tip `c1f7fbd`** on `tests-and-pinning` - seven new commits on top of `14f4e2c`/`a2da6a3`, no rewrite (`git merge-base --is-ancestor 14f4e2c HEAD` holds). Worktree live but idle. **Rung: `tested`.** `internal/verify` and `verify-incremental` untouched (one local mutation of `verify.go` for a check, reverted, `git status` clean there). The PM's uncommitted `team/BACKLOG.md` / `review-requests.md` edits sit in my worktree unstaged - PM paths, left alone.

Finding → commit, in order:
1. **F1 → `71bb5c5`** - chose the **shared package** `internal/testguard` over four copied `TestMain`s: one resolver to review, one test of the guard, nothing to drift. `Require()` resolves `os.TempDir()` (`Abs` + `EvalSymlinks`; a dangling symlink followed by `Readlink`; a non-existent tail re-appended to its longest existing ancestor) and exits 1 if under `/volume1` or `/mnt` (roots compared in literal and resolved spellings). Called first thing in `TestMain` of `cmd/vault`, `internal/{certify,scan,copy}` (+ `testguard` itself, `scripts/test`). `cmd/vault`'s private guard and `safeDir` are gone. **Tested safely** via `CheckUnder(dir, roots)` against a fake root under `t.TempDir()`: exact root, child, symlink, relative symlink, dangling symlink, non-existent tail, `..` spelling all refuse; sibling, `volume10`, and a deeper dir merely *named* `volume1` pass (root-anchored, not substring - which is also why the PM's "symlink to a temp dir named `.../volume1/...`" must NOT trip the real guard). CLAUDE.md test paragraph now states the suite-wide mechanism.
2. **F2 → `55a28a9`** - `rowsOf()` reopens the manifest fresh at each stage. Asserted: rows after copy (dest, sha256-of-content, size, `copied`, `verified_at` 0); after verify (all `verified`, `verified_at` set, sha/`copied_at` unchanged); **across certify: `reflect.DeepEqual` before/after**, and every cert file ref = its row on dest/sha/size/`verified_at`, counts equal; after recopy (new sha + mtime, `copied`, `verified_at` 0, the two other rows DeepEqual their prior state, re-verify advances `verified_at`); after corruption (row keeps the sha the file *had*, `mismatch`, stamped; others still `verified`); **missing file: row snapshot DeepEqual after**. Mutation: making verify's missing branch call `MarkMismatch` fails `missing-file verify touched the row`.
3. **F3 → `15ccdd5`** - `nas-tars-copy-all.sh`: `LOG="${VAULT_LOG:-/volume1/docker/tars-copy.log}"`, nothing else (the test greps for that exact default line). `scripts/test/nas-tars-copy-all.sh` runs the real script with a `docker` stub on `PATH` that records argv and exits 1 / 0: stub-1 → all four cards log `<card> FAILED`, none `done`, exactly 4 FAILED lines, pass runs to the end, stub saw the real `copy media-djiflip /usb/sdc1/DJIFlip /volume1/media/DJIFlip … --on-collision rename-mtime-year` argv (so it was the thing invoked); stub-0 → four `done`, zero FAILED. `scripts/test/scripts_test.go` runs it under `go test ./...` (guarded, `TMPDIR=t.TempDir()`). Mutation: `FAILED`→`done` in the script fails 8 checks.
4. **F4 → `50fc0d2`** - CLAUDE.md "announce" rule: newest **single-row** `verified_at`, labeled as one row's date, explicitly not a full sweep; "no command records when a disk was last fully verified; per-row timestamps cannot prove it, so never claim it."
5. **F5 → `50fc0d2`** - exit-code contract rewritten from every `os.Exit`/`die` site: **2** = wrong positional arity / no or unknown command, nothing else; **1** = every `die` (bad flag value or missing value, config dir / manifest open, scan/plan, manifest write, sign/marshal/write, empty manifest) + interrupt + per-command conditions; flags parse *before* arity for `scan`/`copy`/`move` so bad-flag-with-wrong-arity is 1. One table, all 15 commands, with an honest "exits 0 even though" column - see "noticed" below.
6. **Former rev 2 → `d007744` (B16), `d146091` (B15), `c1f7fbd` (B14).** B16: `on:` block below; `type=ref,event=branch` and `type=raw,value=latest` dropped, `sha-` + semver kept; `ugos.md` two `latest` → `v0.2.0` + one sentence on where the current tag lives; `docs/release.md` step 1-2 and CLAUDE.md deploy line updated in the same commit; only remaining `latest` in the repo is `runs-on: ubuntu-latest`. B15: `git diff -w` empty for the three files. B14: steps 3-4 present tense, `--only-unverified` example with the not-an-integrity-sweep warning; also dropped the "(soon)" in "Why not rsync" - same stale fact, same doc, saying so here.

```yaml
on:
  push:
    tags: ["v*"]
  workflow_dispatch:
```

Evidence (Mac mini, `2026-09-17T18:20:23-07:00`, go1.27.0 darwin/arm64):
```
$ gofmt -l .                      → (empty)
$ go vet ./...                    → clean
$ bash -n scripts/*.sh scripts/test/*.sh   → clean
$ go test ./... -count=1
ok  	github.com/eddyvarelae/media-vault/cmd/vault	0.467s
ok  	github.com/eddyvarelae/media-vault/internal/certify	0.489s
ok  	github.com/eddyvarelae/media-vault/internal/copy	0.677s
ok  	github.com/eddyvarelae/media-vault/internal/scan	1.045s
ok  	github.com/eddyvarelae/media-vault/internal/testguard	0.845s
ok  	github.com/eddyvarelae/media-vault/scripts/test	1.474s
(dedup, importer, inventory, manifest, move, verify: no test files)
$ CGO_ENABLED=0 go test ./... -count=1     → same 6 ok
44 PASS lines (tests + subtests)
```

Guard refusal, from compiled test binaries (`go test -c`) run by hand - nothing created, `/volume1` and `/mnt` still absent on this host afterwards:
```
$ TMPDIR=/volume1/review-tmp ./vault.test        (same for certify/scan/copy .test)
testguard: refusing to run: temp dir "/volume1/review-tmp" resolves to "/volume1/review-tmp", under forbidden root /volume1
exit 1
$ TMPDIR=$SCRATCH/innocent-link ./copy.test      (innocent-link -> /volume1/review-tmp, dangling)
testguard: refusing to run: temp dir ".../scratchpad/guard/innocent-link" resolves to "/volume1/review-tmp", under forbidden root /volume1
exit 1
$ cd /usr && TMPDIR=../mnt/@usb/x ./copy.test
testguard: refusing to run: temp dir "../mnt/@usb/x" resolves to "/mnt/@usb/x", under forbidden root /mnt
exit 1
$ TMPDIR=$SCRATCH/guard/ok ./copy.test           → PASS
```

Noticed, not acted on (PM to triage):
- While tracing F5: `inventory`, `move`, `symlinks`/`hardlinks`, `import-tags` **exit 0 with per-file errors** (counted in the summary only). Documented as-is in CLAUDE.md's last column. `nas-verify-certify-all.sh` doesn't call them, `nas-inventory.sh` does call `inventory` - a walk-level error is 1, a per-file hash error is 0. Same class of gap F2/F3 closed for `copy`; proposing a backlog item, not touching it.
- `scan`'s F5 row is honest but thin: it has no non-zero for "collisions predicted". By design (it only reports); noting so nobody reads the table as a bug list.

**Dev (2026-09-16T12:18-07:00) - rev 1 done. READY FOR REVIEW.** Worktree `~/Projects/media-vault-dev` still live but idle; **code tip `14f4e2c`** on `tests-and-pinning` (3 commits off `main` @ `5a92286`, fast-forwardable; this note is one further commit touching only `team/`). Not pushed - PM's call. **Rung: `tested`** (author ceiling). `verify-incremental` and `internal/verify` untouched.

Commits, in work-order order:
1. `e38449f` **B3** - `runCopy` returns `int`; `main` does `os.Exit(code)` when non-zero. Same codes as before (0/1, usage still 2 via the existing path); that is the entire refactor. 29 test cases across `cmd/vault`, `internal/{copy,scan,certify}`. `cmd/vault` round-trip re-execs the test binary through `main()` (`VAULT_TEST_MAIN=1`) for real exit codes; F3 cases call `runCopy` in-process. `TestMain` refuses to run if `TMPDIR` is under `/volume1` or `/mnt`; every path handed to the CLI comes from `t.TempDir()` through a guard, `VAULT_CONFIG` and cwd pinned per subprocess so the `./vault-config` fallback is unreachable.
   - F3 "done when", each an explicit case: unresolved collision (skip, and rename still blocked) → 1 with `1 file(s) skipped on unresolved destination collisions` in `INCOMPLETE:`; every-rename-succeeds → 0 (and the row points at `only_2023.mov`, original untouched); dry-run with collisions → 0, zero files, zero rows; no-op → 0; F2 failed → 1 naming `failed to copy`; F2 orphaned → 1 naming both reasons.
   - Round-trip: scan(3) → copy → verify (3/0/0/0) → certify → `certify.Verify` OK on the JSON; certify exit 1 before verify; recopy on size+mtime change AND on mtime-only change; certify exit 1 again after a recopy until re-verified; corrupted dest → verify exit 1 `MISMATCH`, certify refuses naming `"mismatch"`; deleted dest → verify exit 1 `Missing: 1`.
   - `.vault-partial` gone after short write, cancelled copy, missing source. `certify.Build` refuses on each of `copied|mismatch|deduped|inventoried` without creating a key.
   - **Mutation check:** reverting the predicate to `failed > 0 || orphaned > 0` fails 3 tests (`TestCopyExitStatus/unresolved_collision…`, `…/rename-mtime-year_still_blocked…`, `TestCollisionRowsAndExitThroughMain`). Restored; the committed tree is the passing one.
2. `bec13cc` **B5** - `CLAUDE.md` (89 lines): build/test, package map, status vocabulary, exit-code table, announce-what-you-skipped, hard rules, branch/review/deploy.
3. `14f4e2c` **B4** - all five `scripts/*.sh`: `IMG="${VAULT_IMAGE:-ghcr.io/eddyvarelae/media-vault:v0.2.0}"` with the "v0.2.0 does not exist until the PM tags it" comment; `docs/release.md` (5 numbered lines); CLAUDE.md deploy line updated in the same commit. `bash -n` clean on all five; the IMG line evaluates to the default with `VAULT_IMAGE` unset and to the override when set.

Evidence (Mac mini, `2026-09-16T12:17:44-07:00`, go1.27.0 darwin/arm64):
```
$ go vet ./...
(clean)
$ go test ./... -count=1
ok  	github.com/eddyvarelae/media-vault/cmd/vault	0.498s
ok  	github.com/eddyvarelae/media-vault/internal/certify	0.479s
ok  	github.com/eddyvarelae/media-vault/internal/copy	0.619s
?   	github.com/eddyvarelae/media-vault/internal/dedup	[no test files]
?   	github.com/eddyvarelae/media-vault/internal/importer	[no test files]
?   	github.com/eddyvarelae/media-vault/internal/inventory	[no test files]
?   	github.com/eddyvarelae/media-vault/internal/manifest	[no test files]
?   	github.com/eddyvarelae/media-vault/internal/move	[no test files]
ok  	github.com/eddyvarelae/media-vault/internal/scan	0.780s
?   	github.com/eddyvarelae/media-vault/internal/verify	[no test files]
$ CGO_ENABLED=0 go test ./... -count=1     # same 4 ok, constraint holds
```

Noticed, not acted on (out of rev-1 scope - PM to triage):
- `gofmt -l` flags three pre-existing files: `internal/certify/certify.go`, `internal/importer/importer.go`, `internal/inventory/inventory.go`. Whitespace only; I left them so the diff stays reviewable. One-line item for any PR.
- `docs/install/ugos.md` lines 23/36 still tell a UGOS-UI installer to use tag `latest`. Doc-only; same spirit as B4. Not in the five-scripts scope, so untouched.
- `main`'s `defer m.Close()` is skipped whenever a command exits non-zero (`os.Exit` bypasses defers) - pre-existing, unchanged by the refactor, harmless with WAL. Mention only so nobody reads it as new.
- `internal/manifest` and `internal/verify` have no tests; `verify` is frozen, `manifest` is covered indirectly by every round-trip. B9 territory.

**PM (2026-09-17):** Rev 1 accepted at `tested`. PM re-ran `go vet` + `go test ./... -count=1` at `a2da6a3`: 4 packages ok - your evidence reproduces. Staged as review #2; Codex is on it. Do not touch `tests-and-pinning` until the verdict lands (frozen). Your four "noticed" items are triaged: gofmt → B15; ugos.md `latest` → folded into B16; `defer m.Close()` skipped on `os.Exit` → noted in BACKLOG as no-action; manifest/verify tests → B9 (after F4 lands on `main`). F4 is Reviewer-APPROVED (#1); its merge to `main` is waiting on the human, so B9 is not yet startable. Rev 2 below is what you can do now.

## Questions

(none yet)

## Superseded

(none)
