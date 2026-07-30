import { describe, expect, it } from "vitest";
import { resolveEntries, summariseEntries } from "./resolve";
import type { Server, SubscriberEntry, VlessUser } from "../../api/types";
import type { NodeUsers } from "../../queries/hooks";

const ams: Server = { id: "aaa111", url: "https://ams.example.com" };
const ber: Server = { id: "bbb222", url: "https://ber.example.com" };

function entry(over: Partial<SubscriberEntry> = {}): SubscriberEntry {
  return {
    id: "e1",
    server_id: ams.id,
    vless_user_id: "u_alice",
    added_at: "2026-07-01T00:00:00Z",
    server_configured: true,
    ...over,
  };
}

function user(id: string, over: Partial<VlessUser> = {}): VlessUser {
  return {
    id,
    name: id,
    uuid: "uuid",
    enabled: true,
    quota_bytes: 0,
    usage_reset_at: "2026-07-01T00:00:00Z",
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...over,
  };
}

/** A user carrying traffic, which is all summariseEntries reads off one. */
function used(id: string, windowTotal: number, over: Partial<VlessUser> = {}): VlessUser {
  return user(id, {
    usage: {
      up: 0,
      down: windowTotal,
      total: windowTotal,
      window_up: 0,
      window_down: windowTotal,
      window_total: windowTotal,
      quota_bytes: 0,
      quota_remaining: 0,
    },
    ...over,
  });
}

function node(server: Server, users: VlessUser[] | null, failed = false): NodeUsers {
  return {
    server,
    users,
    failure: failed ? { kind: "unreachable", reason: "refused", detail: "" } : null,
  };
}

describe("resolveEntries", () => {
  it("matches an entry to its account", () => {
    const [r] = resolveEntries([entry()], [ams], [node(ams, [user("u_alice")])]);
    expect(r?.nodeState).toBe("ok");
    expect(r?.user?.id).toBe("u_alice");
    expect(r?.server).toBe(ams);
  });

  it("marks an entry whose server is no longer configured", () => {
    // What an operator lands in after changing a node's URL: the id is derived from the
    // origin, so the reference orphans.
    const [r] = resolveEntries([entry({ server_id: "gone" })], [ams], [node(ams, [])]);
    expect(r?.nodeState).toBe("unconfigured");
    expect(r?.server).toBeUndefined();
  });

  it("marks an account the node has never heard of", () => {
    const [r] = resolveEntries([entry()], [ams], [node(ams, [user("u_bob")])]);
    expect(r?.nodeState).toBe("ok");
    expect(r?.user).toBeUndefined();
  });

  it("distinguishes a node still loading from one that failed", () => {
    // These render identically if you only check whether `user` is undefined, and they
    // mean opposite things: one is a spinner, the other is a broken row.
    const loading = resolveEntries([entry()], [ams], [node(ams, null)]);
    const failed = resolveEntries([entry()], [ams], [node(ams, null, true)]);
    expect(loading[0]?.nodeState).toBe("loading");
    expect(failed[0]?.nodeState).toBe("failed");
  });

  it("resolves entries across several nodes independently", () => {
    const entries = [
      entry({ id: "e1", server_id: ams.id, vless_user_id: "u_alice" }),
      entry({ id: "e2", server_id: ber.id, vless_user_id: "u_bob" }),
    ];
    const resolved = resolveEntries(entries, [ams, ber], [
      node(ams, [user("u_alice")]),
      node(ber, null, true),
    ]);
    expect(resolved[0]?.nodeState).toBe("ok");
    expect(resolved[1]?.nodeState).toBe("failed");
    // One node failing must not disturb the other's row.
    expect(resolved[0]?.user?.id).toBe("u_alice");
  });

  it("keeps ids that share a prefix apart", () => {
    // Guards the nested-map lookup against any composite-key scheme creeping back in.
    const entries = [
      entry({ id: "e1", server_id: ams.id, vless_user_id: "u_a" }),
      entry({ id: "e2", server_id: ams.id, vless_user_id: "u_ab" }),
    ];
    const resolved = resolveEntries(entries, [ams], [node(ams, [user("u_a"), user("u_ab")])]);
    expect(resolved[0]?.user?.id).toBe("u_a");
    expect(resolved[1]?.user?.id).toBe("u_ab");
  });
});

describe("summariseEntries", () => {
  const GB = 1024 ** 3;

  it("adds up the traffic of every attached account", () => {
    const entries = [
      entry({ id: "e1", server_id: ams.id, vless_user_id: "u_a" }),
      entry({ id: "e2", server_id: ber.id, vless_user_id: "u_b" }),
    ];
    const summary = summariseEntries(
      resolveEntries(entries, [ams, ber], [
        node(ams, [used("u_a", 3 * GB)]),
        node(ber, [used("u_b", 1.5 * GB)]),
      ]),
    );

    expect(summary.used).toBe(4.5 * GB);
    expect(summary.partialUsage).toBe(false);
    expect(summary.problems).toBe(0);
  });

  it("counts an account with no usage figure as zero rather than as missing", () => {
    // A node built without with_v2ray_api reports no counters at all. That is a real answer:
    // the account is there and has moved nothing this panel can see.
    const summary = summariseEntries(resolveEntries([entry()], [ams], [node(ams, [user("u_alice")])]));

    expect(summary.used).toBe(0);
    expect(summary.partialUsage).toBe(false);
  });

  /**
   * The case the `≥` in the column exists for. A sum that silently omits a node reads as a
   * total, and an operator would believe someone had used a third of what they had.
   */
  it("flags the sum as a floor when an account could not be read", () => {
    const entries = [
      entry({ id: "e1", server_id: ams.id, vless_user_id: "u_a" }),
      entry({ id: "e2", server_id: ber.id, vless_user_id: "u_b" }),
    ];

    for (const unreadable of [node(ber, null), node(ber, null, true), node(ber, [])]) {
      const summary = summariseEntries(
        resolveEntries(entries, [ams, ber], [node(ams, [used("u_a", 2 * GB)]), unreadable]),
      );

      expect(summary.used).toBe(2 * GB);
      expect(summary.partialUsage).toBe(true);
    }
  });

  it("flags a server that is no longer configured the same way", () => {
    const summary = summariseEntries(
      resolveEntries([entry({ server_id: "gone" })], [ams], [node(ams, [])]),
    );

    expect(summary.partialUsage).toBe(true);
    expect(summary.problems).toBe(1);
  });

  it("still counts problems and pending as before", () => {
    const entries = [
      entry({ id: "e1", server_id: ams.id, vless_user_id: "u_a" }),
      entry({ id: "e2", server_id: ams.id, vless_user_id: "u_off" }),
      entry({ id: "e3", server_id: ber.id, vless_user_id: "u_b" }),
    ];
    const summary = summariseEntries(
      resolveEntries(entries, [ams, ber], [
        node(ams, [used("u_a", GB), used("u_off", GB, { enabled: false })]),
        node(ber, null),
      ]),
    );

    // The disabled account is a problem, the unanswered node is pending — and its traffic is
    // the part missing from the sum.
    expect(summary.problems).toBe(1);
    expect(summary.pending).toBe(true);
    expect(summary.used).toBe(2 * GB);
    expect(summary.partialUsage).toBe(true);
  });

  it("summarises a subscriber with no connections at all", () => {
    expect(summariseEntries([])).toEqual({
      problems: 0,
      pending: false,
      used: 0,
      partialUsage: false,
    });
  });
});
