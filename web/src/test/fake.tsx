import { render, type RenderResult } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import type { ReactNode } from "react";

import { ApiProvider } from "../api/ApiProvider";
import { ApiDispatcher } from "../api/dispatcher";
import { makeQueryClient } from "../queries/client";
import { ReloadWatch, ReloadWatchProvider } from "../queries/reloadWatch";
import type { Method, RequestOptions, Transport } from "../api/transport";
import type {
  Passkey,
  QRMatrix,
  Server,
  ServerInfo,
  ServerStatus,
  Subscriber,
  UsageSeries,
  UserLink,
  VlessUser,
} from "../api/types";

/**
 * A stand-in backend, shared by the component tests.
 *
 * A hand-rolled Transport rather than msw — nothing here exercises the real network, and
 * transport.ts exists precisely as this seam. It records every request, so a test can
 * assert what was *not* called; several properties worth protecting here are absences.
 */

export const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json; charset=utf-8" },
  });

/** The stdlib page vlessvmore returns for every refusal, including a rejected token. */
export const stdlib404 = () =>
  new Response("404 page not found", {
    status: 404,
    headers: { "content-type": "text/plain; charset=utf-8" },
  });

export const QR: QRMatrix = { size: 2, rows: ["10", "01"], quiet_zone: 4 };

export function makeServer(over: Partial<Server> = {}): Server {
  return { id: "aaa111", url: "https://ams.example.com", ...over };
}

export function makeUser(over: Partial<VlessUser> = {}): VlessUser {
  return {
    id: "u_alice",
    name: "alice-phone",
    uuid: "5c8586b1-81bc-487f-bb5e-b502347a52d4",
    enabled: true,
    quota_bytes: 0,
    usage_reset_at: "2026-07-01T00:00:00Z",
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    usage: {
      up: 1_000,
      down: 9_000,
      total: 10_000,
      window_up: 1_000,
      window_down: 9_000,
      window_total: 10_000,
      quota_bytes: 0,
      quota_remaining: 0,
    },
    ...over,
  };
}

export function makeSubscriber(over: Partial<Subscriber> = {}): Subscriber {
  return {
    id: "sub1",
    name: "Ivan",
    token: "QK7M2XA9TESTTKEN0123456789ABCDEF",
    access_path: "/access/QK7M2XA9TESTTKEN0123456789ABCDEF",
    disabled: false,
    entries: [],
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...over,
  };
}

const serverInfo = (name: string, host: string): ServerInfo => ({
  name,
  host,
  port: 8443,
  sni: host,
  public_key: "PUBKEY",
  short_id: "ab12",
  flow: "xtls-rprx-vision",
  fingerprint: "chrome",
  handshake: `${host}:443`,
});

const serverStatus = (users: VlessUser[]): ServerStatus => ({
  sing_box: {
    running: true,
    pid: 42,
    started_at: "2026-07-27T00:00:00Z",
    config_path: "/etc/sing-box/config.json",
    active_users: users.filter((u) => u.enabled).length,
    last_reload: "2026-07-27T00:00:00Z",
  },
  sing_box_version: "sing-box 1.13.14\nTags: with_utls,with_v2ray_api",
  users: users.length,
  active_users: users.filter((u) => u.enabled).length,
  tokens: 1,
  data_dir: "/var/lib/vlessvmore",
});

/** What the fake will report as a user's link, so tests can assert on it exactly. */
export const expectedLink = (u: VlessUser, server: Server) =>
  `vless://${u.uuid}@${new URL(server.url).host}:8443?type=tcp#${u.name}`;

const userLink = (u: VlessUser, host: string, over: Partial<UserLink> = {}): UserLink => ({
  user_id: u.id,
  name: u.name,
  link: `vless://${u.uuid}@${host}:8443?type=tcp#${u.name}`,
  subscription_url: `https://${host}/sub/SUBTOKEN${u.id.toUpperCase()}`,
  install_url: `https://${host}/show/SUBTOKEN${u.id.toUpperCase()}`,
  qr: QR,
  subscription_qr: QR,
  ...over,
});

const usageSeries = (u: VlessUser): UsageSeries => ({
  user_id: u.id,
  name: u.name,
  from: "2026-07-27T00:00:00Z",
  to: "2026-07-28T00:00:00Z",
  bucket: "hour",
  // Deliberately empty. The chart is a lazily-loaded recharts chunk that measures its own
  // container, and jsdom gives it none — a test that asserted on bars would be asserting
  // on nothing. What matters here is that the drawer resolves rather than suspending
  // forever, which an empty series does just as well.
  series: [],
  summary: u.usage!,
});

