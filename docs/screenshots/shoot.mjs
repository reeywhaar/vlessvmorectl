// Screenshot the panel by driving headless Chromium over the DevTools protocol.
//
// No Playwright and no browser download: Chromium ships its own headless mode, and Node
// has a WebSocket client built in, so the whole driver is the handful of CDP calls below.
//
// Invoked by capture.sh, which starts everything this expects. Environment:
//   PANEL           base URL of a running panel      (default http://127.0.0.1)
//   SESSION_COOKIE  value of a valid vlessvmore_auth cookie
//   OUT             directory to write PNGs into     (default .)
//   WIDTH           CSS width to render at           (default 600)
//   THEME           dark | light                     (default dark)
//   SHARE_TOKEN     a subscriber's token, for the signed-out share page (optional)
import { writeFileSync } from "node:fs";
import { setTimeout as sleep } from "node:timers/promises";

const PANEL = process.env.PANEL ?? "http://127.0.0.1";
const OUT = process.env.OUT ?? ".";
const COOKIE = process.env.SESSION_COOKIE;
const WIDTH = Number(process.env.WIDTH ?? 600);
const THEME = process.env.THEME ?? "dark";
/** A subscriber's share token, for the signed-out page. Optional; the shot is skipped
 *  without one, so this script still runs against a panel with no subscribers. */
const SHARE_TOKEN = process.env.SHARE_TOKEN ?? "";

if (!COOKIE) throw new Error("SESSION_COOKIE is required");

const target = await (async () => {
  for (let i = 0; i < 50; i++) {
    try {
      const list = await (await fetch("http://127.0.0.1:9222/json/list")).json();
      const page = list.find((t) => t.type === "page");
      if (page?.webSocketDebuggerUrl) return page;
    } catch {
      /* not up yet */
    }
    await sleep(200);
  }
  throw new Error("chromium devtools endpoint never came up on :9222");
})();

const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((ok, bad) => {
  ws.onopen = ok;
  ws.onerror = bad;
});

let nextId = 1;
const pending = new Map();
const listeners = new Map();

ws.onmessage = (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result);
  } else if (msg.method && listeners.has(msg.method)) {
    listeners.get(msg.method).forEach((fn) => fn(msg.params));
    listeners.delete(msg.method);
  }
};

const send = (method, params = {}) =>
  new Promise((resolve, reject) => {
    const id = nextId++;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params }));
  });

const once = (method) =>
  new Promise((resolve) => {
    if (!listeners.has(method)) listeners.set(method, []);
    listeners.get(method).push(resolve);
  });

async function evaluate(expression) {
  const { result, exceptionDetails } = await send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
  });
  if (exceptionDetails) throw new Error(`${exceptionDetails.text}\n${expression}`);
  return result.value;
}

/**
 * Poll for a selector rather than sleeping a fixed amount: the panel suspends on its own
 * fetches, and a fixed wait is either flaky or slow.
 *
 * The selector goes in as a variable, not interpolated into the string — one containing
 * double quotes would otherwise break the quoting and turn the whole expression into a
 * parse error, which surfaces as a mysterious immediate failure rather than a timeout.
 */
const waitFor = (selector, timeout = 15000) =>
  evaluate(`
    new Promise((resolve, reject) => {
      const sel = ${JSON.stringify(selector)};
      const deadline = Date.now() + ${timeout};
      (function poll() {
        if (document.querySelector(sel)) return resolve(true);
        if (Date.now() > deadline) return reject(new Error("timeout waiting for " + sel));
        setTimeout(poll, 100);
      })();
    })`);

const clickText = (selector, text) =>
  evaluate(`
    (() => {
      const sel = ${JSON.stringify(selector)}, want = ${JSON.stringify(text)};
      const el = [...document.querySelectorAll(sel)].find(e => e.textContent.trim().includes(want));
      if (!el) throw new Error("no " + sel + " containing " + want);
      el.click();
      return true;
    })()`);

const setViewport = (height) =>
  send("Emulation.setDeviceMetricsOverride", {
    width: WIDTH,
    height: Math.round(height),
    deviceScaleFactor: 2,
    mobile: false,
  });

/**
 * Size the viewport to the content, so a screenshot is not mostly empty page.
 *
 * Shrinks before measuring. The shell is `min-h-full`, so scrollHeight never reports
 * less than the current viewport — measuring at the height we happen to be at just
 * returns that height back, and the capture comes out padded with blank page.
 */
