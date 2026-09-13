# Feedback: NemoClaw / Token Factory / OpenClaw

Collected while building the on-call copilot. Each item is reproducible.
This doubles as hackathon "Most Valuable Feedback" submission material.

**Observed on `nemoclaw` CLI v0.0.109** (npm `nemoclaw@0.1.0`, the only version
published to npm at the time of writing). Upstream `NVIDIA/NemoClaw` was at tag
`v0.0.123`, so some of these may already be fixed in builds not published to
npm. Where an upstream issue already tracks a finding, it is cited inline.

## NemoClaw (v0.0.109, OpenShell 0.0.101, Ubuntu 26.04 host, Docker 29)

### 1. Dashboard port reallocation breaks the forward (agent-recoverable blocker)

When the default dashboard port (18789) is occupied by a stale forward at
onboard time, the host port is auto-bumped to 18790, but the sandbox image is
already baked with `CHAT_UI_URL` on 18789. The forward then maps
host:18790 -> sandbox:18790 where nothing listens (`Empty reply from server`),
and `nemoclaw <sb> status` reports "agent delivery chain could not be proven"
forever.

Worse, the dead 18790 listener is then recorded as owned by the sandbox, so
subsequent `onboard --resume` runs protect it (stale-listener cleanup skips it)
while still failing with "Port 18790 is not available ... cannot be
reallocated" — a self-deadlock.

Workaround found: `dashboardPort` in `~/.nemoclaw/sandboxes.json` must match
the port baked into the image; kill all stray `ssh -L 187x` forwards; then
`onboard --resume`.

Expected: either forward host:X -> sandbox:18789 (honor the baked port as the
target), or rebuild/reconfigure when the dashboard port changes.

### 2. Tavily web search breaks the sandbox image build

With `TAVILY_API_KEY` present in the environment, non-interactive onboard
auto-selects Tavily (`webSearchConfig { fetchEnabled: true, provider: tavily }`)
and the BuildKit image build fails at:

    openclaw plugins inspect tavily --json > /dev/null   (exit 1)

The Brave path uses `install_reviewed_openclaw_plugin` with a pinned npm
integrity hash; the Tavily path just runs `plugins inspect tavily`, which fails
(tavily is not bundled in the image and not an npm-reviewed plugin).

Workaround: unset TAVILY_API_KEY during onboard; add web search to the running
sandbox afterwards.

Expected: either ship a pinned Tavily plugin, or gracefully skip web search in
non-interactive mode when the plugin can't be installed.

### 3. Ubuntu 26.04 onboarding works (validation gap)

Docs list Ubuntu 24.04 as the validated host path with 26.04 "pending".
CLI install, Docker preflight, OpenShell gateway, sandbox build, and OpenClaw
agent turns all worked on 26.04.1 (Node 24.19, Docker 29.7). Worth extending
the validated matrix.

### 9. `openshell forward start` reports failure on a forward that succeeded

No upstream issue matches this one; the closest, #7266, is a listener that
genuinely failed to start rather than a probe misreporting one that worked.

`openshell forward start --background 18789 oncall` exits 52 with
`ssh exited before local forward listener opened on 127.0.0.1:18789`, but the
forward is up. Running the same command with `-vv` shows ssh getting all the
way through:

```
Local forwarding listening on 127.0.0.1 port 18789.
setting up multiplex master socket
forking to background
Error: ssh exited before local forward listener opened on 127.0.0.1:18789
```

The readiness probe races ssh's fork to background and gives up too early.
`ss -lnt` shows the listener, and `nemoclaw <sb> exec -- openclaw cron list`
works immediately afterwards.

Impact: worse than a cosmetic error. `nemoclaw <sb> recover` consumes the same
probe, so it also reports failure and tells you to re-run the very command that
just worked — a loop with no exit. Each retry leaves another ssh mux holding the
port, which eventually produces a real `Port ... is not available` collision. The
false negative manufactures the symptom it then blames (see item 1).

Expected: poll the listener (or wait on the mux socket) before declaring
failure, and treat an existing healthy forward as success.

### 10. `rebuild` prints success, exits 1, and silently drops cron jobs

