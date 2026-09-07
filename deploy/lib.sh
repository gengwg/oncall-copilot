#!/usr/bin/env bash
# Shared helpers sourced by the deploy scripts.

# Read the alert-relay bearer token from the cluster secret Alertmanager uses.
relay_token() {
  kubectl -n observability get secret relay-token -o jsonpath='{.data.token}' 2>/dev/null | base64 -d
}
