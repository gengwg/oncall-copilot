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

(placeholder — add as we build the agent skills/automations)
