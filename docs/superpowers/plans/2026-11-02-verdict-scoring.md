# Verdict Scoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A read-only script that scores every shadow brief's `page`/`hold` verdict against what humans did in IRM, so the owner can run the Phase 1 exit test (production-readiness A2) before acks go live.

**Architecture:** `alert-relay/score-verdicts.sh` (bash + jq, like the poller) parses the briefs log, fetches each brief's IRM alert group by id with GETs only, classifies each brief, and prints a per-brief table plus a summary. It never writes anywhere. A stub-API test pins its parsing and classification.

**Tech stack:** bash, jq 1.7+ (CI) / 1.8.1 (host), curl. No new dependencies.

**Spec:** `docs/production-readiness.md` A2: "across all paging alerts in at least two weeks of shadow, zero `hold` verdicts on alerts that needed someone before morning. Record the false-page rate."

## Global Constraints

- Read-only: only HTTP GET to `${IRM_API_URL%/}/api/v1/alert_groups/<id>`. No file writes outside a temp dir the script removes.
- Auth exactly like `slack-agent/internal/irm/client.go` `do()`: a token starting `glsa_` is sent as `Authorization: Bearer <token>` plus `X-Grafana-URL: $GRAFANA_URL`; any other token is sent bare as `Authorization: <token>`. Env: `IRM_API_URL` (required), `IRM_TOKEN` (required), `GRAFANA_URL` (optional; required when the token starts `glsa_`).
- The briefs log is the poller's own (`$STATE_DIR/briefs.log`, written by `alert-relay/poll.sh` `log()`): entries start with `=== <ts> <LABEL> #<ref> <id>`; skip lines start with `--- ` and are ignored. The real log contains NUL bytes (1,166 of them in one block on the host); strip them before parsing. A brief can contain more than one `Verdict:` line; the last one counts (same rule as `emit_event`'s `field()`).
- jq filters must parse on jq 1.7: parenthesize any expression used as an object value (CI failed on this once).
- Output may contain internal alert titles and links. It goes to the operator's terminal only; never commit real output.

## Definitions (owner to confirm)

- **Human acted:** the alert group's `acknowledged_by` or `resolved_by` is non-null. IRM sets them to a user id; a source auto-resolve leaves `resolved_by` null (checked on live data 2026-11-02). While `ACK_DRY_RUN=1` no ack comes from the copilot, so every non-null `acknowledged_by` is a human.
- **Night:** the alert group's `created_at` falls between 22:00 and 08:00 America/Los_Angeles.
- Categories per brief:
  - `MISS?`: verdict `hold` and a human acted. A candidate miss; the owner judges each one.
  - `hold-ok`: `hold`, resolved, nobody acted.
  - `page-ok`: `page` (or no verdict), and a human acted.
  - `false-page?`: `page`, resolved, and nobody acted.
  - `open`: not resolved and nobody acted yet.
  - `unknown`: the lookup failed or the group is missing. Never counted as ok.
- Summary: the count per category; the false-page rate = `false-page?` / (`page-ok` + `false-page?`); the same counts for night briefs only; the full `MISS?` list with ref, time, minutes to first human action, title and link.

## Review Focus

1. A failed lookup (HTTP 4xx/5xx, timeout, non-JSON) must show as `unknown`, never as `hold-ok` or `false-page?`, or the exit test passes on missing data.
2. A brief whose verdict line is missing or garbled reads as `page`, matching `investigate.sh` (`page - no verdict given`), so a broken brief can't hide among `hold-ok`.
3. The NUL block and a header that shares a line with NULs: the entry still parses, or is reported as unparseable, never silently dropped. The summary prints the number of entries parsed and the number of `=== ` headers seen, so a drop is visible.
4. Time math at the night boundaries (22:00, 08:00 PT) and across the DST change on 2026-11-01.
5. Too many lookups: one GET per brief (126 today); keep it sequential with `curl -m 20` and no retries, so a slow API makes the run slow, not wrong.

---

### Task 1: The scoring script and its test

