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

# Verify they are *ours*. A stub left running from an earlier attempt keeps the port, the
# new one dies with EADDRINUSE in the background where nothing notices, and the capture
# quietly succeeds against stale data — which is exactly how a set of screenshots ends up
# showing something the code no longer does.
for port in 8801 8802 8803; do
  ready=""
  for _ in $(seq 40); do
    if curl -fsS "http://127.0.0.1:$port/api/server" > /dev/null 2>&1; then ready=1; break; fi
    sleep 0.25
  done
  if [ -z "$ready" ]; then
    echo "stand-in node on :$port never answered:" >&2
    cat "$work/stub.log" >&2
    exit 1
  fi
done
if ! grep -q "vpn-nl.example.com" <(curl -fsS http://127.0.0.1:8801/api/server); then
  echo "something else is already listening on :8801 — stop it and try again" >&2
  cat "$work/stub.log" >&2
  exit 1
fi

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

# Seed a subscriber through the panel's own API rather than by writing subscribers.json.
#
# Two reasons. The daemon is the only writer of that file and refuses to save over an
# outside edit, so a hand-written seed would make the first UI change in the capture fail;
# and going through the API means these screenshots exercise the real create-and-attach
# path, so a break in it shows up here rather than in production.
echo "==> seeding a subscriber"
api() { curl -fsS -b "$work/ck" -H 'Content-Type: application/json' "$@"; }

subscriber=$(api -X POST "$PANEL_URL/api/subscribers" \
  -d '{"name":"Ivan Petrov","note":"paid to August"}')
subscriber_id=$(printf '%s' "$subscriber" | sed -n 's/.*"id": *"\([^"]*\)".*/\1/p' | head -1)
share_token=$(printf '%s' "$subscriber" | sed -n 's/.*"token": *"\([^"]*\)".*/\1/p' | head -1)
[ -n "$subscriber_id" ] || { echo "could not create a subscriber: $subscriber"; exit 1; }

# One account on each of two nodes, which is the case the whole feature exists for: a
# person whose accounts live in more than one place.
servers=$(api "$PANEL_URL/api/servers")
nl_id=$(printf '%s' "$servers" | sed -n 's/.*"id": *"\([^"]*\)",[[:space:]]*"url": *"http:\/\/127.0.0.1:8801".*/\1/p' | head -1)
de_id=$(printf '%s' "$servers" | tr -d '\n' | sed -n 's/.*"id": *"\([^"]*\)",[[:space:]]*"url": *"http:\/\/127.0.0.1:8802".*/\1/p' | head -1)
for pair in "$nl_id:u_0B4X6TWQ8ZKM3N1PVJHR5DGYAC:phone" "$de_id:u_1C5Y7UXR9ALN4P2QWKIS6EHZBD:laptop"; do
  sid="${pair%%:*}"; rest="${pair#*:}"; uid="${rest%%:*}"; label="${rest#*:}"
  [ -n "$sid" ] || continue
  api -X POST "$PANEL_URL/api/subscribers/$subscriber_id/entries" \
    -d "{\"server_id\":\"$sid\",\"vless_user_id\":\"$uid\",\"label\":\"$label\"}" -o /dev/null || true
done

echo "==> starting headless chromium"
"$chromium" --headless=new --remote-debugging-port=9222 \
  --user-data-dir="$work/chrome" \
  --no-first-run --no-default-browser-check \
  --hide-scrollbars --force-color-profile=srgb --disable-gpu \
  about:blank > "$work/chromium.log" 2>&1 &
pids+=($!)

echo "==> capturing at ${WIDTH}px, ${THEME} theme"
PANEL="$PANEL_URL" OUT="$here" SESSION_COOKIE="$cookie" WIDTH="$WIDTH" THEME="$THEME" \
  SHARE_TOKEN="$share_token" \
  node "$here/shoot.mjs"

echo "==> done"
ls -la "$here"/*.png
