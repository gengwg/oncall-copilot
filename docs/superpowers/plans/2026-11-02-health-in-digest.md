# Health Line in the Daily Digest Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The owner learns within a day when either service's heartbeat was held or missed, without anyone being paged: the 08:05 PT digest DM gets a health line, and posts even on a day with no off-shift briefs when there is a problem.

**Architecture:** The poller appends one line to `$STATE_DIR/health.log` whenever a run fails, its ping fails, or its ping is held, and touches `$STATE_DIR/last-ok` after each successful ping. The agent, when the digest is due, reads those two files from the poller's state dir (group-readable to the owner) and counts its own failed pings since the last digest. Any problem in the past 24 hours becomes one line per service at the top of the digest.

**Tech stack:** bash (poller), Go stdlib (agent). No new dependencies.

**Spec:** `docs/production-readiness.md` A7: "Keep heartbeats wakeless. Add a line to the 08:05 digest DM when either heartbeat was held or missed in the past 24 hours. Exit: stop the poller for an hour, and the next digest says so."

## Global Constraints

- Heartbeats stay wakeless: no new pages, no new IRM writes, no change to when either service pings.
- No regression: every existing test in `tests/irm_poll_test.sh` and `slack-agent/...` passes with its expected strings unchanged; the digest's existing text for off-shift briefs is byte-identical when there is no health problem.
- Fail visibly: if the agent cannot read the poller's files, the digest says "poller health unknown: <reason>" rather than nothing.
- Poller state files stay bounded: `health.log` is trimmed to its last 3000 lines on every write. A held or failing poller writes one line per run, about 1440 a day, so 500 lines would lose the day.
- `health.log` line format: `<UTC RFC3339>\t<kind>\t<detail>`, kind one of `poll-failed`, `ping-failed`, `held`, `gap` (written on the first successful ping after more than 10 minutes without one, with the gap's length and start). Details never contain the heartbeat URL (it holds a secret).
- jq, if used, must parse on jq 1.7.

## Review Focus

1. The poller's early `exit 1` paths (unknown adapter, adapter failure, non-JSONL output) must each write a `poll-failed` line before exiting, without changing their stderr text or exit code.
2. A stopped poller writes nothing while it is stopped. Two signals cover it: a `gap` line written when it comes back (a past outage, which is the A7 exit test), and staleness at digest time (`last-ok` older than 10 minutes means it is down right now).
3. Quiet days: no off-shift briefs and no problems -> no DM (unchanged); no briefs but a problem -> a health-only DM; the digest marker is written either way so it posts once per day.
4. The agent restarting resets its in-memory failed-ping count; that undercounts, never overcounts. Say so in a comment, don't persist it.
5. A `health.log` line with a future or unparseable timestamp is ignored for the 24-hour window but doesn't crash the digest.

---

### Task 1: Poller records its health

**Files:**
- Modify: `alert-relay/poll.sh` (the `heartbeat()` function at ~line 60, the exits at ~lines 32, 74, 75, and the bounding block at the end)
- Modify: `tests/irm_poll_test.sh` (new cases after the last one)

**Interfaces:**
- Produces: `$STATE_DIR/health.log` (format above) and `$STATE_DIR/last-ok` (mtime = last successful ping; contents the UTC RFC3339 time). Consumed by Task 2.

- [ ] **Step 1: Failing tests.** New cases: (a) a successful poll with `HEARTBEAT_URL` set updates `last-ok` and writes no `health.log` line; (b) a refused token (`GRAFANA_TOKEN=wrong`) writes one `poll-failed` line containing `HTTP 401` and leaves `last-ok` unchanged; (c) an unreachable heartbeat URL writes one `ping-failed` line; (d) two degraded briefs in a row write a `held` line on the second run; (e) an unknown `ONCALL_PAGER` writes `poll-failed`; (f) with `last-ok` set 30 minutes in the past (`touch -d`), a successful ping writes one `gap` line naming about 30 minutes; with it set 5 minutes in the past, no `gap` line; (g) `health.log` is trimmed to 3000 lines; (h) no line ever contains the heartbeat URL. Each line matches `^[0-9T:-]+Z\t(poll-failed|ping-failed|held|gap)\t`.
- [ ] **Step 2: Run** `./tests/irm_poll_test.sh` — new cases fail, all old ones pass.
- [ ] **Step 3: Implement** a `health_event <kind> <detail>` helper (create `$STATE_DIR` first if needed; append one line), call it at each exit and in `heartbeat()`, after a successful `curl`, write a `gap` line if the old `last-ok` is more than 10 minutes old, then write `last-ok`, trim with the other state files.
- [ ] **Step 4: Run** all four shell suites — pass, old expected strings unchanged.
- [ ] **Step 5: Commit** `Record poller health for the daily digest`.

### Task 2: The digest reports health

**Files:**
- Modify: `slack-agent/internal/agent/agent.go` (`digest`), `slack-agent/internal/agent/agent_test.go`
- Modify: `slack-agent/main.go` (count failed pings), `slack-agent/internal/config/config.go` (+ test)
- Create: `slack-agent/internal/health/health.go`, `health_test.go`

**Interfaces:**
- Consumes: Task 1's files.
- Produces, in package `health`:
  ```go
  // Poller summarizes the poller's health files for the 24 hours before now.
  // It returns "" when there was no problem.
  func Poller(dir string, now time.Time) string
  ```
  Returns e.g. `poller: 3 failed polls, 1 failed ping, heartbeat held 2x, 1 gap (longest 62 min) in the last 24h (last: <ts> poll-failed HTTP 401)` and/or `poller: no successful ping since <ts>` when `last-ok` is older than 10 minutes, or `poller health unknown: <err>` when `last-ok` can't be read.
- `Agent` gains `PollerStateDir string` and `FailedPings func() int` (returns and resets the agent's failed-ping count; nil means none). `digest` prepends the poller line and `agent: N failed heartbeat pings since the last digest` when non-zero, posts a health-only DM when there are no briefs but a line exists, and keeps today's text unchanged otherwise.
- Config: `POLLER_STATE_DIR`, default `/var/lib/oncall-copilot/.local/state/oncall-copilot`.

- [ ] **Step 1: Failing tests.** `health_test.go`: empty dir -> "health unknown"; fresh `last-ok`, empty log -> ""; stale `last-ok` (11 min) -> "no successful ping since"; log lines inside and outside 24h counted correctly by kind, including `gap` with its longest length; a garbled or future line is ignored. `agent_test.go`: digest with briefs and no problem -> byte-identical to today's text (reuse the existing digest test's expectation); briefs plus a problem -> health line first, then today's text; no briefs plus a problem -> one health-only DM; no briefs, no problem -> no DM; marker written in each case. Config test: default and override for `POLLER_STATE_DIR`.
- [ ] **Step 2: Run** `cd slack-agent && go test ./...` — fail (missing package/fields).
- [ ] **Step 3: Implement.** In `main.go`, an atomic counter incremented where `heartbeat ping failed` and `heartbeat ping: HTTP %d` are logged, exposed as `FailedPings` (swap to 0).
- [ ] **Step 4: Run** `test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...` — pass; existing digest tests unchanged.
- [ ] **Step 5: Commit** `Report poller and agent heartbeat problems in the daily digest`.

### Task 3: Docs and the exit test

- [ ] **Step 1:** `docs/real-oncall-status.md`: one line in "Decisions" or "Heartbeats" that the digest now carries a health line; `deploy/systemd/README.md` Heartbeats section: same, plus `POLLER_STATE_DIR`; `docs/production-readiness.md` A7: point to it. Commit `Document the digest health line`.
- [ ] **Step 2 (after merge, owner):** deploy (agent first, then poller). Exit test: `sudo systemctl stop oncall-copilot-poll.timer` for an hour in a quiet period, start it again, and check that the next 08:05 digest reports the gap (about 60 minutes).
