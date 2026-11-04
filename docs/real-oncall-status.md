# Real on-call mode: status and handoff

Where the real on-call work stands, what was decided and why, and what is
still open, so the next agent can pick it up. Last updated 2026-11-01.

How to deploy is in `deploy/systemd/README.md`; the background and the phased
plan are in `docs/real-oncall-research.md`. Internal names (host, clusters,
channels, IDs) stay out of this public repo. They live in the operator's
`~/.config/oncall-copilot/` and in the project's Claude memory notes.

## What is live

| Service | State | Writes |
|---|---|---|
| Poller (`alert-relay/poll.sh`, IRM adapter, systemd timer, every 60 s) | live, shadow mode | briefs log, outbox, DC drafts |
| Slack agent (`slack-agent/`, user service) | live, `ACK_DRY_RUN=1` | Slack DMs, "would ack" notes |

The poller reads Grafana IRM alert groups with a Viewer token, investigates
each new critical or warning group once with `investigate.sh`, and writes a
brief with a verdict. Node evidence comes from a read-only ServiceAccount on
every in-service cluster. It runs as a dedicated nologin user that cannot read
the operator's admin kubeconfigs, in a sandboxed unit (`ProtectHome`,
`ProtectSystem=strict`).

## How it got here

| Phase | PRs | What |
|---|---|---|
| Research | #40 | `docs/real-oncall-research.md` |
| 0: evidence and credentials | #41-#44 | bearer token for the query scripts; node evidence for alerts with `cluster`/`node` labels; computed state of the alert's own node condition; health-check conditions (`*Passed`) read as normal when True |
| 1: shadow poller | #45-#47 | IRM poller with storm caps; page-or-hold verdict; systemd timer |
| Monitoring | #48, #49, #51 | IRM heartbeat; held after two degraded briefs in a row |
| DC drafts | #50 | `FAULT: real\|false-alarm` call; pre-filled `/file-dc-request` draft for real hardware/fabric faults; one retry of a failed model call |
| Slack agent | #52-#55, #57, #58 | briefs as DMs; acks for healthy hold-verdict briefs of alerts that fired during the owner's shift; installer re-exec; `file`/`ack`/`unack`/`ignore` by alert number or link; `sent` reply and agent heartbeat; daily digest for off-shift briefs |
| Docs | #56, #59, #60 | deploy README and architecture notes (#56), `AGENTS.md` (#59), README testing section (#60) |

## Decisions and why

- **Poll IRM, no webhook.** Needs no public endpoint and sees every alert
  group, including ones IRM never posted to Slack.
- **Read-only credentials, separate user.** The credentials cap what a wrong
  model call can do; the sandbox does not have to.
- **Verdict and fault are separate calls.** `page|hold` is urgency, `real|false-alarm`
  is whether to file. A dead GPU on a drained node holds but still gets a DC draft.
  A missing call reads as page and as real: a wrong page costs sleep, a missing
  ticket costs more.
- **Facts the model must not infer are computed in code.** Live replays showed
  the model trusts the alert's wording over a condition missing from the
  evidence, so the alert condition's state and timing are stated outright.
- **"Previous" is the same alert on the same node.** Incident reports are
  named `<date>-<alert>@<cluster>@<node>.md`, and the previous incident and
  the related one are looked up by the report's Service line, so reports from
  before the change still match. With one report per alert per day, a later
  node's brief overwrote an earlier one's and the next brief quoted another
  node's cause (2026-11-04).
- **Heartbeats go to a quiet channel with no escalation**, as the deploy
  README recommends, so a dead shadow service is visible but wakes nobody. The
  poller skips the ping on any failed poll and holds it after two degraded
  briefs in a row (one upstream blip does not alert); once held, it stays held
  until a healthy brief. The 08:05 digest now carries a health line for both
  services, so a quiet heartbeat channel no longer means problems go unseen.
