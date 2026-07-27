import { createApiDispatcherAction } from "../request";
import type { UserCreate, UserPatch } from "../patch";
import type { DeletedUser, Reloaded, Server, UsageSeries, UserLink, VlessUser } from "../types";

const base = "/api/users";
const byId = (id: string) => `${base}/${encodeURIComponent(id)}`;

/**
 * GET /api/users — always with usage, which is what every screen needs.
 *
 * The `.then` is why actions are curried: unwrapping the envelope is ordinary code whose
 * result type is inferred, rather than a hand-typed `select` callback.
 */
export function getUsers(server: Server) {
  return createApiDispatcherAction((d) =>
    d
      .call<{ users: VlessUser[] }>({
        scope: server,
        method: "GET",
        path: base,
        query: { include: "usage" },
      })
      .then((r) => r.users),
  );
}

/** POST /api/users */
export function postUsers(server: Server, input: UserCreate) {
  return createApiDispatcherAction((d) =>
    d.call<Reloaded<VlessUser>>(
      { scope: server, method: "POST", path: base, body: input.toBody() }
    ),
  );
}

/** GET /api/users/:id */
export function getUsersById(server: Server, id: string) {
  return createApiDispatcherAction((d) =>
    d.call<VlessUser>({ scope: server, method: "GET", path: byId(id) }),
  );
}

/** PATCH /api/users/:id */
export function patchUsersById(server: Server, id: string, patch: UserPatch) {
  return createApiDispatcherAction((d) =>
    d.call<Reloaded<VlessUser>>(
      { scope: server, method: "PATCH", path: byId(id), body: patch.toBody() }
    ),
  );
}

/** DELETE /api/users/:id — takes the user's usage history with it, irreversibly. */
export function deleteUsersById(server: Server, id: string) {
  return createApiDispatcherAction((d) =>
    d.call<Reloaded<DeletedUser>>({ scope: server, method: "DELETE", path: byId(id) }),
  );
}

/**
 * POST /api/users/:id/reset-usage
 *
 * Starts a new quota window at now and re-enables the user if they were disabled *for
 * quota*. History is not deleted — the traffic still happened, it just stops counting.
 * Someone disabled by hand stays disabled.
 */
export function postUsersByIdResetUsage(server: Server, id: string) {
  return createApiDispatcherAction((d) =>
    d.call<Reloaded<VlessUser>>(
      { scope: server, method: "POST", path: `${byId(id)}/reset-usage` }
    ),
  );
}

/**
 * POST /api/users/:id/rotate-sub
 *
 * Bare, not wrapped in Reloaded — no reload happens, because sing-box's config does not
 * depend on the subscription token. That is what makes this the right way to cut off a
 * leaked subscription URL: it invalidates the URL without disconnecting anyone.
 */
export function postUsersByIdRotateSub(server: Server, id: string) {
  return createApiDispatcherAction((d) =>
    d.call<VlessUser>({ scope: server, method: "POST", path: `${byId(id)}/rotate-sub` }),
  );
}

/** GET /api/users/:id/link — the vless:// URI, both user-facing URLs, and a QR matrix. */
export function getUsersByIdLink(server: Server, id: string) {
  return createApiDispatcherAction((d) =>
    d.call<UserLink>({ scope: server, method: "GET", path: `${byId(id)}/link` }),
  );
}

/**
 * GET /api/users/:id/usage
 *
 * Requires a vlessvmore that returns `"series": []` rather than `null` for a user with
 * no traffic. Older builds return a nil slice there, which marshals as null.
 */
export function getUsersByIdUsage(
  server: Server,
  id: string,
  fromUnix: number,
  bucket: "hour" | "day",
) {
  return createApiDispatcherAction((d) =>
    d.call<UsageSeries>({
      scope: server,
      method: "GET",
      path: `${byId(id)}/usage`,
      // `to` is deliberately omitted, letting the node default it to now. An explicit
      // `to` would change the URL on every poll, minting a fresh cache entry each tick
      // and defeating both keepPreviousData and the proxy's request coalescing.
      query: { from: fromUnix, bucket },
    }),
  );
}
