#!/usr/bin/env bash
# Kept so an older unit file keeps working; the poller is poll.sh.
exec bash "$(dirname "$0")/poll.sh" "$@"
