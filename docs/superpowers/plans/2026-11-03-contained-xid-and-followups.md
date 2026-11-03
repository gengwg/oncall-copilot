# Contained Xids Hold, Existing Tickets Get Follow-ups

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A GPU Xid Critical alert on a node that is already contained never pages; the DC request flow handles it. When the node already has a DC ticket whose thread has gone quiet, that flow drafts a follow-up in the thread instead of "nothing drafted".

**Architecture:** `alert-relay/investigate.sh` overrides the model's verdict to `hold` for a contained known Xid, computed from node facts it already collects, and always writes the DC draft for it. The Slack agent's DC prompt (`slack-agent/internal/dcrequest/runner.go` `BuildPrompt`) gains a follow-up branch for an existing ticket thread, with a new `RESULT: FOLLOWUP_DRAFTED` outcome that the runner parses and the agent reports.

**Tech stack:** bash + jq (poller side), Go (agent). No new dependencies.

**Spec:** Weigang's decisions on 2026-11-03 during his shift: #5590 and #5591 (contained, already-ticketed GPU Xids) should not have paged; known Xids go to the file-dc-request skill, which decides whether to file; notify him. Scope answers: hold only **contained** Xids (node cordoned or carrying the `aranya.tech/gpu-fault` taint, read from the node, not the model); draft follow-ups for **any** hardware fault with an existing ticket. His three example follow-ups (ticket threads in provider channels) define the follow-up's shape.

## Global Constraints

- Known Xid = `alertname` starting with `GPU Xid Critical` (production name: `GPU Xid Critical (node-problem-detector)`).
- Contained = the collected node evidence says `cordoned: true`, or its taints include the key `aranya.tech/gpu-fault`. If node evidence wasn't collected, the node is NOT contained (fail open to the model's verdict).
- The override applies only on a cluster listed in `DC_PROVIDERS` with a node label; elsewhere nothing changes.
- Override verdict text, exactly: `hold - contained known Xid (<reason>): the DC request flow files a request or follows up on the existing ticket`, where `<reason>` is `node cordoned`, `gpu-fault taint`, or `node cordoned, gpu-fault taint`.
- For a contained known Xid the DC draft file is written whatever the model's `FAULT` call; every other case keeps today's rule (real + hardware/fabric + mapped cluster + node).
- Draft only, always. Nothing is posted to a provider channel; the owner sends drafts. Never cordon, label, taint or drain (unchanged runner rules).
- No regression: existing tests keep their expectations, except the one listed under Allowed test edits in Task 1.
- jq must parse on jq 1.7 (parenthesize object-value expressions).

## Follow-up shape (from the owner's three examples)

