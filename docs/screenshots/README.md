# Screenshots

The images in the project README. Real captures of the real panel — only the data behind
them is invented.

| file | what it shows |
| --- | --- |
| `login.png` | the sign-in screen |
| `overview.png` | every managed node, with counts and traffic |
| `server.png` | one node's users, problems sorted first |
| `user.png` | the user drawer: QR, credentials, quota, traffic history |

## Regenerating

```sh
docs/screenshots/capture.sh
```

That builds the frontend and the binary, starts three stand-in nodes and a panel against
them, drives headless Chromium over the DevTools protocol, and overwrites the PNGs here.
Everything it starts is stopped again on the way out, including on failure.

Needs `go`, `node`, and Chromium or Chrome. No VPN, no Docker, no npm packages beyond the
frontend's own — and no Playwright, since Chromium has a headless mode and Node has a
WebSocket client.

It does need to bind `:80`, because that is where the panel listens and the port is not
configurable. Fine on macOS; on Linux that means root, or
`sudo setcap cap_net_bind_service=+ep` on the built binary.

Two knobs:

```sh
WIDTH=900 docs/screenshots/capture.sh     # default 600
THEME=light docs/screenshots/capture.sh   # default dark
```

600 CSS pixels at `deviceScaleFactor: 2` gives 1200-pixel-wide files that stay crisp on a
retina display and still sit sensibly in a README. Height is measured from the content, so
none of them carry a screenful of empty page.

At 600 px the user table scrolls horizontally and its `Lifetime` column sits off-screen.
That is genuinely what a narrow window looks like, not a capture artefact; render wider if
you want the whole table.

## The pieces

**`stub.mjs`** — three stand-in vlessvmore nodes on ports 8801–8803, serving the endpoints
the panel calls. The users are a deliberate spread: over quota, expiring, disabled by hand,
unlimited, nearly full. Traffic follows a daily rhythm rather than being random, because a
flat noisy series reads as test data at a glance.

Node stdlib only. It has no npm dependencies because the QR codes come from `qr.json`
rather than an encoder.

**`shoot.mjs`** — the Chromium driver. Notable bits, all of which were bugs first:

- It emulates `prefers-color-scheme: dark`. Headless Chromium reports light, and the panel
  honours the system preference, so without this every capture came out in the light theme.
- `fitHeight` shrinks the viewport *before* measuring. The shell is `min-h-full`, so
  `scrollHeight` never reports less than the current viewport.
- `fitDialogHeight` measures the drawer's children rather than its `scrollHeight`. Once the
  viewport is tall enough the content stops overflowing and `scrollHeight` collapses to the
  viewport height.
- Selectors are passed into the page as variables, never interpolated into a string. One
  containing a double quote turns the whole expression into a parse error, which surfaces
  as an immediate failure rather than a timeout.

**`qr.json`** — QR bit matrices for the demo data, in the shape vlessvmore returns.
Committed rather than computed so `stub.mjs` needs no dependency. To regenerate:

```sh
npx --yes @paulmillr/qr --help >/dev/null   # any QR library will do
node --input-type=module -e '
import { encodeQR } from "@paulmillr/qr";
const m = t => { const b = encodeQR(t, "raw");
  return { size: b.length, rows: b.map(r => r.map(d => d ? "1" : "0").join("")), quiet_zone: 4 }; };
console.log(JSON.stringify({
  subscription: m("https://vpn.example.com/sub/QK7M2XVCNULMV0EXAMPLE"),
  vless: m("vless://…"),
}, null, 2));'
```
