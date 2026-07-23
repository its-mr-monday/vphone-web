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

echo ">> vphone-web starting (config: $CONFIG)"
exec "$BINARY" -config "$CONFIG" -log-level info
