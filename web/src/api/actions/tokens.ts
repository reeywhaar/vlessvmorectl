import { createApiDispatcherAction } from "../request";
import type { Server, VlessToken } from "../types";

/**
 * GET /api/tokens
 *
 * The best diagnostic there is for the "404 means the token was rejected" class of
 * problem: `last_used_at` shows whether this panel's token is being used at all, and
 * `revoked_at` shows whether someone retired it. Never returns a secret.
 */
export function getTokens(server: Server) {
  return createApiDispatcherAction((d) =>
    d
      .call<{ tokens: VlessToken[] }>({ scope: server, method: "GET", path: "/api/tokens" })
      .then((r) => r.tokens),
  );
}
