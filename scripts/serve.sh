#!/usr/bin/env bash
# serve.sh — start the vphone-web server for a stable single-host deployment.
#
# Config is read from ~/.config/vphone-web/config.toml (override: serve.sh <path>).
# We deliberately CLEAR any stale VPHONE_WEB_* environment variables first, so the
# server can never inherit a previous session's temp/scratchpad paths — the config
# file is the single source of truth. (This is exactly the failure that stranded a
# whole install under /private/tmp; never again.)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="$REPO_ROOT/bin/vphone-web"
CONFIG="${1:-$HOME/.config/vphone-web/config.toml}"

# Strip any inherited VPHONE_WEB_* overrides.
while IFS= read -r var; do
  [ -n "$var" ] && unset "$var"
done < <(env | sed -n 's/^\(VPHONE_WEB_[A-Za-z0-9_]*\)=.*/\1/p')

if [ ! -x "$BINARY" ]; then
  echo ">> $BINARY missing — building"
  make -C "$REPO_ROOT" build
fi
if [ ! -f "$CONFIG" ]; then
  echo "!! config not found: $CONFIG" >&2
  echo "   copy $REPO_ROOT/config.example.toml there and edit, or pass a path." >&2
  exit 1
fi

# Ensure the amfidont AMFI-bypass daemon is running. Without it, AMFI SIGKILLs the
# signed vphone-cli (exit 137) and no VM can boot. The daemon does NOT survive a
# host reboot, so we (re)start it here. This is idempotent: if it's already up we
# leave it alone. Runs as `python3 -m amfidont --spoof-apple`, so match on cmdline.
if pgrep -f 'amfidont .*--spoof-apple' >/dev/null 2>&1; then
  echo ">> amfidont already running"
else
  echo ">> amfidont not running — starting (make amfidont_allow_vphone)"
  if make -C "$REPO_ROOT/vphone-cli" amfidont_allow_vphone; then
    echo ">> amfidont started"
  else
    echo "!! amfidont failed to start — VMs will not boot (AMFI will exit 137)." >&2
    echo "   see /tmp/amfidont-vphone.log; server will start anyway." >&2
  fi
fi

echo ">> vphone-web starting (config: $CONFIG)"
exec "$BINARY" -config "$CONFIG" -log-level info
