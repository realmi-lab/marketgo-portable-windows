#!/bin/sh
set -eu
bundle=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$bundle"
# Keep the portable process independent of inherited deployment settings.
exec /usr/bin/env -i HOME="$HOME" PATH=/usr/bin:/bin:/usr/sbin:/sbin TMPDIR="${TMPDIR:-/tmp}" \
  MARKET_SHARE_STORAGE_MODE=sqlite-memory \
  MARKET_SHARE_DEPLOYMENT_MODE=standalone \
  MARKET_SHARE_LLM_ENABLED=false \
  MARKET_SHARE_NOTICE_ENABLED=false \
  MARKET_SHARE_HERMES_NOTICE_AUTOSTART=false \
  MARKET_SHARE_FRONTEND_ROOT="$bundle/web" \
  MARKET_SHARE_OPEN_BROWSER=true \
  MARKET_SHARE_API_HOST=127.0.0.1 \
  MARKET_SHARE_API_PORT=20560 \
  "$bundle/marketgo"
