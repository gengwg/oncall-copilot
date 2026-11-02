# Pager adapters

The poller and the Slack agent talk to the paging service through a small
boundary, so a second provider means one new adapter on each side and no
change to the core. Only Grafana IRM exists today.

## Selecting a pager

Both services read `ONCALL_PAGER` (default `irm`). It is not `PAGER`, which is
the terminal pager variable and is set in most shells.

- Poller: `alert-relay/poll.sh` runs `alert-relay/pagers/<name>.sh`.
  `ONCALL_PAGER_ADAPTER` overrides the path (tests). A missing adapter is an error.
  `alert-relay/irm-poll.sh` only execs `poll.sh`, kept so an older unit keeps working.
- Agent: `slack-agent` accepts only `irm` for now; any other value is a config
  error until an adapter exists. The `IRM_*` settings are required only for `irm`.

## Poller adapter contract

`pagers/<name>.sh` takes no arguments and reads its settings from the
environment (for `irm`: `IRM_API_URL`, `GRAFANA_URL`, `GRAFANA_TOKEN`). It prints
JSONL on stdout, oldest first, one object per alert group:

```
{"id": "...", "ref": "...", "url": "...", "state": "...", "severity": "...",
 "alert": {"alertname": "...", "severity": "...", "summary": "...",
           "description": "...", "labels": {}, "starts_at": "..."}}
```

- `id` is the provider's key and the dedupe key. `ref` is the human number as a
  string. `url` may be empty. `state` is `firing`, `acknowledged`, `resolved` or `silenced`.
- `severity` is the group's severity, which `poll.sh` filters on (`SEVERITIES`).
  `alert` describes the firing alert.
- `alert.labels` must already have the `customer` label removed.
- A failed fetch prints one `error:` line to stderr and exits non-zero. The
  poller then changes no state and sends no heartbeat, so a refused token does
  not read as "no new alerts". Empty output with exit 0 means no groups.
- Output that is not valid JSON is also treated as a failed poll.

What `pagers/irm.sh` does: it fetches the first 50 groups, reverses them, and takes
the firing alert of each group (else its first alert). A group with no alerts
gets an empty `severity`, so it is skipped. `url` is the Slack permalink,
else the web one.

Event labels come from `pager_label` in `poll.sh` (`irm` shows as `IRM`; any
other name shows as itself). `pager.Label` in the agent does the same.

## Outbox events

`poll.sh` writes one `$OUTBOX_DIR/<id>.json` per brief, written whole and then renamed.
Keys: `pager`, `id`, `ref` (string), `url`, `alertname`, `severity`, `cluster`,
`node`, `starts_at`, `fault`, `verdict`, `failure_mode`, `degraded`, `dc_draft`,
`brief`, `ts`. `cluster` and `node` are lifted from `alert.labels`.

For `irm` the poller also writes the legacy keys `irm_id`, `irm_number` (a number,
null when `ref` is not numeric) and `irm_url`.

The agent (`slack-agent/internal/event`) reads the new keys or, when they are
missing, the legacy ones. For `irm` it writes both into its records too. So the
poller and the agent can be deployed or rolled back in either order.

Removing the legacy keys: one small PR, after both services have run two weeks
on the release that introduced the boundary with no skew errors. It drops the
`irm_*` lines in `emit_event` and the fallback and dual write in `event`.

## Agent interface

`slack-agent/internal/agent` depends on this interface and not on a provider:

```go
type Pager interface {
	Get(ctx context.Context, id string) (pager.Alert, error)
	Find(ctx context.Context, ref string) (pager.Alert, error)
	Acknowledge(ctx context.Context, id string) error
	Unacknowledge(ctx context.Context, id string) error
}
```

`internal/pager` holds `Alert` (`ID`, `Ref`, `URL`, `State`, `Labels`, `StartsAt`)
and the errors `ErrNotFound` and `ErrAlreadyAcked`. `Find` takes whatever an
operator types: `#N`, `N`, or a link the pager understands. `internal/irm`
implements the interface. Wrap provider errors so `errors.Is` matches the two
sentinels. `State` of `firing` means still paging.

On-call shifts are not part of `Pager`. They come from the IRM client's
`FinalShifts`, wired in `main.go`, so a new provider also needs its own source.

## Adding a provider

Questions to answer before writing either side:

- Does an ack expire? The agent assumes an ack holds until a human acts.
- Where do the Alertmanager labels (`cluster`, `node`) live in its payload? The
  storm and repeat filters and the agent's cluster and node fields depend on them.
- How will `Find` parse its numbers and links?
- Which identity do the agent's acks show as, and can a human tell it from a person?
- How is "whose shift is it" looked up?

Then:

1. Add `alert-relay/pagers/<name>.sh` to the contract above, and a
   `pager_label` case if the name should read differently.
2. Add an implementation of `Pager` and a case in the config check that now
   rejects every name but `irm`. Wire its shifts in `main.go`.
3. Run `tests/` for the poller and `go test ./...` in `slack-agent` against it.