export interface FakeOptions {
  servers?: Server[];
  /** Per server id. */
  users?: Record<string, VlessUser[]>;
  subscribers?: Subscriber[];
  /** Per server id: make that node fail every call. */
  down?: string[];
  /** Overrides for a user's link response, by user id. */
  links?: Record<string, Partial<UserLink>>;
  /** Overrides for a node's /api/server response, by server id — its display name mostly. */
  infos?: Record<string, Partial<ServerInfo>>;
  /** As VLESSVMORE_PASSKEY_ORIGIN being set: /api/me says so and the endpoints answer. */
  passkeysEnabled?: boolean;
  passkeys?: Passkey[];
}

export function makePasskey(over: Partial<Passkey> = {}): Passkey {
  return {
    id: "k7m2xa9v",
    label: "iPhone",
    algorithm: "ES256",
    // Apple Passwords, which is what an iPhone enrolling a passkey reports. All of these are
    // optional on the wire, so a test for an authenticator that identified itself poorly, or not
    // at all, overrides them. No logo_dark, like most providers.
    provider: "Apple Passwords",
    aaguid: "fbfc3007-154e-4ecc-8c0b-6e020557d7bd",
    logo: "/assets/icons/webauthn/apple_passwords_light.9d2487ab5b1d.svg",
    synced: true,
    created_at: "2026-07-01T09:12:33Z",
    ...over,
  };
}

/** The current password the fake backend accepts when re-authenticating. */
export const FAKE_PASSWORD = "hunter2hunter2";

export interface Fake {
  transport: Transport;
  /** Every request, as "PANEL GET /api/servers" or "NODE aaa111 GET /api/users". */
  calls: string[];
  bodies: unknown[];
}