- **A contained known Xid holds, and the DC flow follows up.** A `GPU Xid
  Critical` or `GPU XID hardware fault` alert on a mapped cluster whose node is cordoned or carries the
  gpu-fault taint gets the hold verdict "hold - contained known Xid (<reasons>)",
  naming which of the two applied, and the poller always writes its local DC
  draft file, whatever the model said. The hold applies only once that file is
  written; an uncontained node, an unmapped cluster or missing node evidence
  keeps the model's verdict. Contained means cordoned or tainted, not
  necessarily drained: the model prompt still says drained, and the owner chose
  the cordon-alone scope explicitly (2026-11-03). The rule assumes that on
  these clusters only people (or the DC flow after triage) cordon or taint GPU
  nodes, as the owner confirmed on 2026-11-03; if automatic cordoning is ever
  introduced (for example a fault-quarantine controller), "contained" must
  narrow to the `aranya.tech/gpu-fault` taint, which only people apply. The Slack agent then looks for
  the node's ticket thread in the provider channel. If one exists, it drafts a
  follow-up reply there when the thread has been quiet for 24 hours, and
  reports "already exists" when it has not; with no thread it drafts a new
  request. These Slack drafts are separate from the poller's file, and the owner
  sends every one; the agent never does. A drafted request or follow-up settles
  the node in the agent's ledger: follow-ups and already-active results for 7
  days, new requests for 14; a follow-up stays on the 7-day rhythm after the
  owner marks it `sent`. Why: on the owner's 2026-11-03 shift two Xid
  alerts paged on faults that were already contained and ticketed; the previous
  on-call's handoff covered them and the team's Slack Claude app called them
  known. Limits: a follow-up runs only when the node alerts again, and `file`
  on a brief forces a run. Slack allows one attached draft per channel, so an
  unsent draft in a provider channel makes the next request or follow-up there
  fail until it is sent.
- **Acks are narrow, and dry run first.** Only healthy briefs with a hold
  verdict, for paging-severity alerts that fired during the owner's own shift.
  This amended the research doc's "no auto-ack" (§3, 2026-10-31 amendment).
  Turn `ACK_DRY_RUN` off only after watching one shift handoff in dry run.
- **The hackathon demo is retired** (2026-11-01, after the submission). Judge
  changes by their effect on the production path (`poll.sh` ->
  `investigate.sh` -> outbox -> Slack agent). The demo pieces (NemoClaw sandbox,
  minikube demo app, Telegram) are left as they are and not maintained; the
  real mode runs as plain host services and never calls NemoClaw.

## Credentials

Every secret the two services use, with no values. The poller's env file is
`/var/lib/oncall-copilot/.config/oncall-copilot/env` (sudo to edit; the timer
reads it on each run, so no restart). The agent's is
`~/.config/oncall-copilot/agent.env`; restart the agent after a change.

