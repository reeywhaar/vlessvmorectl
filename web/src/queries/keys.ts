import type { SnappedRange } from "../lib/range";

/**
 * The key hierarchy does the isolation work by construction.
 *
 *  - `["server", id]` is a prefix of everything belonging to that node, so invalidating
 *    one node touches no other. Cross-server blast radius is zero.
 *  - `["server", id, "users"]` is a prefix of every per-user key, so a mutation that
 *    invalidates the list also invalidates detail, link and usage. The over-invalidation
 *    is intentional: changing a uuid genuinely does change the QR code.
 *  - `["servers"]` sits under a different root from `["server", …]`, so refetching the
 *    node list never discards cached node data.
 */
export const qk = {
  session: ["session"] as const,
  servers: ["servers"] as const,

  /**
   * The panel's own subscriber store, under its own root.
   *
   * Not nested under ["server", …]: a subscriber belongs to no node, and hanging it off
   * one would make a node-scoped invalidation throw away data that node never supplied.
   */
  subscribers: ["subscribers"] as const,
  subscriber: (id: string) => ["subscribers", id] as const,

  server: (s: string) => ["server", s] as const,
  serverInfo: (s: string) => ["server", s, "info"] as const,
  serverStatus: (s: string) => ["server", s, "status"] as const,
  tokens: (s: string) => ["server", s, "tokens"] as const,

  users: (s: string) => ["server", s, "users"] as const,
  user: (s: string, u: string) => ["server", s, "users", u] as const,
  userLink: (s: string, u: string) => ["server", s, "users", u, "link"] as const,
  userUsage: (s: string, u: string, r: SnappedRange) =>
    ["server", s, "users", u, "usage", r.fromUnix, r.bucket] as const,
};
