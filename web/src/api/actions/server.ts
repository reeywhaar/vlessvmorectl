import { createApiDispatcherAction } from "../request";
import type { Server, ServerInfo } from "../types";

/**
 * GET /api/server — a single node's own connection details.
 *
 * Not to be confused with getServers in ./servers.ts, which asks *our* backend which
 * nodes exist.
 *
 * Read-only: API.md is explicit that there is no PATCH here, because config.json is
 * operator input.
 */
export function getServer(server: Server) {
  return createApiDispatcherAction((d) =>
    d.call<ServerInfo>({ scope: server, method: "GET", path: "/api/server" }),
  );
}
