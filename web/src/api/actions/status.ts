import { createApiDispatcherAction } from "../request";
import type { Server, ServerStatus } from "../types";

/**
 * GET /api/status
 *
 * Worth reading `sing_box_version` from this rather than only displaying it: without
 * `with_v2ray_api` in the build tags there are no per-user traffic counters at all, so
 * usage sits at zero and quotas never fire — and nothing else anywhere says so.
 */
export function getStatus(server: Server) {
  return createApiDispatcherAction((d) =>
    d.call<ServerStatus>({ scope: server, method: "GET", path: "/api/status" }),
  );
}
