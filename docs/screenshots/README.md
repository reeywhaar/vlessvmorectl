# Screenshots

The images in the project README. Real captures of the real panel — only the data behind
them is invented.

| file | what it shows |
| --- | --- |
| `login.png` | the sign-in screen |
| `overview.png` | every managed node, with counts and traffic |
| `server.png` | one node's users, problems sorted first |
| `user.png` | the user drawer: credentials, quota, who holds it, traffic history |
| `subscribers.png` | the people the panel hands accounts to |
| `access.png` | a subscriber's own page, signed out — every account they hold |

## Regenerating

```sh
docs/screenshots/capture.sh
```

That builds the frontend and the binary, starts three stand-in nodes and a panel against
them, seeds two subscribers through the panel's own API, drives headless Chromium over the
DevTools protocol, and overwrites the PNGs here. Everything it starts is stopped again on
the way out, including on failure.

Needs `go`, `node`, and Chromium or Chrome. No VPN, no Docker, no npm packages beyond the
frontend's own — and no Playwright, since Chromium has a headless mode and Node has a
WebSocket client.

It does need to bind `:80`, because that is where the panel listens and the port is not
configurable. Fine on macOS; on Linux that means root, or
`sudo setcap cap_net_bind_service=+ep` on the built binary.

Two knobs:

```sh
WIDTH=900 docs/screenshots/capture.sh     # default 720
THEME=light docs/screenshots/capture.sh   # default dark
```

720 CSS pixels at `deviceScaleFactor: 2` gives 1440-pixel-wide files that stay crisp on a
retina display and still sit sensibly two-to-a-row in a README. Height is measured from the
content, so none of them carry a screenful of empty page.

**Do not drop below 640** without meaning to. That is the panel's `sm` breakpoint: under it
the header folds its tabs into a burger menu, and a set of screenshots in which every one
hides the navigation sells the panel short. The old 600 px default did exactly that. It is
also the width at which the user table starts scrolling horizontally and its `Lifetime`
column goes off-screen — genuinely what a narrow window looks like, but not what a reader
came to the README to see.

## The pieces

**`stub.mjs`** — three stand-in vlessvmore nodes on ports 8801–8803, serving the endpoints
the panel calls. The users are a deliberate spread: over quota, expiring, disabled by hand,
unlimited, nearly full. Traffic follows a daily rhythm rather than being random, because a
flat noisy series reads as test data at a glance.

`bellamy-phone` on :8801 and `bellamy-laptop` on :8802 are one person on two nodes, and
`capture.sh` attaches exactly those two to a subscriber — by name, so renaming one without
the other fails the run instead of quietly producing an empty share page. The drawer shot
opens `bellamy-phone` for the same reason.

Node stdlib only. It has no npm dependencies because the QR codes come from `qr.json`
rather than an encoder.

**`shoot.mjs`** — the Chromium driver. Notable bits, all of which were bugs first:

- It emulates `prefers-color-scheme: dark`. Headless Chromium reports light, and the panel
  honours the system preference, so without this every capture came out in the light theme.
- Nothing waits on a QR code any more. Both the drawer and the share page put their codes
  behind a button — a QR is the one credential a bystander can photograph off a shared
  screen — so a wait for one is a guaranteed 15-second timeout. The credential rows are the
  hook instead, and they arrive with the last of each page's fetches.
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
