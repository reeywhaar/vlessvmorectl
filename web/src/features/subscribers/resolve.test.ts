import { describe, expect, it } from "vitest";
import { resolveEntries } from "./resolve";
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

function user(id: string): VlessUser {
  return {
    id,
    name: id,
    uuid: "uuid",
    enabled: true,
    quota_bytes: 0,
    usage_reset_at: "2026-07-01T00:00:00Z",
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
  };
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