async function fitHeight(min = 420, max = 2600) {
  await setViewport(min);
  await sleep(150);

  const h = await evaluate(
    `Math.max(document.documentElement.scrollHeight, document.body.scrollHeight)`,
  );
  await setViewport(Math.min(max, Math.max(min, Math.ceil(h))));
  await sleep(250); // let the layout settle at the new height
}

/**
 * Size the viewport to a modal's content.
 *
 * The drawer is a <dialog> sized to the viewport with its own internal scroll, so the
 * document's scrollHeight says nothing about how tall its contents are — fitHeight would
 * crop it at whatever the viewport happened to be, losing the usage chart entirely.
 *
 * Grow first so nothing is clipped, then measure where the content actually ends.
 *
 * Note that it measures the *children's* bounding boxes, not the scroller's scrollHeight.
 * Once the viewport has been grown the content no longer overflows, so scrollHeight
 * collapses to clientHeight — which is the tall viewport we just set, and the capture
 * comes out padded with half a screen of empty sheet.
 *
 * The scroller is found by computed overflow rather than by class name, so restyling the
 * drawer does not silently break the capture.
 */
async function fitDialogHeight(max = 2600) {
  await setViewport(max);
  await sleep(400);

  const h = await evaluate(`(() => {
    const dialog = document.querySelector("dialog[open]");
    if (!dialog) return 0;
    const scroller = [...dialog.querySelectorAll("*")]
      .find(el => getComputedStyle(el).overflowY === "auto");
    if (!scroller || !scroller.children.length) return dialog.scrollHeight;

    const bottom = Math.max(...[...scroller.children].map(el => el.getBoundingClientRect().bottom));
    const padding = parseFloat(getComputedStyle(scroller).paddingBottom) || 0;
    return Math.ceil(bottom - dialog.getBoundingClientRect().top + padding);
  })()`);

  await setViewport(Math.min(max, Math.max(420, h)));
  await sleep(300);
}

async function shot(name) {
  await evaluate("new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))");
  const { data } = await send("Page.captureScreenshot", { format: "png" });
  writeFileSync(`${OUT}/${name}.png`, Buffer.from(data, "base64"));
  console.log(`  wrote ${name}.png`);
}

await send("Page.enable");
await send("Runtime.enable");
await send("Network.enable");

// Headless Chromium reports prefers-color-scheme: light. The panel honours the system
// preference, so without this every screenshot comes out in the light theme.
await send("Emulation.setEmulatedMedia", {
  features: [{ name: "prefers-color-scheme", value: THEME }],
});
await send("Emulation.setDeviceMetricsOverride", {
  width: WIDTH,
  height: 800,
  deviceScaleFactor: 2,
  mobile: false,
});

const navigate = async (path) => {
  const loaded = once("Page.loadEventFired");
  await send("Page.navigate", { url: PANEL + path });
  await loaded;
};

// --- 1. signed out ---
await navigate("/");
await waitFor('input[autocomplete="username"]');
await sleep(400);
await fitHeight();
await shot("login");

// Sign in by setting the cookie rather than typing into the form: fewer moving parts,
// and the form itself is already captured above.
await send("Network.setCookie", {
  name: "vlessvmore_auth",
  value: COOKIE,
  domain: new URL(PANEL).hostname,
  path: "/",
  httpOnly: true,
});

// --- 2. every managed node ---
await navigate("/");
await waitFor("main a[href^='/servers/']");
await sleep(1000); // let each card's queries settle so no skeleton is caught mid-flight
await fitHeight();
await shot("overview");

// --- 3. one node's users ---
await clickText("main a[href^='/servers/']", "vpn-nl.example.com");
await waitFor("table tbody tr");
await sleep(1000);
await fitHeight();
await shot("server");

// --- 4. the user drawer ---
await clickText("table tbody tr", "alice-phone");
await waitFor("dialog svg[role='img']");
await sleep(1600); // the usage chart is a lazily loaded chunk
await fitDialogHeight();
await shot("user");

// --- 5. the people a panel hands accounts to ---
await navigate("/subscribers");
await waitFor("main table tbody tr");
await sleep(1000);
await fitHeight();
await shot("subscribers");

// --- 6. the share page, signed out ---
//
// The cookie is cleared first, and that is the point of the shot rather than a detail of
// it: this page is a separate bundle with no session, and capturing it while signed in
// would prove nothing about whether it works for the person it is meant for.
if (SHARE_TOKEN) {
  await send("Network.clearBrowserCookies");
  await navigate(`/access/${SHARE_TOKEN}`);
  await waitFor("main svg[role='img']");
  await sleep(1000);
  await fitHeight();
  await shot("access");
} else {
  console.log("  skipping access.png: no SHARE_TOKEN");
}

ws.close();
console.log("done");
process.exit(0);