export function makeFake(opts: FakeOptions = {}): Fake {
  const servers = opts.servers ?? [makeServer()];
  const usersByServer = opts.users ?? { [servers[0]!.id]: [makeUser()] };
  let subscribers = opts.subscribers ?? [];
  const down = new Set(opts.down ?? []);

  const calls: string[] = [];
  const bodies: unknown[] = [];

  // Account state, so a rename is visible from /api/me afterwards the way it is in the
  // real backend.
  let username = "alice";

  /** 403 for a wrong current password, matching the backend's distinction from 401. */
  const reauth = (body: unknown) =>
    (body as { current_password?: string }).current_password === FAKE_PASSWORD
      ? null
      : json({ error: "that is not your current password" }, 403);

  let passkeys = opts.passkeys ?? [];

  // The option objects only have to be shaped right, not cryptographically real: the
  // authenticator on the other side is stubbed too. What these tests are for is the wiring —
  // that buffers are decoded before reaching the browser and encoded before reaching us.
  const passkeyRoute = (method: Method, path: string, o: RequestOptions): Response => {
    if (path === "/api/passkeys" && method === "GET") return json({ passkeys });
    if (path === "/api/passkeys/register/begin") {
      return json({
        state: "reg-state",
        options: {
          challenge: "Y2hhbGxlbmdl",
          rp: { id: "panel.example.com", name: "vlessvmore panel" },
          user: { id: "dXNlci1oYW5kbGU", name: username, displayName: username },
          pubKeyCredParams: [{ type: "public-key", alg: -7 }],
          excludeCredentials: passkeys.map(() => ({
            id: "ZXhpc3Rpbmc",
            type: "public-key",
            transports: ["internal"],
          })),
        },
      });
    }
    if (path === "/api/passkeys/register/finish") {
      // No label, like the real one: enrolment stores no name, and the panel calls a credential
      // after its provider until somebody renames it.
      const created = makePasskey({ id: `pk${passkeys.length + 1}`, label: "" });
      passkeys = [...passkeys, created];
      return json({ passkey: created }, 201);
    }
    if (path === "/api/passkeys/login/begin") {
      return json({ state: "login-state", options: { challenge: "Y2hhbGxlbmdl" } });
    }
    if (path === "/api/passkeys/login/finish") {
      return json({ user: { username } });
    }
    const m = /^\/api\/passkeys\/([^/]+)$/.exec(path);
    if (m) {
      const id = decodeURIComponent(m[1]!);
      const found = passkeys.find((p) => p.id === id);
      if (!found) return json({ error: "not found" }, 404);
      if (method === "DELETE") {
        passkeys = passkeys.filter((p) => p.id !== id);
        return new Response(null, { status: 204 });
      }
      const renamed = { ...found, label: String((o.body as { label: string }).label) };
      passkeys = passkeys.map((p) => (p.id === id ? renamed : p));
      return json({ passkey: renamed });
    }
    return json({ error: "no such endpoint: " + path }, 404);
  };

  const transport: Transport = {
    panel(method: Method, path: string, o: RequestOptions = {}) {
      calls.push(`PANEL ${method} ${path}`);
      if (o.body !== undefined) bodies.push(o.body);

      if (path === "/api/me") {
        return Promise.resolve(
          json({
            username,
            expires_at: "2026-08-06T00:00:00Z",
            passkeys_enabled: opts.passkeysEnabled ?? false,
          }),
        );
      }
      // Absent unless configured, exactly as the real router leaves them unregistered.
      if (path.startsWith("/api/passkeys")) {
        if (!opts.passkeysEnabled) {
          return Promise.resolve(json({ error: "no such endpoint: " + path }, 404));
        }
        return Promise.resolve(passkeyRoute(method, path, o));
      }
      // No re-auth here, matching the real backend: a username is not a secret.
      if (path === "/api/account/username") {
        username = String((o.body as { username: string }).username);
        return Promise.resolve(json({ username }));
      }
      if (path === "/api/account/password") {
        const bad = reauth(o.body);
        if (bad) return Promise.resolve(bad);
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === "/api/servers") return Promise.resolve(json({ servers }));
      if (path === "/api/subscribers") {
        if (method === "GET") return Promise.resolve(json({ subscribers }));
        const created = makeSubscriber({ id: "new", name: String((o.body as { name: string }).name) });
        subscribers = [...subscribers, created];
        return Promise.resolve(json(created, 201));
      }
      return Promise.resolve(json({ error: "no such endpoint: " + path }, 404));
    },

    node(server: Server, method: Method, path: string, o: RequestOptions = {}) {
      calls.push(`NODE ${server.id} ${method} ${path}`);
      if (o.body !== undefined) bodies.push(o.body);

      if (down.has(server.id)) {
        // What the panel's proxy emits when it cannot reach a node at all.
        return Promise.resolve(
          new Response(JSON.stringify({ error: "connection refused", proxy_error: "refused" }), {
            status: 502,
            headers: { "content-type": "application/json", "x-proxy-error": "1" },
          }),
        );
      }

      const users = usersByServer[server.id] ?? [];
      const host = new URL(server.url).host;

      if (path === "/api/server")
        return Promise.resolve(json({ ...serverInfo(`[NL] ${host}`, host), ...opts.infos?.[server.id] }));
      if (path === "/api/status") return Promise.resolve(json(serverStatus(users)));
      if (path === "/api/users") {
        if (method !== "POST") return Promise.resolve(json({ users }));
        const { name } = o.body as { name: string };
        // The node's own uniqueness rule, which is what makes a name collide.
        if (users.some((u) => u.name === name)) {
          return Promise.resolve(json({ error: `user ${name} already exists` }, 409));
        }
        const created = makeUser({ id: `u_${name}`, name });
        usersByServer[server.id] = [...users, created];
        return Promise.resolve(json({ result: created, reloaded: true }, 201));
      }

      const m = /^\/api\/users\/([^/]+)(\/link|\/usage)?$/.exec(path);
      if (m) {
        const u = users.find((x) => x.id === decodeURIComponent(m[1]!));
        if (!u) return Promise.resolve(json({ error: "not found" }, 404));
        if (m[2] === "/link") return Promise.resolve(json(userLink(u, host, opts.links?.[u.id])));
        if (m[2] === "/usage") return Promise.resolve(json(usageSeries(u)));
        return Promise.resolve(json(u));
      }
      return Promise.resolve(stdlib404());
    },
  };

  return { transport, calls, bodies };
}

/**
 * Renders `ui` inside the same provider stack main.tsx builds, at `route`.
 *
 * `path` is the route pattern, so a component using useParams sees what it would in the
 * real app rather than undefined.
 */
export function renderAt(
  ui: ReactNode,
  { fake, route = "/", path = "/" }: { fake: Fake; route?: string; path?: string },
): RenderResult {
  return render(
    <ApiProvider dispatcher={new ApiDispatcher(fake.transport)}>
      <QueryClientProvider client={makeQueryClient()}>
        <ReloadWatchProvider value={new ReloadWatch()}>
          <MemoryRouter initialEntries={[route]}>
            <Routes>
              <Route path={path} element={ui} />
            </Routes>
          </MemoryRouter>
        </ReloadWatchProvider>
      </QueryClientProvider>
    </ApiProvider>,
  );
}