| Credential | Used by | What it can do | Where | Expires | Renew |
|---|---|---|---|---|---|
| Grafana service account token, Viewer | poller | read IRM alert groups; Prometheus through the datasource proxy | poller env `GRAFANA_TOKEN` (also `PROMETHEUS_TOKEN`) | about 2027-01-23 (90 days from Phase 0; not re-checked) | new token on the same service account |
| Cluster ServiceAccount tokens, one per in-service cluster | poller | get/list nodes, pods, events, namespaces | `<KUBECONFIG_DIR>/<cluster>.kubeconfig` | 2027-01-23 22:51Z to 2027-01-24 03:36Z (read from the token claims, 2026-11-02) | re-run the minting script per cluster (`CONTEXT=` override where the admin context isn't `admin@<cluster>`) |
| Model key | poller | inference calls, which spend money | poller env `NEBIUS_API_KEY` (name kept from the demo), with `INFERENCE_URL`, `MODEL` | none recorded; a company router key with a shared weekly spend limit | rotate in the router's console (production-readiness A1) |
| Poller heartbeat URL | poller | ping its IRM heartbeat integration; the secret is in the URL | poller env `HEARTBEAT_URL` | none recorded | regenerate on the IRM integration |
| IRM personal API token | agent | read alert groups and shifts; ack and unack as the owner (the client allowlists those two writes) | `IRM_PERSONAL_TOKEN` | none recorded | new token in the owner's IRM settings |
| Slack bot token | agent | DM the owner and read replies | `SLACK_BOT_TOKEN` | none recorded | the Slack app's settings |
| Slack app-level token | agent | open the Socket Mode connection | `SLACK_APP_TOKEN` | none recorded | the Slack app's settings |
| Agent heartbeat URL | agent | ping its IRM heartbeat integration | `HEARTBEAT_URL` | none recorded | regenerate on the IRM integration |
| Claude Code login | agent, DC drafts only | run the DC request skill headless, draft only, behind the runner's deny list | the owner's login on the host (`CLAUDE_BIN`) | login session | log in again on the host; until then the agent replies "DC request not drafted" with the failure reason |

Private but not secret: the `DC_PROVIDERS` TSV, `IRM_SCHEDULE_ID`,
`ONCALL_ME` and `SLACK_OWNER_ID`. An expired poller token fails safe: the poll
errors and the heartbeat stops. Heartbeats route to a channel with no
escalation, so check the earliest date above, not the pager.

## Open

1. **Phase 1 exit test.** Compare each brief's verdict with what on-call did.
   Pass: no `hold` on an alert that needed someone that night. On the host,
   as the owner (read-only, GET requests only; output has alert titles and
   links, so keep it on the terminal):
   `set -a; . ~/.config/oncall-copilot/agent.env; set +a; IRM_TOKEN=$IRM_PERSONAL_TOKEN bash alert-relay/score-verdicts.sh /var/lib/oncall-copilot/.local/state/oncall-copilot/briefs.log`.
   Each brief gets a category. `MISS?`: `hold` and a human acked or resolved
   it. `hold-ok`: `hold`, resolved, nobody acted. `page-ok`: `page` (or no
   readable verdict) and a human acted. `false-page?`: `page`, resolved,
   nobody acted. `open`: not resolved, nobody acted yet; it can still be
   acked later, so recheck any `open` hold before declaring A2 passed.
   `unknown`: the lookup or parse failed; it is never counted as ok. `MISS?`
   is a candidate list, not a verdict: the team acks most pages, noise
   included, so "a human acted" does not by itself mean the alert needed
   someone. The owner reads each `MISS?` row and decides. The summary also
   gives the false-page rate (`false-page?` / (`page-ok` + `false-page?`);
   `open` and `unknown` rows are left out), the same counts for night briefs,
   and `entries parsed` (with the distinct id count, so a twice-briefed id
   shows) against `headers seen`, with a WARNING if they differ. It also
   counts NUL blocks in the log and `Verdict:` lines that sit outside any
   entry, with a WARNING when there are any: the evidence has a gap. Score
   only periods when `ACK_DRY_RUN=1` held: live copilot acks use the owner's
   personal token and show as the owner, so any live-ack period makes
   `page-ok` too high and the false-page rate too low.
2. **Agent dry run.** Flip `ACK_DRY_RUN=0` after one observed handoff
   (`deploy/systemd/README.md`, Going live).
3. **False-alarm pings.** IRM pages before the copilot looks, so only an IRM
   change stops them: the warning chain holds at night and the copilot pages
   the real ones (research doc §3, Phase 4). That is a team decision.
4. **Infra record of the ServiceAccount.** The manifest and the `VIEWER_SA`
   option for the minting script exist only as a local patch in the
   operator's `~/.config/oncall-copilot/`. They are not in the infrastructure
   repository yet, so the identity is live on every cluster with no review record.
5. **Token renewal, by 2027-01-23.** The Grafana and cluster tokens expire
   first (see Credentials). Renew both, reinstall them on the host, and set
   a reminder for two weeks before.
6. **`DC_PROVIDERS` drift.** The private TSV is a copy of the provider table in
   the team's `file-dc-request` skill. Update both when a cluster or channel changes.
7. **Remove the legacy outbox keys.** `irm_id`, `irm_number` and `irm_url` are
   still written next to `pager`, `id`, `ref` and `url`
   (`docs/pager-adapters.md`). Drop the legacy writes (not the read fallback in the
   agent, since records are never pruned) in one small PR after the poller and
   the agent have both run two weeks on the pager-boundary release with no skew errors.

## Traps hit along the way

- Writes to shared clusters and merges are blocked by the auto-mode
  classifier until the human says so explicitly; sudo on the host needs their
  password. Give them the exact command and let them run it.
- In the Claude Code prompt, `!` runs one line typed at the start of a prompt.
  Several pasted `!` lines in one message, or a `!` sent from the web or phone
  view, run nothing.
- The host's login shell is zsh: a glob over a directory the caller cannot
  read aborts the command. Expand it inside `sudo -u <user> bash -c`.
- In the operator's interactive shell `rm` and `mv` are aliased to `-i` and
  hang on a prompt; use `command rm`/`command mv`. Scripts are unaffected.
- `kubectl auth can-i get pods/log` asks about a pod named `log`; use
  `--subresource=log`.
- `jq -r ... | tr -c 'A-Za-z0-9_-' '_'` also turns jq's trailing newline into `_`.
  Capture the value first, then sanitize it.
- systemd `EnvironmentFile=` does not expand `$VAR`; the poller's unit sources
  its env file through bash for that reason.
- Relative journal windows (`--since -1h`) keep showing a cleared problem.
  Check how old the newest warning is instead:
  `journalctl -u oncall-copilot-poll -n 200 -o short-unix | rg 'warn:|error:' | tail -1`.
