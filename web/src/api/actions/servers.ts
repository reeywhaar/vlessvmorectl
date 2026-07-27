import { createApiDispatcherAction } from "../request";
import type { Server } from "../types";

/**
 * GET /api/servers — *our* backend's list of the nodes it manages.
 *
 * Not to be confused with getServer in ./server.ts, which asks a single node about
 * itself. This one returns only `{id, url}`: the bearer tokens stay on the host, which is
 * the entire reason this panel proxies rather than handing credentials to the browser.
 */
export function getServers() {
  return createApiDispatcherAction((d) =>
    d
      .call<{ servers: Server[] }>({ scope: "panel", method: "GET", path: "/api/servers" })
      .then((r) => r.servers),
  );
}
