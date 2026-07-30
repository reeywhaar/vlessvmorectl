import type { Server, SubscriberEntry, VlessUser } from "../../api/types";
import type { NodeUsers } from "../../queries/hooks";
import { userState } from "../../lib/format";

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

/** One subscriber, reduced to the three things the list column shows. */
export interface EntrySummary {
  /** Accounts an operator would want to act on. */
  problems: number;
  /** True while some node has not answered, or has answered badly. */
  pending: boolean;
  /** Traffic since each account's own quota window opened, summed. */
  used: number;
  /** True when at least one account contributed nothing to `used` — so the figure is a
   *  floor rather than a total, and must not be shown as if it were one. */
  partialUsage: boolean;
}

/**
 * What the subscribers list says about one person, from data the panel already has.
 *
 * `used` sums each account's window_total, which is the same figure the server page calls
 * "Used". Those windows open when an operator resets a quota, so two accounts can be
 * counting from different days — the sum answers "how much has this person moved lately",
 * not "how much since a shared instant".
 */
export function summariseEntries(resolved: ResolvedEntry[]): EntrySummary {
  let problems = 0;
  let pending = false;
  let used = 0;
  let partialUsage = false;

  for (const r of resolved) {
    if (r.nodeState === "unconfigured") problems++;
    else if (r.nodeState === "loading") pending = true;
    else if (r.nodeState === "failed") pending = true;
    else if (!r.user) problems++;
    else if (userState(r.user).kind !== "active") problems++;

    // Every case without an account is usage we cannot see: a node that has not answered,
    // one that failed, a server no longer configured, an account deleted on its node.
    if (r.user) used += r.user.usage?.window_total ?? 0;
    else partialUsage = true;
  }

  return { problems, pending, used, partialUsage };
}
