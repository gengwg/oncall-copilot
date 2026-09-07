# Feedback: NemoClaw / Token Factory / OpenClaw

Collected while building the on-call copilot. Each item is reproducible.
This doubles as hackathon "Most Valuable Feedback" submission material.

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
