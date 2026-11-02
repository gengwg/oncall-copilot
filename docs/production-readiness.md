# Production readiness plan

What stands between the real on-call mode as it runs today (live in shadow on
one host, acks in dry run) and a copilot that can be trusted unattended at
03:00. Written 2026-11-01. Current state and history: `docs/real-oncall-status.md`.
Background and the original phases: `docs/real-oncall-research.md`. Internal
names stay out of this file, as in the status doc.

The hackathon demo (NemoClaw sandbox, minikube demo app, Telegram) is out of
scope. Production never calls NemoClaw.

## The invariant

IRM escalation is the safety net, and every part of the copilot must fail open
to it: a failure can cost a brief or an ack, never a page. Three rules already
hold this in code, and every change below is judged against them.

- A missing or unparseable verdict reads as `page` (`alert-relay/investigate.sh:357`).
- A degraded brief (model call failed, or node evidence missing) is never acked
  (`slack-agent/internal/agent/agent.go:318`).
- The agent acks only `hold` briefs for paging alerts that fired during the
  owner's own shift, and only after the DM has posted.

The one change that can weaken the safety net is turning acks on, so it is
gated on measured verdict accuracy (A2) rather than on time.

## Two stages

**Stage A, owner production.** Unattended on the owner's own shifts, with acks
live. Needs no team decision. Every item has an exit test.

**Stage B, team service.** Other people's shifts, service identities, and the
research doc's Phases 2-4. Gated on team decisions listed at the end.

## Stage A

### A1. Inference that does not run out at night

The poller's model key is a company key shared with other work under a weekly
spend limit. The copilot's own use is small (research doc §5, Cost: roughly
$8-13 a month at a storm-week rate), so the risk is someone else's usage
exhausting the shared limit. Requests also don't yet set the routing options
the research doc recommends for a router (`zero data retention`, fallbacks
across upstream providers).

- Give the poller its own key with its own spend limit, separate from other
  company use. (Assumption to verify: the router supports per-key limits.)
- Add the router options to the request body through one optional env var, so
  the script stays provider-neutral and other providers ignore it.
- Exit: `tests/investigate_test.sh` gains a case where the stub inference
  returns HTTP 402 twice, and the brief comes out degraded with verdict `page`.
  On the host, one real brief succeeds through the new key.

### A2. Verdict accuracy, measured (Phase 1 exit test)

This is open item 1 in the status doc, and the gate for A3. The briefs log has
every verdict. Nobody has scored them yet.

- `alert-relay/score-verdicts.sh` joins the briefs log's verdicts with what IRM
  recorded for the same alert group and lists each `hold` a human then acted
  on. How to run it and read the output is in open item 1 of the status doc.
- Run it over the shadow window since 2026-10-25, then weekly.
- Exit: across all paging alerts in at least two weeks of shadow, zero `hold`
  verdicts on alerts that needed someone before morning. Record the false-page
  rate (a `page` on an alert nobody needed to touch) as the baseline for Stage B.

### A3. Acks live, with an audit trail and a kill switch

- Preconditions: A2 passes, and one 08:00 PT handoff has been watched in dry
  run (`deploy/systemd/README.md`, Going live).
- Set `ACK_DRY_RUN=0` and restart the agent.
- Every real ack already gets an in-thread reply. Extend the A2 script to list
  each live ack next to what happened to that alert group afterwards.
- Kill switch, written into the deploy README: `ACK_DRY_RUN=1` plus a restart,
  and `unack #N` for a single group.
- Exit: one week of live acks, every ack audited, none on an alert that needed
  action.

### A4. Credentials with known expiry dates

Cluster and Grafana tokens expire from 2027-01-23. The inventory is the
status doc's Credentials section; the agent's IRM token, the Slack app tokens
and the model key show "none recorded" there. An expired token fails safe (the poll fails and the
heartbeat stops) but quietly, because heartbeats go to a channel with no
escalation.

- Add an inventory table to the status doc: each credential, what it can do,
  where it lives on the host, its expiry, and how to renew it. No values.
- Set a reminder two weeks before the earliest expiry.
- Exit: the table has a row for every variable in the poller's env file and
  `agent.env` that holds a secret, and the reminder exists.

### A5. A reviewed record of the cluster identity

The read-only ServiceAccount runs on every in-service cluster but exists only
as a local patch (status doc, open item 4). Production identities need a
reviewed record.

- Apply the patch on a branch of the infrastructure repo and open the MR,
  noting the objects are already live. This needs the owner's OK to touch that
  repo.
- Exit: MR merged, and `kubectl auth can-i --list` as the ServiceAccount on one
  cluster matches the merged manifest.

### A6. Host restarts and deploys

Everything runs on one host. Linger and the timer should bring both services
back after a reboot, but that has not been tested. The poller and the agent
each have their own clone, and nothing records which revision each is running.

- Reboot the host once in a quiet hour. Exit: within five minutes the timer
  fires, the agent reconnects to Slack, and both heartbeats resume.
- Have `install.sh` and `install-slack-agent.sh` print the revision they
  installed. Write a rollback (revert on main, rerun both installers) into the
  deploy README.
- Exit: one deploy and one rollback done from the README alone.

### A7. A dead service is noticed within a shift

Quiet heartbeats are right while in shadow. Once acks are live, a dead agent
means pages escalate without a brief (safe), and a dead poller means no briefs
(also safe), but the owner should still find out the same day.

- Keep heartbeats wakeless. Add a line to the 08:05 digest DM when either
  heartbeat was held or missed in the past 24 hours. (Assumption: the agent can
  read the held state; the poller writes it under its state dir.)
- Exit: stop the poller for an hour, and the next digest says so.

## Stage B (needs team decisions)

### B1. Service identities

The agent acks under the owner's personal IRM token (acks show as the owner),
DMs one person, and drafts DC requests through the owner's own `claude` login.
That is right for one person's shifts and wrong for a team service.

- A service IRM token whose acks show as the copilot, scoped to the ack and
  note endpoints, with the endpoint allowlist kept in code.
- A team Slack app rather than a personal one.
- DC drafts through a service account for the model CLI, or drafts only (no
  CLI) for other people's shifts.

### B2-B4. The research doc's Phases 2-4

Shadow briefs in the alert's Slack thread (Phase 2), notes on the IRM alert
group (Phase 3), and an overnight hold on the warning chain with the copilot
paging the real ones (Phase 4). Each phase's exit test is in research doc §6.
Phase 4 is the only one that cuts false night pages (status doc, open item 3).

### B5. Operations beyond one person

A runbook for the copilot itself (what each alert or degraded state means, how
to stop it), a second operator who has run a deploy, and a host that is not a
personal workstation.

## Decisions needed

| Decision | Who | Recommendation |
|---|---|---|
| Dedicated model key and its limit (A1) | owner, with whoever owns the company key | a separate key with a small monthly limit, for example 3x the expected spend |
| OK to open the infrastructure MR (A5) | owner | yes, before acks go live; it is the reviewed record of an identity already on every cluster |
| Whether Stage B happens, and when | team | only after A3 has run a month; until then the false-page baseline from A2 is the input to that discussion |
| Overnight hold on the warning chain (B4) | team | per research doc §3 |

## Order

A1 and A4 first: they are small and protect what is already live. A2 next,
because it gates A3. A5 and A6 can run alongside A2. A3 once A2 passes. A7
after A3. Stage B only after A3 has a month of clean audits.
