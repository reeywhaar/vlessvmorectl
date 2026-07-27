import { createApiDispatcherAction } from "../request";
import type { Session } from "../types";

/**
 * Our own backend's session endpoints. Never a managed node — these are scoped "panel".
 */

/** POST /api/login */
export function postLogin(username: string, password: string) {
  return createApiDispatcherAction((d) =>
    d
      .call<{ user: { username: string } }>(
        { scope: "panel", method: "POST", path: "/api/login", body: { username, password } }
      )
      .then((r) => r.user),
  );
}

/** POST /api/logout — 204 whether or not a session existed. */
export function postLogout() {
  return createApiDispatcherAction((d) =>
    d.call<void>({ scope: "panel", method: "POST", path: "/api/logout" }),
  );
}

/**
 * GET /api/me
 *
 * The SPA's boot call: it decides login-page versus application. Its 401 body carries
 * `no_admins` when nobody has been created yet, which is what drives the setup card.
 */
export function getMe() {
  return createApiDispatcherAction((d) =>
    d.call<Session>({ scope: "panel", method: "GET", path: "/api/me" }),
  );
}
