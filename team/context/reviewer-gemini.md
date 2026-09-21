# Second Reviewer: Gemini CLI (drafted 2026-09-20 22:02, PM) - numbers first, code only after calibration

Why: Codex on the subscription gives ~12 reviews per 5-h window and a weekly cap (hit 2026-09-19 16:39 → Tue 09-22 12:21). Eddy chose Gemini CLI as a second non-Claude reviewer (2026-09-20 22:0x). A local 7B model was tried and ruled out (DECISIONS 2026-09-20: it approved with invented numbers).

## Install / auth
- `npm install -g @google/gemini-cli` → `gemini` 0.60.0 at `/opt/homebrew/bin/gemini` (installed 2026-09-20 22:01 by the PM).
- Auth is **Eddy's**: the Google-login path is dead for individuals in this client ("migrate to Antigravity"), so it is an **AI Studio API key** (new format, prefix `AQ.`, 53 chars, project 672992288007) in `~/.gemini/.env` as `GEMINI_API_KEY=…`, mode 600, written from the clipboard with `pbpaste` (a `read -s` paste got mangled by bracketed-paste escapes). `~/.gemini/settings.json` has `security.auth.selectedType = "gemini-api-key"` (the PM set it; the default `oauth-personal` fails). No agent prints that file; the PM checks only prefix + length. Validated 2026-09-20 22:24: models endpoint 200, headless `-p` reply OK.

## Invocation (PM only, one at a time, never against the live checkout)
```
S=<scratchpad>/review-copy            # a throwaway clone of the repo at the reviewed SHA
cd $S && git fetch -q origin && git checkout -q <main-sha>
GEMINI_CLI_TRUST_WORKSPACE=true gemini -m gemini-3.8-flash --sandbox --approval-mode yolo -p "<the request text + the same rules Codex gets>" </dev/null > <scratch>/reviewN-gemini.log 2>&1
```
- `--sandbox` on macOS = Seatbelt: writes confined to the working dir (the throwaway clone), reads open (it needs `/Volumes/Scratch1/tester/*` and `~/mounts/docker/vault-certs/*`). `--approval-mode yolo` lets it run `python3`/`sqlite3`/`git diff` without prompts. Never run it in `~/Projects/media-vault`.
- `GEMINI_CLI_TRUST_WORKSPACE=true` is required in headless mode (otherwise it refuses an untrusted directory). No `timeout` binary on macOS: cap with `perl -e 'alarm N; exec @ARGV' gemini …`. `ripgrep` is not installed (it falls back to its own grep; fine).
- `--approval-mode plan` is read-only and cannot run the recount commands, so it is not enough for a Reviewer.
- The verdict is whatever it prints last; the PM records it in `review-requests.md` as **`Reviewer (Gemini, <ts>)`** so the record shows which reviewer said what.

## Calibration (must pass before any verdict counts)
1. Numbers: re-run a **closed** numeric request whose artifacts still exist and whose recorded verdict is known - #83 (kipp copy: `kipp-live/run3.log`, `b24-live/manifest-after.db` before, `kipp-live/manifest-after.db` after). Pass = it recomputes 9,872 / 521 / 5,012 / 78,128 = 67,735 + 10,393 with 0 pre-existing rows changed, by actually running the commands (the log must show them). Any invented number = fail, ruled out like the local model.
2. Code: re-run a closed code request with a recorded FINDINGS - #79 (kipp-fixes `f7edbfe`: the P1 "absent foreign owner masks a present one" at `scan.go:410/:443`). Pass = it finds that defect (or a strictly stronger one) without being told. Until it passes this, Gemini holds **numeric verdicts only**; code reviews stay with Codex.

## Calibration record
- **Cal 1 (numbers) PASSED 2026-09-20 22:30** on `gemini-3.8-flash`: re-review of #83 - it ran `grep` on `run3.log` and a python `sqlite3` script with `mode=ro&immutable=1`, and reproduced every number (shas, 67,735 → 78,128, 0 pre-existing rows changed, per-disk statuses, 5,012 `_2026`) **and** the same single finding Codex recorded (7 `never overwritten` summary lines vs the PM's "0"). Log `scratchpad/cal1-gemini.log`.
- **Cal 2 (code) NOT RUN** - free-tier daily quota (20 requests/day/model) exhausted; `gemini-3.1-pro-preview` has **zero** free quota. Needs billing on project 672992288007. Until it passes, Gemini holds numeric verdicts only.
- Model facts 2026-09-20 22:30: `gemini-2.5-pro` retired for new keys; use `gemini-3.8-flash` (free, calibrated for numbers) or `gemini-3.1-pro-preview` (billing). Free tier = 20 req/day/model - one review is ~10-20 requests, so budget one review per model per day without billing.

## Scope once calibrated
- Numbers (Tester witness recounts, rule-8 claims): Gemini or Codex, whichever has budget.
- Code: Codex; Gemini only if calibration 2 passed, and then Codex re-reviews anything that ships a release tag when it has budget.
