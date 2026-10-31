#!/usr/bin/env bash
# Install the shadow-mode poller on the copilot host as the oncall-copilot user.
# Expects the user, its env, kubeconfigs and repo clone from the Phase 0 setup.
# Run on the host with sudo rights: bash deploy/systemd/install.sh
set -euo pipefail
U=oncall-copilot
H=/var/lib/oncall-copilot
SRC="$H/oncall-copilot/deploy/systemd"

# bash keeps reading the file it opened, so a pull that replaces this script
# would leave the old steps running with the new units. Pull, then re-run the
# fresh copy once.
if [ "${1:-}" != "--pulled" ]; then
  sudo -H -u "$U" git -C "$H/oncall-copilot" pull -q --ff-only
  exec bash "$SRC/install.sh" --pulled
fi
sudo -H -u "$U" mkdir -p "$H/ws/memory/incidents" "$H/ws/memory/runbooks"
sudo -H -u "$U" ln -sfn "$H/oncall-copilot/skills" "$H/ws/skills"

# The outbox is where the Slack agent (deploy/systemd/user/, run by a person's
# account) reads one event per brief. Group-readable, never writable by it.
AGENT_USER="${AGENT_USER:-${SUDO_USER:-$USER}}"
sudo install -d -m 750 -o "$U" -g "$U" "$H/outbox"
id -nG "$AGENT_USER" | tr ' ' '\n' | grep -qx "$U" || sudo usermod -aG "$U" "$AGENT_USER"

sudo install -m 644 "$SRC/oncall-copilot-poll.service" "$SRC/oncall-copilot-poll.timer" /etc/systemd/system/
sudo systemctl daemon-reload

# One run by hand first: on a fresh state dir it only records existing groups.
sudo systemctl start oncall-copilot-poll.service
sudo journalctl -u oncall-copilot-poll.service -n 5 --no-pager -o cat

sudo systemctl enable --now oncall-copilot-poll.timer
systemctl list-timers oncall-copilot-poll.timer --no-pager
echo "briefs: $H/.local/state/oncall-copilot/briefs.log (read with: sudo tail -f ...)"