One short message, posted as a reply in the existing ticket thread: who it's for (mention the provider contact already in the thread), the ticket id, the node's identifiers (serial if known, hostname, IP if known), what changed since the last message (new evidence: Xid still latched, remapped rows rising, reboot didn't hold, how long since the last update), one concrete ask (a date, an RMA, a reopen, a specific check), and the node state ("cordoned with no customer workloads, so it can be taken any time" when true). Ends "Thank you."

## Review Focus

1. A contained known Xid with the model saying `page` must come out `hold` with the exact text above; an uncontained one (not cordoned, no gpu-fault taint) must keep the model's verdict.
2. Missing node evidence (no kubeconfig, kubectl failure) must not count as contained.
3. Follow-up only when the thread is quiet: the newest message in the thread is more than 24 hours old. A thread with a message in the last 24 hours gives `ALREADY_SENT` and no draft, so a re-run or `file` can't stack drafts.
4. The ticket lookup must find threads older than 14 days: one thread covers a node for its whole life (the skill's "One node, one thread"), and the owner's examples were 1-3 weeks old.
5. A failed draft (`attached_draft_exists`, not in channel) is `RESULT: FAILED` with the reason, not a silent success.

---

### Task 1: Contained known Xids hold and always reach the DC flow

**Files:**
- Modify: `alert-relay/investigate.sh` (after the verdict and fault parsing, ~line 353-368; the DC draft condition ~line 375)
- Modify: `tests/investigate_test.sh` (case 19 area, ~line 504-549; the kubectl stub's node JSON at ~line 74 may need an env-selected uncontained variant)

**Interfaces:**
- Produces: the brief's `Verdict:` line with the exact override text; `DC draft:` line present for every contained known Xid on a mapped cluster.

- [ ] **Step 1: Failing tests.** New cases: (a) contained (fixture's cordoned node), model `page` -> `Verdict: hold - contained known Xid (node cordoned): the DC request flow files a request or follows up on the existing ticket` and a draft exists; (b) node not cordoned but tainted `aranya.tech/gpu-fault=...:NoSchedule` -> hold with reason `gpu-fault taint`; (c) not cordoned, no gpu-fault taint, model `page` -> verdict stays the model's page; (d) contained but node evidence not collected (no kubeconfig) -> model's verdict; (e) contained, model `FAULT: false-alarm` -> still drafted and held; (f) unmapped cluster (c2) contained -> model's verdict, no draft; (g) a non-Xid hardware alert (e.g. `IB Rail Down`) contained with model page -> model's verdict unchanged.
- [ ] **Step 2: Run** `./tests/investigate_test.sh` — new cases fail; old ones pass.
- [ ] **Step 3: Implement** in investigate.sh: compute `contained_reason` from `node_info` lines (`cordoned: true`; a taint key `aranya.tech/gpu-fault`), apply the override after the verdict is parsed, and widen the DC draft condition for contained known Xids. A short why-comment citing the owner's rule.
- [ ] **Step 4: Run** `./tests/investigate_test.sh`, `./tests/irm_poll_test.sh`, `./tests/query_scripts_test.sh`, `./tests/score_verdicts_test.sh` — all pass.
- [ ] **Step 5: Commit** `Hold contained GPU Xids and always hand them to the DC flow`.

**Allowed test edits:** the existing "false alarm or no provider -> no draft" check (~line 537-549) used a GPU Xid alert on a cordoned node on the mapped cluster; under the new rule that case is drafted. Keep the check's meaning by using a non-Xid hardware alert for its false-alarm half (the unmapped-cluster half is unchanged). Nothing else.

### Task 2: Follow-ups on existing ticket threads

**Files:**
- Modify: `slack-agent/internal/dcrequest/runner.go` (`BuildPrompt`, `resultLine`, `Status`), `runner_test.go`
- Modify: `slack-agent/internal/agent/agent.go` (DC result handling ~line 519-538), `agent_test.go`

**Interfaces:**
- Produces: `dcrequest.FollowupDrafted Status = "followup_drafted"`; `RESULT: FOLLOWUP_DRAFTED <thread link>` parsed into it; agent reply `Follow-up drafted in the existing thread for <cluster>/<node>: <link>. Review and send it from your Slack drafts.`; ledger/record status `followup_drafted`.

- [ ] **Step 1: Failing tests.** `runner_test.go`: ParseResult maps `RESULT: FOLLOWUP_DRAFTED https://x/thread` to `FollowupDrafted` with that detail; BuildPrompt contains the follow-up rules (look back 60 days for the node's thread; quiet = newest message older than 24 h; draft the reply with `slack_send_message_draft` and `thread_ts`; the follow-up shape; `ALREADY_SENT` when the thread had a message in the last 24 h). `agent_test.go`: a runner returning FollowupDrafted gives the exact reply text above and records `followup_drafted` in the ledger; existing drafted/already_sent/failed replies unchanged.
- [ ] **Step 2: Run** `cd slack-agent && go test ./...` — fail.
- [ ] **Step 3: Implement.** Rewrite the prompt's existing-ticket rule: search the provider channel for the node's request or ticket thread in the last 60 days; if found and its newest message is within 24 h, `RESULT: ALREADY_SENT <link>`; if found and quiet, gather fresh evidence per the skill, write one follow-up in the shape above, create it with `slack_send_message_draft` (channel + `thread_ts` of the thread's parent), and output `RESULT: FOLLOWUP_DRAFTED <thread link>`; if none, today's new-request path. Keep every other rule (no cordon, draft only, RESULT last line).
- [ ] **Step 4: Run** `test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...` plus the four shell suites — pass; the golden BuildPrompt test is updated only where the prompt text intentionally changed (state it in the report).
- [ ] **Step 5: Commit** `Draft a follow-up in an existing DC ticket thread instead of stopping`.

### Task 3: Docs

- [ ] `docs/real-oncall-status.md` "Decisions and why": one bullet for contained known Xids and follow-ups, with the why (the 2026-11-03 shift: #5590/#5591 paged on faults already ticketed and contained). `deploy/systemd/README.md` needs nothing. Commit `Document contained Xids and DC follow-ups`.

## After merge (owner)

Deploy agent first (`install-slack-agent.sh`), then the poller (`sudo install.sh`). Not during an on-call shift. Then watch the next contained Xid: the brief says hold with the override text, and the thread shows a follow-up draft or "already exists".
