#!/usr/bin/env bash
# Install the Slack agent for the current user on the copilot host. Expects
# ~/.config/oncall-copilot/agent.env (see user/agent.env.example), membership of
# the oncall-copilot group (deploy/systemd/install.sh adds it) and a Claude Code
# login with the claude.ai Slack connector. Run from the repo clone:
#   bash deploy/systemd/install-slack-agent.sh
set -euo pipefail
cd "$(dirname "$0")/../.."

[ -r "$HOME/.config/oncall-copilot/agent.env" ] || { echo "missing ~/.config/oncall-copilot/agent.env"; exit 1; }
id -nG | tr ' ' '\n' | grep -qx oncall-copilot || echo "warn: not in the oncall-copilot group yet; log out and in after install.sh added you"

mkdir -p "$HOME/.local/bin" "$HOME/.config/systemd/user"
# GOTOOLCHAIN=auto fetches the go.mod version if the host's go is older.
(cd slack-agent && GOTOOLCHAIN=auto go build -o "$HOME/.local/bin/oncall-slack-agent" .)
install -m 644 deploy/systemd/user/oncall-slack-agent.service "$HOME/.config/systemd/user/"
systemctl --user daemon-reload
systemctl --user enable --now oncall-slack-agent.service
systemctl --user restart oncall-slack-agent.service
sleep 2
systemctl --user --no-pager status oncall-slack-agent.service | head -12
echo "logs: journalctl --user -u oncall-slack-agent -f"
