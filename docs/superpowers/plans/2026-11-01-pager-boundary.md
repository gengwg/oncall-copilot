# Pager Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put Grafana IRM behind one pager boundary in the poller and in the Slack agent, so another paging provider is one adapter on each side, with no change in behavior for IRM.

**Architecture:** The poller splits into a provider-neutral core (`alert-relay/poll.sh`: dedup, storm caps, severity filter, investigate, outbox, heartbeat) and one fetch-and-normalize adapter per provider (`alert-relay/pagers/irm.sh`). The outbox event and the alert line get neutral keys (`pager`, `id`, `ref`, `url`). The agent depends on a `Pager` interface with IRM as its only implementation. For one release both sides also write and read the old `irm_*` keys, so the poller and agent can be deployed or rolled back in either order.

**Tech stack:** bash + jq (poller), Go stdlib + `slack-go/slack` (agent). No new dependencies.

**Spec:** this conversation's design (2026-11-01): rename the fields to neutral names, add the boundary on both sides, keep IRM as the only adapter, and ensure no regression.

## Global Constraints

- **No regression for IRM.** Every user-visible string stays byte-identical: briefs log lines (`IRM #N ...`), DM text, ack notes, digest lines, the DC prompt, DC draft filenames.
- **Existing tests are the regression contract.** In `tests/*.sh` and `*_test.go`, a changed line may rename a field, constructor or file path. It must not change an expected string or assertion, except the lines listed under "Allowed test edits" in each task.
- **Persistent state on the host stays readable with no migration:** the poller's `seen`, `storm`, `degraded` and `degraded-streak` files, the agent's `events/*.json` records, `dc-requests.tsv`, and outbox files.
- **No env var renames.** `IRM_API_URL`, `IRM_PERSONAL_TOKEN`, `IRM_SCHEDULE_ID`, `GRAFANA_URL` and `GRAFANA_TOKEN` keep their meaning. New: `PAGER` (default `irm`) on both sides. (Changed during implementation to `ONCALL_PAGER` and `ONCALL_PAGER_ADAPTER`, because `PAGER` is the terminal-pager variable.)
- **The write allowlist stays a transport in the IRM client.** No code path may gain a write.
- **Label map:** `irm` -> `IRM`; any other pager's label is its name. It lives in exactly two places, `pager_label` in `poll.sh` and `pager.Label` in Go, and both are tested.
- Each task's final check is the full CI set: per Go module `test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...`, plus the three `tests/*.sh`.

## Review Focus

1. **Deploy skew.** A new poller writes events read by the old agent, or the reverse. The old agent requires `irm_id` (`outbox.go:36`) and acks `ev.IRMID`. Pinned by the dual-write tests in Tasks 2 and 3 and the legacy-read tests in Task 3.
2. **Re-investigating the backlog after deploy.** If ids or the `seen` format changed, the first poll would re-bill the model and DM every recent alert again. Pinned by Task 2's old-state test.
3. **Records the old agent wrote** must load, ack and reply as before. Pinned by Task 3's legacy record test, built from the current `Record` shape.
4. **The systemd unit still pointing at `irm-poll.sh`** after a partial deploy. The shim in Task 2 keeps it working, with its own test.
5. **The briefs log format** will be parsed by the verdict-scoring script (production-readiness A2). Pinned by Task 1's exact-line test.

---

### Task 1: Characterization tests on the current code

Pins today's exact behavior before anything moves. Every test here must pass on `main` before Task 2 starts.

**Files:**
- Modify: `tests/irm_poll_test.sh` (new case 13, before the summary at line 233)
- Create: `slack-agent/internal/agent/golden_test.go`
- Create: `slack-agent/internal/event/testdata/legacy-event.json`, `slack-agent/internal/event/testdata/legacy-record.json`

**Interfaces:**
- Produces: the two legacy JSON fixtures, used by Task 3, and the golden strings, which Tasks 3 and 4 must keep green unchanged.

- [ ] **Step 1: Snapshot the poller's line, event and log line (case 13)**

