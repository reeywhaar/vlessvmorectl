#!/usr/bin/env bash
#
# Regenerate the README screenshots.
#
#   docs/screenshots/capture.sh
#
# Builds the frontend and the binary, starts three stand-in vlessvmore nodes and a panel
# against them, drives headless Chromium over the DevTools protocol, and writes the PNGs
# next to this script. Everything it starts is stopped again on the way out, including on
# failure.
#
# Requires: go, node, and Chromium or Chrome. No VPN, no Docker, no npm packages beyond
# the frontend's own.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
work="$(mktemp -d)"

WIDTH="${WIDTH:-600}"
THEME="${THEME:-dark}"

# The panel's listen port is not configurable — it is :80 inside a container and the
# operator remaps it. Here that means this script needs to be able to bind :80: fine on
# macOS, but on Linux it wants either root or
#   sudo setcap cap_net_bind_service=+ep <binary>
PANEL_URL="http://127.0.0.1"

chromium=""
for candidate in \
  "/Applications/Chromium.app/Contents/MacOS/Chromium" \
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  "$(command -v chromium || true)" \
  "$(command -v chromium-browser || true)" \
  "$(command -v google-chrome || true)"; do
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then chromium="$candidate"; break; fi
done
if [ -z "$chromium" ]; then
  echo "no Chromium or Chrome found; set one on PATH" >&2
  exit 1
fi

pids=()
cleanup() {
  for pid in "${pids[@]:-}"; do kill "$pid" 2>/dev/null || true; done
  # Chromium writes its profile out as it exits, so removing the directory immediately
  # races it and leaves "Directory not empty" noise on the way out.
  sleep 1
  rm -rf "$work" 2>/dev/null || true
}
trap cleanup EXIT

echo "==> building the frontend"
(cd "$root/web" && npm ci --silent && npm run build --silent)

echo "==> building the panel"
(cd "$root" && go build -o "$work/vlessvmorectl" .)

echo "==> starting the stand-in nodes"
node "$here/stub.mjs" > "$work/stub.log" 2>&1 &
pids+=($!)

echo "==> starting the panel"
export VLESSVMORECTL_DATA_DIR="$work/data"
"$work/vlessvmorectl" users add demo demopassword123 > /dev/null
VLESSVMORE_SERVERS="http://127.0.0.1:8801|TOKENAAAAAAAAAAAAAAAAAAAAAAAAAAA,http://127.0.0.1:8802|TOKENBBBBBBBBBBBBBBBBBBBBBBBBBBB,http://127.0.0.1:8803|TOKENCCCCCCCCCCCCCCCCCCCCCCCCCCC" \
  "$work/vlessvmorectl" serve > "$work/panel.log" 2>&1 &
pids+=($!)

for _ in $(seq 40); do
  curl -fsS "$PANEL_URL/healthz" > /dev/null 2>&1 && break
  sleep 0.25
done
curl -fsS "$PANEL_URL/healthz" > /dev/null || { echo "the panel never came up:"; cat "$work/panel.log"; exit 1; }

# Two statements, not one substitution: `$(curl -w '%{http_code}' && awk ...)` captures
# curl's "200" as well as the cookie, and the resulting "200\n<value>" is not a session.
curl -fsS -c "$work/ck" -X POST "$PANEL_URL/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"username":"demo","password":"demopassword123"}' -o /dev/null
cookie=$(awk '/vlessvmore_auth/ {print $7}' "$work/ck")
[ -n "$cookie" ] || { echo "could not sign in"; cat "$work/panel.log"; exit 1; }

echo "==> starting headless chromium"
"$chromium" --headless=new --remote-debugging-port=9222 \
  --user-data-dir="$work/chrome" \
  --no-first-run --no-default-browser-check \
  --hide-scrollbars --force-color-profile=srgb --disable-gpu \
  about:blank > "$work/chromium.log" 2>&1 &
pids+=($!)

echo "==> capturing at ${WIDTH}px, ${THEME} theme"
PANEL="$PANEL_URL" OUT="$here" SESSION_COOKIE="$cookie" WIDTH="$WIDTH" THEME="$THEME" \
  node "$here/shoot.mjs"

echo "==> done"
ls -la "$here"/*.png