A closed issue, [NVIDIA/NemoClaw#11137][i11137], covers `rebuild` exiting 1, but
for a sandbox left in an Error phase. Here the rebuild genuinely succeeds and
the exit code is the only thing wrong — and the dropped cron jobs are not
mentioned anywhere upstream.

[i11137]: https://github.com/NVIDIA/NemoClaw/issues/11137

`nemoclaw <sb> rebuild -y` ends with:

```
  Sandbox 'oncall' rebuilt successfully
    Now running: OpenClaw v2026.7.1
[exited with code 1]
```

The non-zero exit comes from post-rebuild verification (`gateway: HTTP 0`,
`dashboard: port forward not working`), which is itself downstream of item 9.
Scripts see only the exit code and cannot tell "rebuild failed" from "rebuild
worked, verification probe is broken".

Separately, the rebuild preserves `/sandbox/.openclaw/workspace` exactly as
advertised (files, memory, skills all survived) but `openclaw cron list` comes
back with 0 jobs. The workspace is restored; the job registry is not. Nothing in
the output says so, so an unattended agent silently stops running.

Expected: exit 0 when the rebuild succeeded and only verification was
inconclusive; either restore cron jobs alongside workspace state or say plainly
that they must be re-registered.

### 11. Post-rebuild device scope upgrade has no discoverable approval path

**Already tracked upstream as [NVIDIA/NemoClaw#10070][i10070] (open).** That
issue reports the same gap and notes that the fix (#9853, "name the openclaw
devices review path on a permission scope upgrade") shipped in `v0.0.114` yet
still does not emit. This report is a confirmation on `v0.0.109`, which predates
that fix, so it adds a second platform and the exact recovery sequence rather
than a new finding. The related hang after approval is
[NVIDIA/NemoClaw#11422][i11422].

[i10070]: https://github.com/NVIDIA/NemoClaw/issues/10070
[i11422]: https://github.com/NVIDIA/NemoClaw/issues/11422

After a rebuild, every gateway call fails with:

```
GatewayClientRequestError: scope upgrade pending approval (requestId: ...)
gateway closed (1008): pairing required: device is asking for more scopes than
currently approved
```

The device is already paired as `operator`; the rebuild makes it request
`operator.admin` on top of its existing `operator.pairing/read/write`. Nothing
in `nemoclaw --help`, `openshell --help`, `openshell gateway --help`,
`openshell sandbox --help`, or `openshell doctor` mentions approving it, and
`openshell gateway login` refuses ("does not use edge or OIDC authentication").
The `openshell term` TUI surfaces pending *network rules* on the sandbox row but
not this pairing request.

The command exists only inside the sandbox:

```
nemoclaw <sb> exec -- openclaw devices list
nemoclaw <sb> exec -- openclaw devices approve <requestId>
```

which itself warns `Direct scope access failed; using local fallback` before
succeeding.

Expected: surface the pending request and its approval command in the error
text, or in `nemoclaw <sb> recover` / the TUI alongside network rules.

## Token Factory

### 4. Reasoning models silently return `content: null` when max_tokens is small

`nvidia/NVIDIA-Nemotron-3-Nano-30B-A3B` with `max_tokens: 20` returns HTTP 200
with `message.content: null` and all tokens consumed by `reasoning`. No error,
no truncation flag in the response. Client libraries that read `.content`
surface this as an empty reply.

Expected: return the reasoning overflow as an explicit finish_reason or an
error, so clients know to raise max_tokens.

## OpenClaw

### 5. Unattended agent-turn jobs don't reliably execute tools with reasoning models

OpenClaw 2026.7.1. An isolated or custom-session cron `agentTurn` job with a
reasoning model (Nemotron 3 Ultra) narrates tool calls as text instead of
executing them (e.g. emits `{"tool": "exec", ...}` in the reply body, or
`TOOL: search_code\nARGUMENTS: {...}`), and sometimes loops `search_code`
errors indefinitely. Interactive `openclaw agent --message` in the main session
executes the same tools fine. `--tools exec,read,write` and `--clear-tools` did
not fix it; `--thinking off` did not fix it. `--session main` requires
`systemEvent` payloads (no agentTurn).

Impact: unattended investigation agents are unreliable. Workaround: use a
`command` payload that runs the deterministic collection and calls the model
directly, reserving the LLM for analysis.

### 6. Code-mode trigger scripts expose `tools`, not the documented `exec` global

The current Automations docs use `await exec({ command: ... })` and
`trigger.state`. This build's code-mode exposes `tools` (object) and `trigger`,
but NOT `exec` or `fetch` (`ReferenceError: exec is not defined`). stdout is at
`r.result.content[i].text` for `type=="text"` after
`await tools.call("exec", {command})`. The docs/runtime mismatch cost real
debugging time.

### 7. Cron `--command-env` values stored in plaintext in job spec

`openclaw cron add --command-env NEBIUS_API_KEY=...` stores the secret in
plaintext in the job definition (`openclaw cron list --json` shows it). No
SecretRef support for command-env. Secrets should reference the OpenClaw
secrets store, not literal values.

### 8. Docs/runtime flag drift

This build's `openclaw cron add` has no `--stream-command`, no `--script`
payloads, and no `--every <30s` (min 30000ms), though the current Automations
docs describe all three. Version-pin the docs or gate features behind the
runtime that introduced them.

### 12. Channel delivery failure is only visible as a cron `error`

No upstream issue matches this one.

A cron job with `--announce --channel telegram` whose delivery fails reports:

```
lastRunStatus: error
lastDelivered: false
lastDeliveryStatus: not-delivered
```

with no indication that the *channel* is the problem. The command itself exited
0 and its stdout (the brief) is intact in `lastDiagnosticSummary`, so the run
looks like a script failure. Ticks that produce no output (`NO_REPLY`) stay
`ok`, so the error appears only on the ticks that matter.

The actual cause was a revoked Telegram bot token: `getMe` returned
`401 Unauthorized`, and `openclaw.json` held the unresolved placeholder
`openshell:resolve:env:TELEGRAM_BOT_TOKEN`. `nemoclaw <sb> channels status`
reported registration, policy coverage, allowed IDs and group policy all `ok`,
with only `Bot API reachability: startup outcome not conclusive from the log
window` hinting at it. `channels add telegram` does name it
("Bot token was rejected by Telegram"), but only when re-enrolling.

Expected: distinguish delivery failure from command failure in the run status,
and have `channels status` perform the `getMe` check it already knows how to do.
