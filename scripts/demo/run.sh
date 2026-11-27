#!/bin/sh
# Launch rexws against the scratch demo world at exactly 80x24.
# Never point this at a real config; it clears the demo's session flag.
set -eu

ROOT=${REXWS_DEMO_DIR:-/tmp/rexws-demo}

stty rows 24 cols 80
rm -f "$ROOT/state/main-live"
export REXWS_CONFIG_DIR="$ROOT/config"
export REXWS_REX_BIN="$ROOT/bin/rex"
"$ROOT/bin/rexws" || true

# The tape sets REXWS_DEMO_HOLD so the shell prompt and the typed command are
# never captured after the TUI quits: clear the screen and keep the shell busy.
if [ "${REXWS_DEMO_HOLD:-}" = 1 ]; then
  printf '\033[?25l\033[2J\033[H'
  sleep 3600 &
  hold=$!
  trap 'kill "$hold" 2>/dev/null; exit 0' TERM INT
  wait
fi
