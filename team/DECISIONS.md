# Decisions log (append-only)

Every decision the human makes - in any session, verbally or in writing - gets appended here BY THE AGENT WHO RECEIVED IT, before acting on it. No agent may assume the others heard it.

Format: `- YYYY-MM-DD · **decision** · context/why · received-by: {role}`

- 2026-09-15 · **`copy` fails the run on an unresolved collision even when `--dedupe-content` archived the bytes under another name** · the missing manifest row is itself the gap; exit code must not depend on an unrelated flag · received-by: (pre-framework, recorded in HANDOFF-f3-f4 by the then-reviewer; logged here by pm)
- 2026-09-15 · **Fail-on-collision only when the content is archived elsewhere: rejected** · same reasoning; do not relitigate · received-by: pm (from handoff)
- 2026-09-16 · **Adopt the team framework in media-vault; this session is the PM (Fable)** · `team/` copied from ~/Projects/team-framework · received-by: pm
- 2026-09-16 · **Reviewer = Codex CLI (`/opt/homebrew/bin/codex`), never a Claude session** · framework rule; the previous "MEV - Reviewer" seat is retired · received-by: pm
- 2026-09-16 · **F4 (`verify --only-unverified`, branch `verify-incremental`) goes through the Reviewer before merge** · first item in `channels/review-requests.md` · received-by: pm
- 2026-09-16 · **Immediate goal is operational: re-check all 4 source SSDs against the NAS, then certify** · README roadmap features go to P2 until the archive is certified · received-by: pm
- 2026-09-16 · **The 5th SSD is now "Scratch1", extra storage for the Mac mini - no longer a source disk** · whether it was certified before repurposing is unknown; Tester to check the manifest · received-by: pm
- 2026-09-16 · **Boot the Tester now** · product has live side effects on production data · received-by: pm
- 2026-09-16 · **NAS scripts pin a release tag instead of `:latest`** · a merge to main must not be a deploy · received-by: pm
- 2026-09-16 · **Test coverage is P1** · zero test files on code that decides whether footage is safe to wipe · received-by: pm
- 2026-09-16 · **All seats (PM, Dev, Tester) run on the Mac mini** · it is on ethernet next to the NAS; the MacBook is not a seat host · received-by: pm
- 2026-09-16 · **Audience: Eddy's archive first, public-quality docs second** · received-by: pm
- 2026-09-16 · **`HANDOFF-f3-f4.md` moves under `team/archive/`; `team/` is committed on `main` by the PM** · received-by: pm
- 2026-09-17 · **The PM runs the Reviewer (codex) and boots every seat itself; the human is not asked to run agents** · received-by: pm
- 2026-09-17 · **Push `main` (team framework commit)** · received-by: pm
- 2026-09-17 · **NAS access method is unknown to the human** · PM/Tester discover it; the human will be asked only for a concrete enable-step or credential · received-by: pm
