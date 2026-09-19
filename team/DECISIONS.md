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
- 2026-09-17 · **media-vault owns every scheduled batch against the archive, the nightly video tagger included; mini-server is only the machine** · handed over in `team/archive/2026-09-17-notes-from-mini-server-pm.md` (received via Eddy) · received-by: pm (relayed from the mini-server PM's record of Eddy's decision)
- 2026-09-17 · **Tagger batch rules (travel with the job): newest first over the whole archive (`copied_at` DESC, no watermark); 200 GB/night cap until a Sony night is timed; no folder skipped, six camera folders first then Backup/LeanTank/Public; `#recycle` never a source; the five GoPro test clips stay tagged** · same source · received-by: pm (relayed)
- 2026-09-15 · **`run-backup.sh` is report-only: on attach of a known external SSD run the content gap check, write a report, copy nothing; attach detection by `StartInterval` polling** · same source · received-by: pm (relayed)
- 2026-09-17 · **`case` SSD is certified and cleared to wipe; only Eddy wipes** · same source · received-by: pm (relayed)
- 2026-09-17 · **Retire mini-server's WO-1 part A over media-vault; Eddy tells that window** · received-by: pm
- 2026-09-17 · **`NAS_SHARES="media docker"` set in mini-server's `config/mini.env`** (was `"media"`) · the `docker` share mounts on the Mini within 5 min · received-by: pm
- 2026-09-17 · **SSH on the UGREEN NAS is enabled** · user/sudo/docker details still to be confirmed by the Tester · received-by: pm
- 2026-09-17 · **The PM merges F4 to `main` and pushes; a permission rule lets the PM merge/push on this repo from now on** · every merge still requires a Reviewer APPROVE in the file · received-by: pm
- 2026-09-17 · **Restart the Mac mini tonight for updates, then leave it running all night; resume with a fresh PM session and re-booted seats** · all pending items documented before the restart · received-by: pm
- 2026-09-17 · **All source SSDs are attached to the Mini (`tars`, `kipp`, `case`, `Eddy's Media Vault`, `Scratch1`); agents read them only - never wipe, format, or edit** · Eddy, after the restart; `noahsarc` is not mounted under that name · received-by: pm
- 2026-09-17 · **SSH key installed on the NAS for `figmaboi`; Eddy logged in successfully** · unblocks B2 · received-by: pm
- 2026-09-17 · **The Tester runs NAS commands as `figmaboi`, read-only - it may not delete anything** · anything that writes the manifest or the archive stays with Eddy · received-by: pm
- 2026-09-17 · **The 2026-09-13 verify pass: Eddy does not know who ran it** · stays unattributed; treated as a real pass with a real result (1 failure, sonya6700) · received-by: pm
- 2026-09-17 · **A `verified` destination is never overwritten by `copy`: same `(source_disk, source_path)` with different content is a collision - renamed under `--on-collision rename-mtime-year`, otherwise skipped, counted in `INCOMPLETE:`, exit 1; the old row keeps its hash and status** · B23(b), the defect behind the 2,668 lost photos; Dev rev 4 item 1 builds exactly this · received-by: pm
- 2026-09-17 · **The 2026-04-26 SonyA6700 source disk is unknown to Eddy; every disk he owns is attached to the Mini; there are no unformatted A6700 cards** · with all five attached SSDs searched (0 of 2,668) and `noahsarc` erased, **B23 recovery is exhausted: the 2,668 April photos (51.1 GB) are lost**; list kept at `team/archive/2026-09-01-sonya6700-lost-2668.tsv` · received-by: pm
- 2026-09-17 · **`scan` excludes `reports/` directories (tagger output) instead of inventorying them** · PM recommendation adopted by Eddy; the tagger itself still runs over every camera folder · received-by: pm
- 2026-09-17 · **Goal after the overwrite fix: run media-vault against all the SSDs, which are attached to the Mac mini (not the NAS)** · sets the next milestone after B6; how the Mini reaches the NAS manifest safely is the PM's design question (B29) · received-by: pm
- 2026-09-17 · **Mini-attached SSDs are for the read-only gap check only (which SSDs still need archiving); any SSD that needs copying gets plugged into the NAS directly and the run happens there** · replaces the B29 design question; nothing on the Mini ever writes the manifest · received-by: pm

## 2026-09-18 17:57 - Eddy's answers to the standing DECISIONS list (PM logged)
- **kipp → NAS:** Eddy will plug `kipp` into the NAS after first copying one folder from it to `Scratch1` as a precaution (~21 min, human action). Agents do not touch `kipp` while that runs.
- **tars as a dump disk Apr–Jul:** unknown. Default: treat every one of the 5,040 gap files as a candidate original; the copy's overwrite guard and dedup decide per file. No policy change.
- **NAS activity Apr 27/28 (B39, 605 empty-dest rows):** unknown. Default: rows stay as they are; `verify` reports them as missing and the repair path (B24-style) decides later. Nothing is deleted.
- Items 3 and 5 re-asked in plain words (Eddy asked what a dry-run and B24 are).