Use a group with state `firing` (real IRM reports `firing`, `acknowledged`, `resolved`; the existing fixtures' `new` is unrealistic but harmless). Bootstrap a fresh `STATE_DIR` first so the group counts as new. Assert three exact values, compared with `jq -S` so key order doesn't matter:
- the investigated line (last line of `$TMP/investigated`) equals exactly: `{status:"firing", alertname:"SnapX", severity:"critical", summary:"summary SnapX", description:"description SnapX", labels:{alertname:"SnapX", cluster:"c30", node:"gpu-9", severity:"critical", condition:"GpuXidCritical"}, starts_at:"2026-10-25T09:04:00Z", irm_number:501, irm_url:"https://slack.example/archives/C1/p501"}` (no `customer` label);
- the outbox event minus `ts` and `brief`, which must equal the current key set and values from `emit_event` (`irm-poll.sh:91-96`);
- the briefs log header line, matched with the regex `^=== [0-9T:-]+Z IRM #501 S1$`.

- [ ] **Step 2: Run it on main**

Run: `./tests/irm_poll_test.sh`
Expected: all previous checks plus the new ones pass. Save the event JSON it produced to `slack-agent/internal/event/testdata/legacy-event.json`, scrubbed only of the `ts` value (set it to `2026-10-25T09:05:00Z`).

- [ ] **Step 3: Golden agent strings**

In `golden_test.go`, using a fixture event with `IRMNumber: 501`, a Slack `IRMURL`, cluster `c30`, node `gpu-9`, verdict `hold`, fault `real` and a DC draft path, add:
- `TestRenderGolden`: `Render(ev)` equals a pasted exact string;
- `TestBuildPromptGolden`: `dcrequest.BuildPrompt(ev)` equals a pasted exact string, plus the same with `IRMURL` empty (the `IRM alert group #501` fallback);
- `TestAckNotesGolden`: the reply texts for an own-shift ack, an already-acked group, a `page` verdict, a manual `ack`, and a manual `unack`, driven through the existing test doubles in `agent_test.go`.
The expected values are whatever the current code prints: run once, paste, re-run.

- [ ] **Step 4: Legacy record fixture**

Write `testdata/legacy-record.json` as `json.MarshalIndent` of a current `event.Record` holding the legacy event, `SlackTS`, `PostedAt`, `AckDecided: true` and `AckedAt`. Add `TestLegacyFixturesDecode` in `internal/event` asserting that both fixtures unmarshal on current code with `IRMID == "S1"` and `IRMNumber == 501`.

- [ ] **Step 5: Run the full CI set**

Expected: all green on unchanged production code.

- [ ] **Step 6: Commit**

`git commit -m "Pin current IRM behavior with characterization tests"`

### Task 2: Split the poller into core and IRM adapter

**Files:**
- Create: `alert-relay/pagers/irm.sh` (moved from `irm-poll.sh:20` and `:56-67`, plus the group-to-alert mapping from `:104-127`)
- Create: `alert-relay/poll.sh` (the rest of `irm-poll.sh`)
- Modify: `alert-relay/irm-poll.sh` (becomes a three-line shim)
- Modify: `alert-relay/investigate.sh:380-395` (read `ref`, `url`, `label`)
- Modify: `deploy/systemd/oncall-copilot-poll.service:14` (`irm-poll.sh` -> `poll.sh`)
- Modify: `tests/irm_poll_test.sh` (run `poll.sh`; new cases 14-17), `tests/investigate_test.sh:514`

**Interfaces:**
- Adapter contract, used by future adapters: `pagers/<name>.sh`, no arguments, provider settings from env. On success it exits 0 and prints JSONL, oldest first, one object per alert group:
  `{"id":"<provider id>","ref":"<human number, string>","url":"<link or empty>","state":"firing|acknowledged|resolved|silenced","severity":"<group severity>","alert":{"alertname":..,"severity":..,"summary":..,"description":..,"labels":{..},"starts_at":".."}}`.
  `alert.labels` already has `customer` removed. On a failed fetch it prints one `error: ...` line to stderr and exits non-zero; empty output with exit 0 means no groups.
- `irm.sh` maps `id`, `alert_group_number` (as a string), `permalinks.slack // permalinks.web`, `state` (passed through), and severity exactly as `irm-poll.sh:111` and `:121-127` do today. Its error text stays `error: IRM alert_groups returned HTTP <code> from <IRM_API_URL>`.
- `poll.sh`: `PAGER` (default `irm`) selects `$(dirname "$0")/pagers/$PAGER.sh`; `PAGER_ADAPTER` overrides the path (tests). An unknown pager exits 1 with `error: no adapter for PAGER=<name>`. `pager_label irm` prints `IRM`, otherwise the name.
- New alert line, consumed by `investigate.sh`: the adapter's `alert` plus `status:"firing"`, `pager`, `label`, `ref`, `url`. `irm_number` and `irm_url` are dropped from the line, since the line never leaves one deploy.
- New outbox event keys: `pager`, `id`, `ref`, `url`, plus every current key. When `pager == "irm"` it also writes `irm_id` (= id), `irm_number` (= ref as a number) and `irm_url` (= url).

- [ ] **Step 1: Add the new failing cases**

- 14: the shim runs `poll.sh`. Running `irm-poll.sh` with the case-2 fixture produces the same briefs log line as `poll.sh`. (The installer copies the unit file on every run, `install.sh:26`, so the shim only matters if the unit and the clone get out of step.)
- 15: old state is kept. Write `$STATE_DIR/seen` the way the pre-split bootstrap did (`irm-poll.sh:71`: one IRM id per line) for three fixture groups, then run `poll.sh` on those groups. Expect no `bootstrapped` line and zero new lines in `$TMP/investigated`.
- 16: core with a fake adapter. `PAGER=fake PAGER_ADAPTER=$TMP/fake.sh`, where the fake prints one contract line with `id:"F1", ref:"77"`. Expect a briefs log `fake #77`, an event with `pager=="fake"`, `id=="F1"`, `ref=="77"` and no `irm_id` key, and the investigated line has `label:"fake"`.
- 17: unknown pager. `PAGER=nope` gives exit 1 and stderr `no adapter for PAGER=nope`.

- [ ] **Step 2: Run them**

Run: `./tests/irm_poll_test.sh`
Expected: 14-17 fail (`poll.sh` doesn't exist); 1-13 pass.

- [ ] **Step 3: Implement `pagers/irm.sh`, `poll.sh`, the shim, the unit line, and `investigate.sh`'s use of `ref`/`url`/`label`**

The draft filename stays `$date-${ref:+$label$ref-}$node.md`, so IRM still gets `IRM501-`. Point `run_poll` in the test at `poll.sh`.

- [ ] **Step 4: Run them**

Run: `./tests/irm_poll_test.sh && ./tests/investigate_test.sh`
Expected: all pass.

**Allowed test edits:** case 13's line snapshot replaces `irm_number:501, irm_url:...` with `pager:"irm", label:"IRM", ref:"501", url:"https://slack.example/archives/C1/p501"`. Case 13's event snapshot gains `pager`, `id`, `ref`, `url` with legacy keys unchanged. `investigate_test.sh:514` swaps `irm_number`/`irm_url` for `pager:"irm", label:"IRM", ref:"5157", url:...`. `run_poll` points at `poll.sh`. Nothing else.

- [ ] **Step 5: Full CI set, then commit**

`git commit -m "Split the IRM poller into a pager-neutral core and an IRM adapter"`

### Task 3: Neutral event, record, ledger and outbox in the agent

**Files:**
- Create: `slack-agent/internal/pager/pager.go`, `pager_test.go`
- Modify: `slack-agent/internal/event/event.go`, `internal/outbox/outbox.go:36,46`, `internal/ledger/ledger.go:18,39,65` and their tests

**Interfaces:**
- Produces, in package `pager`:
  ```go
  type Alert struct {
      ID, Ref, URL, State string // State "firing" means still paging
      Labels                     map[string]string
      StartsAt                   string // RFC3339 or ""
  }
  var ErrNotFound = errors.New("pager: alert not found")
  var ErrAlreadyAcked = errors.New("pager: already acknowledged")
  func Label(name string) string // "irm" -> "IRM", "" -> "IRM", else name
  ```
- `event.Event` replaces `IRMID`, `IRMNumber`, `IRMURL` with `Pager`, `ID`, `Ref`, `URL` (json `pager`, `id`, `ref`, `url`). Two methods:
  - `UnmarshalJSON` falls back to `irm_id`, `irm_number` (formatted with `strconv.Itoa`) and `irm_url` when the new keys are absent, and sets `Pager = "irm"` when absent.
  - `MarshalJSON` writes the new keys, plus the three legacy keys when `Pager` is `irm` (`irm_number` from `strconv.Atoi(Ref)`, omitted if not numeric).
- `event.Record.IRMID` becomes `ID` (json `id`). It reads `irm_id` as a fallback and also writes `irm_id`. `Store.Save` errors on an empty `ID` with `record without id`.
- `ledger.Entry.IRMID` becomes `ID`. The TSV columns don't change.

- [ ] **Step 1: Write the failing tests**

- `TestLabel` (`irm`->`IRM`, `""`->`IRM`, `pagerduty`->`pagerduty`).
- `TestEventLegacyDecode` (`testdata/legacy-event.json` gives `ID=="S1"`, `Ref=="501"`, `Pager=="irm"`, `URL` set).
- `TestEventNewDecode` (new keys only).
- `TestEventMarshalDualWritesForIRM` (has `irm_id`, `irm_number: 501`, `irm_url`).
- `TestEventMarshalNoLegacyForOthers` (`Pager:"fake"` has no `irm_*` keys).
- `TestRecordLegacyDecode` (`testdata/legacy-record.json` loads through `Store.Get("S1")` with `AckDecided` and `SlackTS` intact).
- `TestRecordWritesBothIDs`.
- In `outbox_test.go`, a directory with one legacy and one new event file returns both, sorted by `ID`.

- [ ] **Step 2: Run them**

Run: `cd slack-agent && go test ./internal/pager/ ./internal/event/ ./internal/outbox/`
Expected: compile failures, since the fields don't exist yet.

- [ ] **Step 3: Implement, then update every caller to compile**

Callers are in `agent`, `dcrequest`, `ledger` and `main`. This is a mechanical rename at this point: the agent's text keeps using a fixed `IRM` until Task 4.

- [ ] **Step 4: Full CI set**

Expected: green, including Task 1's golden and legacy tests, unchanged.

**Allowed test edits:** field renames in struct literals and accessors (`IRMID:` -> `ID:`, `IRMNumber: 501` -> `Ref: "501"`, `IRMURL:` -> `URL:`). Task 1's `TestLegacyFixturesDecode` asserts `ID`/`Ref` instead of `IRMID`/`IRMNumber`, with the same values.

- [ ] **Step 5: Commit**

`git commit -m "Give the outbox event and agent records pager-neutral fields"`

### Task 4: The agent depends on a Pager, IRM implements it

**Files:**
- Modify: `slack-agent/internal/irm/client.go`, `client_test.go`, `internal/agent/agent.go`, `agent_test.go`, `internal/dcrequest/runner.go:88-90`, `internal/oncall/oncall.go:10-16`, `internal/config/config.go`, `main.go:42-72`

**Interfaces:**
- Consumes `pager.Alert`, `pager.ErrNotFound`, `pager.ErrAlreadyAcked` and `pager.Label` from Task 3.
- `agent.Pager` replaces `agent.IRM`:
  ```go
  type Pager interface {
      Get(ctx context.Context, id string) (pager.Alert, error)
      Find(ctx context.Context, ref string) (pager.Alert, error) // "#N", "N", or a link the pager understands
      Acknowledge(ctx context.Context, id string) error
      Unacknowledge(ctx context.Context, id string) error
  }
  ```
  The field `Agent.IRM` becomes `Agent.Pager`, and a new field `Agent.PagerName string` (default `irm`) feeds `pager.Label` in every string that says `IRM` today (`agent.go:186,188,214,277,279,324,343,354,441,587,590,603`).
- `irm.Client` keeps `Group` and `FindGroup` as they are and adds `Get` and `Find`, which wrap them and return `Group.Alert() pager.Alert` (ID, `strconv.Itoa(Number)` as Ref, Slack permalink before web, State as IRM reports it, first alert's labels and start time). `irm.ErrNotFound` and `irm.ErrAlreadyAcked` become `= pager.ErrNotFound` / `= pager.ErrAlreadyAcked`, so `errors.Is` holds both ways.
- `irm.Client.FinalShifts` decodes IRM's `user_pk`, `user_email`, `shift_start` and `shift_end` into its own struct and returns `[]oncall.Shift` (no JSON tags on `oncall.Shift`).
- `config.Config.Pager` comes from `PAGER` (default `irm`). The `IRM_*` variables are required only when `Pager == "irm"`. Any other value is a config error, `PAGER=<name>: no such pager`, until an adapter exists.
- `eventFromGroup(irm.Group)` becomes `eventFromAlert(name string, a pager.Alert) event.Event`.
- `dcrequest.BuildPrompt`'s fallback becomes `pager.Label(ev.Pager) + " alert group #" + ev.Ref`.

- [ ] **Step 1: Write the failing tests**

- `TestGetMapsGroupToAlert` and `TestFindReturnsAlert` in `client_test.go`, using the existing stub server; they check ID, Ref, URL (slack before web), State, Labels and StartsAt.
- `TestErrorsAreShared` (`errors.Is(irm.ErrAlreadyAcked, pager.ErrAlreadyAcked)`).
- `TestConfigUnknownPager` and `TestConfigPagerDefaultsToIRM` in config.
- In `agent_test.go`, a fake pager named `fake` attached via `file #77` replies with `fake #77` text, which shows the label isn't hard-coded.

- [ ] **Step 2: Run them**

Run: `cd slack-agent && go test ./...`
Expected: compile failures for the missing methods and fields.

- [ ] **Step 3: Implement and switch `main.go` to `Pager: irmc, PagerName: cfg.Pager`**

- [ ] **Step 4: Full CI set**

Expected: green. Task 1's golden tests pass unchanged; that's the proof the IRM text didn't move.

**Allowed test edits:** the test double's type and method names (`Group`/`FindGroup` -> `Get`/`Find`, return `pager.Alert`), `IRM:` -> `Pager:` in `Agent` literals, and `client_test.go` calls to `Group`/`FindGroup` where they now test the internal methods through `Get`/`Find`. No expected strings change.

- [ ] **Step 5: Commit**

`git commit -m "Make the Slack agent depend on a Pager interface with IRM as its adapter"`

### Task 5: Document the boundary

**Files:**
- Create: `docs/pager-adapters.md`: both adapter contracts (the poller's JSONL and the agent's `Pager` interface), the outbox event keys, the label map, the one-release dual-write rule and the condition for removing it, and what a new provider needs (ack semantics, where its labels live, link formats for `Find`).
- Modify: `README.md` (Layout: `poll.sh`, `pagers/`), `deploy/systemd/README.md` (the unit runs `poll.sh`; `irm-poll.sh` is a shim), `docs/real-oncall-status.md` (one Open item: remove the legacy keys after two clean weeks).

- [ ] **Step 1:** Write the docs. Run `rg -n "irm-poll.sh" --glob '!tests/**' --glob '!docs/superpowers/**' .` and expect hits only in the shim's own comment and the docs that describe it.
- [ ] **Step 2:** Commit `Document the pager adapter contract`, push the branch, open the PR.

### Task 6: Deploy and live check **(needs Weigang: merge, sudo)**

Each step runs on the production host. The agent is still in dry run, so a mistake can't ack anything.

- [ ] **Step 1: Baseline.** Record the newest outbox file and its keys, the last briefs log header (needs sudo), the agent's last 50 journal lines, and the time of the last IRM heartbeat ping.
- [ ] **Step 2: Agent first.** After the merge, run `git pull` in the agent's clone, then `bash deploy/systemd/install-slack-agent.sh`.
  Expected: within one scan, the journal has no `unmarshal` or `record` errors, and replying `status` in the DM returns the usual text. The agent now reads both the old poller's events and its own old records.
- [ ] **Step 3: Poller.** Weigang runs `sudo bash /var/lib/oncall-copilot/oncall-copilot/deploy/systemd/install.sh`.
  Expected: `systemctl cat oncall-copilot-poll.service` shows `poll.sh`. The next poll logs no `error:` and no `bootstrapped` line (that would mean the `seen` state was lost). The heartbeat keeps pinging.
- [ ] **Step 4: First real brief after deploy.**
  Expected: the outbox file has `pager`, `id`, `ref`, `url` and all legacy keys. Its briefs log header reads `IRM #<n>`. The DM has the same layout as Step 1's examples.
- [ ] **Step 5: Rollback, if any expectation fails.** Revert the merge commit on main, then rerun both installers (poller first, then agent). Dual-write and read keep either order safe. On rollback `install.sh` pulls (removing `poll.sh`) before it copies the old unit, so one timer tick in between may fail one poll; harmless.

## Not in this plan

- Removing the legacy `irm_*` keys: one small PR once both services have run two weeks on this release with no skew errors.
- Any non-IRM adapter: written when a real second provider exists to test against.
