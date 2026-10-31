# Deploying the real on-call services

Two services, two users, one host.

| | Poller | Slack agent |
|---|---|---|
| Runs as | system user `oncall-copilot` (nologin, read-only tokens) | the owner's own account (needs their Claude Code login, kube and ssh access for the DC skill) |
| Unit | `oncall-copilot-poll.timer` (system) | `oncall-slack-agent.service` (`systemd --user`, linger on) |
| Config | `/var/lib/oncall-copilot/.config/oncall-copilot/env` | `~/.config/oncall-copilot/agent.env` (see `user/agent.env.example`) |
| Installer | `install.sh` (sudo) | `install-slack-agent.sh` (no sudo) |
| Writes | the outbox, briefs log, DC drafts | IRM acknowledge/unacknowledge only, Slack DMs, its own state dir |

## Order

1. Merge to main first. `install.sh` pulls main into the poller's clone and then
   re-executes itself, so a branch is never what gets installed.
2. `sudo bash /var/lib/oncall-copilot/oncall-copilot/deploy/systemd/install.sh`
   creates `/var/lib/oncall-copilot/outbox` (0750, group `oncall-copilot`), adds
   the invoking user to that group, installs the units and starts the timer.
3. `sudo systemctl restart user@$(id -u).service` once, so the owner's user
   manager picks up the new group. Linger keeps the old manager alive, so a plain
   re-login is not enough. This briefly restarts the owner's other user services.
4. As the owner: clone the repo, fill `agent.env`, run
   `bash deploy/systemd/install-slack-agent.sh`. It builds with `GOTOOLCHAIN=auto`
   and enables the user service. Re-run it after every pull to rebuild.

## Slack app

Create it from a manifest: bot scopes `chat:write`, `im:history`, `im:write`,
`im:read`; event `message.im`; Socket Mode on; an app-level token with
`connections:write`; and `features.app_home.messages_tab_enabled: true` with
`messages_tab_read_only_enabled: false`, otherwise the DM refuses input. Install
it to the workspace to get the bot token.

## IRM token

Acknowledging needs a user token, not a service account: IRM > Settings >
Admin & API > "Create OnCall API token". The agent sends it bare in
`Authorization`; a `glsa_` service-account token is sent as a Bearer instead.

## Going live

The agent starts with `ACK_DRY_RUN=1` and posts "would ack" in each brief's
thread. Watch one of the owner's shifts across the 08:00 PT handoff, then set
`ACK_DRY_RUN=0` and `systemctl --user restart oncall-slack-agent`.
