import { createApiDispatcherAction } from "../request";
import type { Server, SingBoxStatus } from "../types";

/**
 * POST /api/reload — regenerate the node's sing-box config and reload it.
 *
 * Bare, not wrapped in Reloaded. Reloading drops established connections, so the node
 * coalesces reloads that arrive within about a second of each other; this is offered as a
 * manual retry after a mutation reported `reloaded: false`, not as something to call
 * routinely.
 */
export function postReload(server: Server) {
  return createApiDispatcherAction((d) =>
    d.call<SingBoxStatus>({ scope: server, method: "POST", path: "/api/reload" }),
  );
}
