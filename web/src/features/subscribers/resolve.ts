import type { Server, SubscriberEntry, VlessUser } from "../../api/types";
import type { NodeUsers } from "../../queries/hooks";

/**
 * How an entry looks once the panel's reference has been matched against live node data.
 *
 * `nodeState` exists because "the account is not there" and "we do not know yet" render
 * identically if you only look at whether `user` is undefined, and they mean opposite
 * things to an operator: one is a broken reference to fix, the other is a spinner.
 */
export interface ResolvedEntry {
  entry: SubscriberEntry;
  /** undefined when server_id is no longer in VLESSVMORE_SERVERS. */
  server: Server | undefined;
  /** undefined when the account was deleted on the node, or the node has not answered. */
  user: VlessUser | undefined;
  nodeState: "ok" | "loading" | "failed" | "unconfigured";
}

/**
 * Joins a subscriber's entries against whatever the nodes have told us.
 *
 * Pure, so it can be tested without rendering anything — which matters, because the four
 * nodeState cases are exactly the ones that are awkward to reproduce in a component test.
 */
export function resolveEntries(
  entries: SubscriberEntry[],
  servers: Server[],
  nodeUsers: NodeUsers[],
): ResolvedEntry[] {
  const serverById = new Map(servers.map((s) => [s.id, s]));

  // A nested map rather than a composite string key: there is no separator that an id
  // could not someday contain, and a collision here would show one person's account under
  // another's name.
  const usersByServer = new Map<string, Map<string, VlessUser> | null>();
  for (const n of nodeUsers) {
    usersByServer.set(
      n.server.id,
      n.users ? new Map(n.users.map((u) => [u.id, u])) : null,
    );
  }

  return entries.map((entry) => {
    const server = serverById.get(entry.server_id);
    if (!server) {
      return { entry, server: undefined, user: undefined, nodeState: "unconfigured" as const };
    }

    const node = nodeUsers.find((n) => n.server.id === entry.server_id);
    const index = usersByServer.get(entry.server_id);
    if (!index) {
      // No list yet. A recorded failure means the node answered badly; anything else
      // means it has not answered at all.
      return {
        entry,
        server,
        user: undefined,
        nodeState: node?.failure ? ("failed" as const) : ("loading" as const),
      };
    }
    return {
      entry,
      server,
      user: index.get(entry.vless_user_id),
      nodeState: "ok" as const,
    };
  });
}
