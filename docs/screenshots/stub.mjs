// Stand-in vlessvmore nodes, for screenshots.
//
// Serves the endpoints the panel actually calls, with data shaped like a real
// deployment: a mix of healthy, near-quota, expiring and disabled users, and traffic
// with a believable daily rhythm rather than noise. Nothing here talks to sing-box, so
// `capture.sh` needs no VPN and no root.
//
// Node stdlib only — the QR matrices come from the committed qr.json rather than an npm
// dependency. See README.md.
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const QR = JSON.parse(readFileSync(join(here, "qr.json"), "utf8"));

const HOUR = 3_600_000;
const GB = 1024 ** 3;
const SUB_BASE = "https://vpn.example.com";

const iso = (ms) => new Date(ms).toISOString().replace(/\.\d+Z$/, "Z");
const now = Date.now();
const days = (n) => iso(now + n * 86_400_000);
const ago = (n) => iso(now - n * 86_400_000);

/**
 * Traffic with a daily rhythm: quiet overnight, a morning ramp, an evening peak.
 * A flat random series looks like test data at a glance, which is exactly what a
 * screenshot should not look like.
 */
function series(fromUnix, hours, scale) {
  const out = [];
  for (let i = 0; i < hours; i++) {
    const t = fromUnix * 1000 + i * HOUR;
    const h = new Date(t).getUTCHours();
    const shape = h < 6 ? 0.08 : h < 11 ? 0.45 : h < 17 ? 0.7 : h < 23 ? 1 : 0.3;
    const jitter = 0.65 + ((Math.sin(i * 2.7) + 1) / 2) * 0.7;
    const total = Math.round(scale * shape * jitter);
    // The real node omits empty buckets rather than sending zeros.
    if (total < scale * 0.06) continue;
    out.push({ bucket: iso(t), up: Math.round(total * 0.18), down: Math.round(total * 0.82) });
  }
  return out;
}

function usage({ windowTotal, lifetime, quota }) {
  const up = Math.round(windowTotal * 0.17);
  return {
    up: Math.round(lifetime * 0.17),
    down: lifetime - Math.round(lifetime * 0.17),
    total: lifetime,
    window_up: up,
    window_down: windowTotal - up,
    window_total: windowTotal,
    quota_bytes: quota,
    quota_remaining: quota > 0 ? Math.max(0, quota - windowTotal) : 0,
  };
}

const NODES = {
  8801: {
    name: "Amsterdam",
    host: "vpn-nl.example.com",
    users: [
      { name: "alice", note: "laptop", quota: 0, win: 41.3 * GB, life: 214 * GB },
      { name: "alice-phone", note: "iPhone 15", quota: 100 * GB, win: 63.7 * GB, life: 88 * GB },
      { name: "bruno", quota: 250 * GB, win: 12.1 * GB, life: 12.1 * GB },
      { name: "camille", note: "contractor", quota: 50 * GB, win: 50 * GB, life: 137 * GB, enabled: false, reason: "quota" },
      { name: "dmitri", quota: 0, win: 7.4 * GB, life: 402 * GB, expires: days(4) },
      { name: "elena", note: "tablet", quota: 500 * GB, win: 188 * GB, life: 613 * GB },
      { name: "farid", note: "old laptop", quota: 0, win: 0, life: 3.2 * GB, enabled: false },
    ],
  },
  8802: {
    name: "Frankfurt",
    host: "vpn-de.example.com",
    users: [
      { name: "greta", quota: 200 * GB, win: 91.5 * GB, life: 340 * GB },
      { name: "hugo", note: "work", quota: 0, win: 22.8 * GB, life: 22.8 * GB },
      { name: "iris", quota: 100 * GB, win: 97.2 * GB, life: 97.2 * GB },
      { name: "jonas", note: "spare", quota: 0, win: 0.4 * GB, life: 0.4 * GB, expires: days(21) },
    ],
  },
  8803: {
    host: "vpn-sg.example.com",
    users: [
      { name: "kenji", quota: 0, win: 156 * GB, life: 1_204 * GB },
      { name: "lena", note: "travel", quota: 300 * GB, win: 45.9 * GB, life: 45.9 * GB },
      { name: "marco", note: "expired trial", quota: 20 * GB, win: 20 * GB, life: 20 * GB, enabled: false, reason: "expired", expires: ago(3) },
    ],
  },
};

function buildUsers(node) {
  return node.users.map((u, i) => {
    const subToken = `QK7M2X${Buffer.from(node.host + u.name).toString("base64url").slice(0, 20).toUpperCase()}`;
    return {
      id: `u_0B4X6TWQ8ZKM3N1PVJHR${String(i).padStart(2, "0")}`,
      name: u.name,
      uuid: `268e4039-6dd0-4d35-b279-b9763${String(i).padStart(2, "0")}d9eed3`,
      enabled: u.enabled !== false,
      quota_bytes: u.quota,
      ...(u.expires ? { expires_at: u.expires } : {}),
      usage_reset_at: ago(12),
      ...(u.reason ? { disabled_reason: u.reason } : {}),
      sub_token: subToken,
      ...(u.note ? { note: u.note } : {}),
      created_at: ago(40 + i * 6),
      updated_at: ago(i),
      usage: usage({ windowTotal: Math.round(u.win), lifetime: Math.round(u.life), quota: u.quota }),
      subscription_url: `${SUB_BASE}/sub/${subToken}`,
      install_url: `${SUB_BASE}/show/${subToken}`,
    };
  });
}

