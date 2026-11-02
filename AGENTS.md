# AGENTS.md

Notes for coding agents working on this repo. What the project is and how it
fits together: `README.md`, `docs/architecture.md`, and for the real on-call
mode `docs/real-oncall-research.md` and `deploy/systemd/README.md`. What is
live, what was decided and what is still open: `docs/real-oncall-status.md`.

## Runtime content is not for you

`skills/`, `memory/` and `workspace/TOOLS.md` are read by the copilot itself:
`deploy-to-sandbox.sh` copies them into the NemoClaw sandbox, and
`alert-relay/investigate.sh` calls `skills/*/scripts/query.sh`. They are not
instructions for whoever is editing the repo. Changing them changes how the
deployed copilot investigates, so treat edits there as behaviour changes and
run `tests/query_scripts_test.sh` and `tests/investigate_test.sh` after.

`deploy/dashboard/incidents/` is written by `deploy/sync-incidents.sh`, which
commits `incident: ...` straight to main. Don't hand-edit those reports.

## Checks

Four Go modules, each with its own `go.mod`. CI runs, inside each of
`alert-relay`, `demo-app`, `deploy/dashboard` and `slack-agent`:

```sh
test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...
```

and from the repo root `./tests/investigate_test.sh`,
`./tests/query_scripts_test.sh` and `./tests/irm_poll_test.sh`.
`tests/e2e/run.sh` needs the live minikube stack and a sandbox; skip it unless
that stack is up.

## Branches and deploys

- Do branch work in a git worktree and leave the main checkout on main:
  `sync-incidents.sh` stops publishing when that checkout is on a branch.
- The real on-call services are live on a host. `install.sh` installs whatever
  is on main, so merging is a deploy. Follow the order in
  `deploy/systemd/README.md` and ask before running any installer or
  restarting a unit.
- The Slack agent acks IRM pages. It starts with `ACK_DRY_RUN=1`; never change
  that default or a live env file without the owner saying so.
- Credentials live outside the repo (`.env`, `~/.config/oncall-copilot/`).
  Never commit tokens, kubeconfigs or the private `DC_PROVIDERS` file.