**Files:**
- Create: `alert-relay/score-verdicts.sh`
- Create: `tests/score_verdicts_test.sh`
- Modify: `.github/workflows/ci.yml:30` (add `- run: ./tests/score_verdicts_test.sh` to the `investigate` job)

**Interfaces:**
- Produces: `score-verdicts.sh [briefs-log]` (default `${STATE_DIR:-$HOME/.local/state/oncall-copilot}/briefs.log`). Stdout: one tab-separated row per brief with columns `ts ref id verdict night acted min_to_action state category title url`, then a blank line and the summary. Exit 0 on success; exit 1 with an `error:` line if the log is missing or `IRM_API_URL`/`IRM_TOKEN` are unset. Per-brief lookup failures don't change the exit code (they're `unknown` rows).

- [ ] **Step 1: Write the failing test**

Follow `tests/irm_poll_test.sh`'s style: `check`/`lacks` helpers and a python stub server on a free port that serves `GET /api/v1/alert_groups/<id>` from `$TMP/groups/<id>.json` (404 if missing), returning 401 unless the auth header is right. Run the stub once in each auth mode. Fixture log with:
- H1: `hold`, a human acked, created at 02:00 PT -> `MISS?`, night yes
- H2: `hold`, auto-resolved (`resolved_by` null) -> `hold-ok`
- P1: `page`, resolved by a human -> `page-ok`
- P2: `page`, auto-resolved, nobody acted -> `false-page?`
- P3: no `Verdict:` line -> treated as `page`
- P4: two `Verdict:` lines, `hold` then `page` -> `page`
- O1: `page`, still firing, nobody acted -> `open`
- U1: id with no stub file (404) -> `unknown`
- a block of NUL bytes between two entries, and one `--- ` skip line (ignored)

Assertions: each row's category; `MISS?` lists H1 with its link and minutes to action; false-page rate `1/2`; the header count equals the parsed count; Bearer + X-Grafana-URL sent for a `glsa_` token and the bare header otherwise; missing `IRM_TOKEN` exits 1 with `error:`; the script makes no request other than GET.

- [ ] **Step 2: Run it**

Run: `./tests/score_verdicts_test.sh`
Expected: fails, because the script doesn't exist.

- [ ] **Step 3: Implement `alert-relay/score-verdicts.sh`**

Strip NULs with `tr -d '\000'`, split entries on `^=== `, take the last `^Verdict: ` word (lowercased; anything but `hold` counts as `page`), fetch each group, and classify with one jq program per group. Compute night and minutes with jq's date functions or `date -d`, converting with `TZ=America/Los_Angeles`.

- [ ] **Step 4: Run it and the existing suites**

Run: `./tests/score_verdicts_test.sh && ./tests/irm_poll_test.sh && ./tests/investigate_test.sh && ./tests/query_scripts_test.sh`
Expected: all pass.

- [ ] **Step 5: Commit**

`git commit -m "Add a read-only verdict scorer for the shadow briefs"`

### Task 2: Document how to run it

**Files:**
- Modify: `docs/real-oncall-status.md` (Open item 1: the command, what each category means, and that `MISS?` rows need the owner's judgment)
- Modify: `docs/production-readiness.md` (A2: point to the script)

- [ ] **Step 1:** Write it. Usage on the host, as the owner: `set -a; . ~/.config/oncall-copilot/agent.env; set +a; IRM_TOKEN=$IRM_PERSONAL_TOKEN bash alert-relay/score-verdicts.sh /var/lib/oncall-copilot/.local/state/oncall-copilot/briefs.log`. Name no host, cluster or ID.
- [ ] **Step 2:** Commit `Document the verdict scorer`.

### Task 3: First scoring run (read-only, on the host)

- [ ] **Step 1:** After merge, run the Task 2 command on the host over the whole log. Expected: headers seen == entries parsed, `unknown` == 0 (or each one explained).
- [ ] **Step 2:** Give the owner the summary and every `MISS?` row with its link. The owner judges each: did it need someone before morning? Record the false-page rate as the Stage B baseline.
- [ ] **Step 3:** The A2 exit test also needs two weeks of shadow. The window started 2026-10-25, so the earliest pass is 2026-11-08. Rerun then.