function start(port) {
  const node = NODES[port];
  const users = buildUsers(node);

  createServer((req, res) => {
    const url = new URL(req.url, `http://localhost:${port}`);
    const send = (body, status = 200) => {
      res.writeHead(status, { "content-type": "application/json; charset=utf-8" });
      res.end(JSON.stringify(body));
    };
    const find = (ref) => users.find((u) => u.id === ref || u.name === ref);

    if (url.pathname === "/api/server") {
      return send({
        // Omitted when unset, exactly as vlessvmore does — vpn-sg has no name, so the
        // panel falls back to its hostname and both paths appear in one screenshot.
        ...(node.name ? { name: node.name } : {}),
        host: node.host,
        port: 8443,
        sni: node.host,
        public_key: "Z0PwFQzd7TTduOXwDLQ7XePJXrtv6O7THdYg6aRloAo",
        short_id: "6048316bbc9ca90e",
        flow: "xtls-rprx-vision",
        fingerprint: "chrome",
        handshake: "caddy-caddy-1:443",
      });
    }

    if (url.pathname === "/api/status") {
      const active = users.filter((u) => u.enabled).length;
      return send({
        sing_box: {
          running: true,
          pid: 28,
          started_at: ago(6),
          config_path: "/var/lib/vlessvmore/sing-box.json",
          active_users: active,
          last_reload: iso(now - 42 * 60_000),
        },
        // Includes with_v2ray_api, so the panel does not show its "no per-user counters"
        // warning. Drop that tag here to screenshot the warning instead.
        sing_box_version:
          "sing-box version 1.13.14\n\nEnvironment: go1.24.13 linux/arm64\nTags: with_utls,with_v2ray_api,badlinkname",
        users: users.length,
        active_users: active,
        tokens: 1,
        data_dir: "/var/lib/vlessvmore",
      });
    }

    if (url.pathname === "/api/users") return send({ users });

    // A single user. The panel's list views never ask for this — they read /api/users —
    // but attaching an account to a subscriber verifies it here, and the share page reads
    // one of these per attached account. Without it both fall through to the stdlib 404
    // below, which the panel correctly but confusingly reports as a rejected token.
    const one = url.pathname.match(/^\/api\/users\/([^/]+)$/);
    if (one && req.method === "GET") {
      const u = find(one[1]);
      // JSON, not the stdlib page. That content type is the only thing distinguishing
      // "no such user" from "your token is wrong", and the panel reads it.
      if (!u) return send({ error: `user ${one[1]}: not found` }, 404);
      return send(u);
    }

    // Enough of PATCH to make the drawer's Save and its Enable/Disable button work when
    // somebody pokes at the demo.
    const patch = url.pathname.match(/^\/api\/users\/([^/]+)$/);
    if (patch && req.method === "PATCH") {
      const u = find(patch[1]);
      if (!u) return send({ error: `user ${patch[1]}: not found` }, 404);
      let body = "";
      req.on("data", (c) => (body += c));
      return req.on("end", () => {
        const fields = JSON.parse(body || "{}");
        if (fields.name !== undefined) u.name = fields.name;
        if (fields.note !== undefined) u.note = fields.note;
        if (fields.enabled !== undefined) u.enabled = fields.enabled;
        if (fields.quota_bytes !== undefined) {
          u.quota_bytes = fields.quota_bytes;
          u.usage.quota_bytes = fields.quota_bytes;
          u.usage.quota_remaining =
            fields.quota_bytes > 0 ? Math.max(0, fields.quota_bytes - u.usage.window_total) : 0;
        }
        // expires_at is three-valued: absent leaves it, null clears it, a string sets it.
        if ("expires_at" in fields) {
          if (fields.expires_at === null) delete u.expires_at;
          else u.expires_at = fields.expires_at;
        }
        u.updated_at = iso(Date.now());
        send({ result: u, reloaded: true });
      });
    }

    const link = url.pathname.match(/^\/api\/users\/([^/]+)\/link$/);
    if (link) {
      const u = find(link[1]);
      if (!u) return send({ error: `user ${link[1]}: not found` }, 404);
      return send({
        user_id: u.id,
        name: u.name,
        link:
          `vless://${u.uuid}@${node.host}:8443?type=tcp&encryption=none&flow=xtls-rprx-vision` +
          `&packetEncoding=xudp&security=reality&sni=${node.host}&fp=chrome` +
          `&pbk=Z0PwFQzd7TTduOXwDLQ7XePJXrtv6O7THdYg6aRloAo&sid=6048316bbc9ca90e#${u.name}`,
        subscription_url: u.subscription_url,
        install_url: u.install_url,
        qr: QR.vless,
        subscription_qr: QR.subscription,
      });
    }

    const usageMatch = url.pathname.match(/^\/api\/users\/([^/]+)\/usage$/);
    if (usageMatch) {
      const u = find(usageMatch[1]);
      if (!u) return send({ error: `user ${usageMatch[1]}: not found` }, 404);
      const fromUnix = Number(url.searchParams.get("from"));
      const bucket = url.searchParams.get("bucket") ?? "hour";
      const hours = Math.max(1, Math.round((now / 1000 - fromUnix) / 3600) + 1);
      return send({
        user_id: u.id,
        name: u.name,
        from: iso(fromUnix * 1000),
        to: iso(now),
        bucket,
        series: series(fromUnix, hours, u.usage.window_total / 26),
        summary: u.usage,
      });
    }

    if (url.pathname === "/api/tokens") {
      return send({
        tokens: [{ id: "t_0B4X6TWQ8ZKM", label: "panel", created_at: ago(40), last_used_at: iso(now) }],
      });
    }

    // Everything the real node refuses looks like this.
    res.writeHead(404, { "content-type": "text/plain; charset=utf-8" });
    res.end("404 page not found\n");
  }).listen(port, () => console.log(`stub ${node.host} on :${port}`));
}

for (const port of Object.keys(NODES)) start(Number(port));
