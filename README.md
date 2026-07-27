# vlessvmorectl

A web control panel for one or more [vlessvmore](https://github.com/reeywhaar/vlessvmore)
servers.

vlessvmore has a complete management API but no interface, so everything is
`docker exec vlessvmore vlessvmore user add alice` or a hand-rolled `curl`. Running more
than one node means logging into each host separately, and there is nowhere to see usage
across all of them. This is that missing piece: a login, an overview of every node, and
the user management you would otherwise do at a shell — links, QR codes, subscription
URLs, quotas, expiry and traffic history.

It holds no VPN state of its own. Every node remains the source of truth for its own
users; this only asks and tells.

<p align="center">
  <img src="docs/screenshots/overview.png" alt="Every managed node, with user counts and traffic" width="49%">
  <img src="docs/screenshots/server.png" alt="One node's users, with the ones needing attention first" width="49%">
</p>
<p align="center">
  <img src="docs/screenshots/user.png" alt="The user drawer: QR code, credentials, quota and traffic history" width="49%">
  <img src="docs/screenshots/login.png" alt="The sign-in screen" width="49%">
</p>

Real captures of the panel; only the data is invented. Regenerate them with
[`docs/screenshots/capture.sh`](docs/screenshots/capture.sh).

## How it fits together

```
                    ┌──────────────────────────────────────────┐
  browser  ────────▶│ vlessvmorectl  :80                       │
       cookie       │  POST /api/login                         │
   vlessvmore_auth  │  GET  /api/me                            │
                    │  GET  /api/servers  → [{id, url}]        │  ← no tokens
                    │  ANY  /api/proxy?url=…                   │
                    │  the React app                           │
                    └───────────────┬──────────────────────────┘
                                    │  Authorization: Bearer <token>
                                    ├──▶ https://vpn-nl.example.com/api/*
                                    └──▶ https://vpn-de.example.com/api/*
```

**The browser never receives a bearer token.** It asks this service to make each call and
this service attaches the credential. That is the central design decision, and it buys
four things:

- A full-control token for every VPN node never sits in a laptop's memory, so an XSS —
  including one arriving through a compromised npm dependency — cannot exfiltrate them.
- There is no `cors_origins` to configure and remember on every node. A forgotten one
  would otherwise fail as a browser CORS error that leaves no trace in any log you own.
- **No node's management API has to be reachable from the internet at all.** The panel can
  reach them over a private Docker network.
- Go sees the real transport error — DNS, refused, TLS, timeout — where a browser making
  the same cross-origin request only ever gets an opaque `TypeError`. The panel's error
  messages are accurate rather than guessed.

The cost is that this service is now a single point of connectivity: a node reachable from
your browser but not from this host is unmanageable. See [Not implemented](#not-implemented).

## Install

```sh
# On each vlessvmore node, mint a token for the panel. Shown once, never again.
docker exec vlessvmore vlessvmore token create panel --raw
```

```sh
# Here
mkdir -p data
cp .env.example .env      # put the url|token pairs in it
docker compose up -d

# Nobody can sign in until an administrator exists. There is deliberately no way to
# create the first one from the web — that would be a race against whoever reaches the
# URL first — so a shell on this host is the bootstrap credential.
docker exec vlessvmorectl vlessvmorectl users add alice
```

Then open the panel and sign in.

### compose

No `docker-compose.yml` ships with this repo; it is four lines of yours plus this:

```yaml
services:
  vlessvmorectl:
    image: ghcr.io/reeywhaar/vlessvmorectl:latest
    container_name: vlessvmorectl
    restart: unless-stopped

    # The tokens must not live in a committed file.
    env_file: .env

    volumes:
      # admins.json. Without this mount every administrator disappears when the
      # container is replaced, and the only way back in is a shell on this host.
      - ./data:/var/lib/vlessvmorectl

    healthcheck:
      test: ["CMD", "vlessvmorectl", "healthcheck"]
      interval: 30s
      timeout: 5s
      start_period: 5s
      retries: 3

    networks:
      - caddy

    labels:
      # This service issues session cookies and proxies credentialed calls. Terminate TLS
      # in front of it.
      caddy: panel.example.com
      caddy.reverse_proxy: "{{upstreams 80}}"
      caddy.log.output: stdout
      caddy.log.format: json

networks:
  caddy:
    external: true
```

Nothing is published to the host: Caddy dials the container directly. If you would rather
run it standalone, add `ports: ["127.0.0.1:8080:80"]` and put your own TLS in front.

**Better still**, if the panel and the nodes share a Docker network, point
`VLESSVMORE_SERVERS` at `http://vlessvmore-nl:80|TOKEN` and stop publishing each node's
management API entirely. Proxying is what makes that possible.

## Configuration

Everything is environment. There is no config file, because the only thing this program
needs to be told is which nodes it manages.

| variable | meaning |
| --- | --- |
| `VLESSVMORE_SERVERS` | comma-separated `url|token` pairs. Newlines work too. |
| `VLESSVMORE_TOKENS` | accepted as an alias for the above |
| `VLESSVMORE_LOG_LEVEL` | `debug`, `info` (default), `warn`, `error` |
| `VLESSVMORECTL_DATA_DIR` | overrides `/var/lib/vlessvmorectl` |

```
VLESSVMORE_SERVERS="https://vpn-nl.example.com|MHDJWEZ5…,https://vpn-de.example.com|QK7M2XA9…"
```

A malformed entry, or two entries for the same node, is a startup error — that is a typo,
and the container logs are where you will look for it. **Zero servers is not an error**:
the panel comes up, you sign in, and the empty state tells you which variable to set.

The listen port is `:80` and is not configurable. Remap it with a port binding, the same
stance vlessvmore takes.

## The CLI

```
vlessvmorectl serve
vlessvmorectl users add <username> [password]
vlessvmorectl users ls [--json]
vlessvmorectl users rm <username> [-y] [--force]
vlessvmorectl users passwd <username> [password]
vlessvmorectl version
```

These are *panel* logins. VPN users live on each node and are managed from the panel
itself.

Three ways to give a password:

```sh
vlessvmorectl users add alice hunter2          # warns: it is now in your shell history
vlessvmorectl users add alice                  # prompts, with echo off
echo hunter2 | vlessvmorectl users add alice --password-stdin
```

The CLI is the only writer of `admins.json`; `serve` only reads it, and notices an
out-of-band change within a second. So `docker exec … users add` takes effect on a running
panel with no restart, and `users passwd` signs that person out **everywhere,
immediately** — which is the entire point of changing a password after a suspected
compromise.

`users rm` refuses to remove the last administrator without `--force`, since that locks
everyone out of a running panel.

## Things worth knowing

**vlessvmore has no 401.** Every refusal it makes — no token, revoked token, unknown path,
wrong method — is an identical `404 page not found` in `text/plain`, padded to a fixed
60–150 ms, deliberately. Its genuine not-founds are JSON. That content type is the only
way to tell "your token is wrong" from "no such user", and this panel reads it: a rejected
token gets a message naming the environment variable to look at, not a shrug.

**A saved change is not necessarily a live one.** Changing a user rewrites the node's
sing-box config and reloads it, and the reload can fail on its own — the node answers 2xx
with `reloaded: false`. The panel shows a sticky banner in that case, not a toast that
disappears: an operator who has just disabled someone believes they are disconnected, and
until that clears they are not. The banner heals itself when any later reload succeeds.

**Reloads drop connections.** They are coalesced on the node, and this panel never reloads
on a timer — only when you change something, or press the retry button.

**`with_v2ray_api` or nothing.** If a node's sing-box was built without that tag there are
no per-user traffic counters at all: usage stays at zero and quotas never fire, silently.
The panel checks and says so on the server card.

**Traffic is counted in whole UTC hours**, so the newest bucket in a chart is always still
accumulating. It is drawn faded rather than as a cliff, because otherwise every graph
looks like it ends in an outage.

**Subscription and install URLs are capabilities.** Anyone holding one can connect as that
user. They are blurred until revealed, never logged, and rotating a subscription token
invalidates the old URL immediately without disconnecting anyone — the UUID is untouched.

**Sessions survive a restart**, so `docker compose restart` does not sign everyone out.
They live in `sessions.json` in the data directory, and what is stored is a *hash* of each
cookie value rather than the value — the same discipline vlessvmore uses for
`tokens.json`. Reading that file gets you a list of digests, not a list of credentials.
Signing out removes the record, so a revoked session does not come back from the dead
either.

**The session cookie is `Secure` only over TLS.** Making it unconditional is stricter, but
with the port fixed at `:80` its failure mode is a browser silently discarding the cookie
and a login that "succeeds" and then 401s forever, with no error anywhere. Instead a login
over plain HTTP from a non-loopback host logs a warning naming the consequence.

## Security

`/api/proxy` takes a URL from an authenticated client and fetches it with a credential
attached. That is what it is for; what keeps it from being a liability is the set of checks
around it:

- A session is required before the URL is even parsed.
- The URL's origin must be an **exact match** for a configured node — a map lookup, never
  a prefix comparison, because `https://vpn.example.com.attacker.test` has a configured URL
  as a prefix and is a domain anyone can register.
- Only `/api/` paths are proxied. `/sub/`, `/show/` and `/static/` are public capability
  URLs meant to be opened directly, so routing them through here would add reach without
  adding value.
- Only `GET`, `POST`, `PATCH`, `DELETE`.
- Redirects are never followed, so a redirect cannot carry the token off-origin.
- Request headers are an allowlist: the browser's own `Authorization` and `Cookie` never
  reach a node. Response headers are an allowlist of exactly `Content-Type`.
- Errors and logs route through a redaction helper. There are tests asserting that no
  response body and no log line ever contains a token.

Elsewhere: bcrypt at cost 12, a login rate limit per username plus a global cap (a slow
hash on an unauthenticated endpoint is a CPU amplifier), `SameSite=Lax` plus a
JSON-only-bodies rule and a `Sec-Fetch-Site` check for CSRF, and **no CORS middleware at
all** — its absence is load-bearing, and there is a test that fails if one appears.

`admins.json` holds only bcrypt hashes, at mode 600.

## Development

```sh
go test ./...            # includes the proxy's security surface
gofmt -l . && go vet ./...

cd web
npm ci
npm run typecheck
npm test
npm run build            # emits web/dist, which the Go binary embeds
```

`go build` works without Node installed: `web/dist/.gitkeep` is tracked so the embed
resolves, and a binary with no bundle serves a page explaining how to build one rather
than a blank screen.

### Binary size

The image is ~27 MB, of which the binary is ~7.3 MB. Three things get it there:

- `CGO_ENABLED=0 -trimpath -ldflags "-s -w -buildid="` — drops the symbol table, DWARF
  and build ID, and takes a plain build from 11 MB to 7.7 MB.
- The bundle is **gzipped before it is embedded** (684 KB → 224 KB), which is another
  ~450 KB off the binary and also means the compressed bytes are served straight from
  memory rather than being recompressed per request. `internal/api/spa.go` registers a
  `foo.js.gz` under `/foo.js`, so nothing downstream knows.
- A scratch-ish `alpine` runtime with only `ca-certificates` and `tzdata`.

UPX would roughly halve what remains and is deliberately not used: it decompresses the
whole binary into RAM at every start, defeats page-cache sharing between containers, and
gets flagged by enough security scanners to be a support burden. For a process that
starts once and runs for months, the trade is the wrong way round.

Running the whole thing locally against a real node:

```sh
# in vlessvmore/
docker compose up -d
TOKEN=$(docker exec vlessvmore vlessvmore token create panel --raw)
docker exec vlessvmore vlessvmore user add alice

# here
go run . users add admin hunter2hunter2
VLESSVMORE_SERVERS="http://localhost:8080|$TOKEN" go run . serve   # :80

cd web && npm run dev    # :5173, proxying /api to :80 so both look same-origin
```

## Not implemented

- **A client|proxy switch.** Proxying means this host is a single point of connectivity: a
  node your browser can reach but this host cannot is unmanageable. The seams for a
  per-node toggle are already in place — `Transport` is one interface with one
  implementation, the proxy passes upstream responses through verbatim so the client's
  error handling would not fork, and `unreachable.reason` already carries the
  browser-side values (`cors`, `mixed-content`, `offline`) with UI copy written. What is
  missing is the second `Transport`, the per-node preference, and returning tokens to the
  browser for the nodes it applies to.
- **Automatic fallback** from proxy to direct. Deliberately not done, and not planned: it
  is how you end up silently shipping bearer tokens to a browser because a node blipped.
- Creating or revoking a node's API tokens from the panel.
- Editing a node's `config.json` — it is read-only operator input, by design upstream.
- Multi-user audit trails beyond the log line each change writes.
- `VLESSVMORE_SERVERS_FILE`, for reading the token list from a Docker secret rather than
  the environment (where `docker inspect` and `/proc/1/environ` can see it).
