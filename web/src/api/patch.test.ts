import { describe, expect, it } from "vitest";
import { EmptyPatchError, Tri, UserCreate, UserPatch } from "./patch";

describe("UserPatch.toBody", () => {
  /**
   * The three-valued expires_at is the whole reason Tri exists.
   *
   * In plain JavaScript `{ expires_at: undefined }` serialises to an absent key, so the
   * obvious way to write "clear the expiry" silently means "change nothing" — and the
   * user keeps expiring on the original date. These three cases are the difference.
   */
  it("omits expires_at when keeping", () => {
    const body = new UserPatch({ name: "alice", expiresAt: Tri.keep() }).toBody();
    expect("expires_at" in body).toBe(false);
    expect(body).toEqual({ name: "alice" });
  });

  it("sends an explicit null when clearing", () => {
    const body = new UserPatch({ expiresAt: Tri.clear() }).toBody();
    expect(body).toEqual({ expires_at: null });
    // Distinct from the field being absent, which is the entire point.
    expect("expires_at" in body).toBe(true);
  });

  it("sends an ISO timestamp when setting", () => {
    const when = new Date("2026-12-31T00:00:00.000Z");
    expect(new UserPatch({ expiresAt: Tri.set(when) }).toBody()).toEqual({
      expires_at: "2026-12-31T00:00:00.000Z",
    });
  });

  it("omits expires_at entirely when the field is not given", () => {
    expect(new UserPatch({ enabled: false }).toBody()).toEqual({ enabled: false });
  });

  /**
   * An empty PATCH is a no-op in the node's store, but its reloadAfterChange still runs —
   * and a reload drops every established connection on that node.
   */
  it("refuses an empty patch rather than sending one", () => {
    expect(() => new UserPatch({}).toBody()).toThrow(EmptyPatchError);
    expect(() => new UserPatch({ expiresAt: Tri.keep() }).toBody()).toThrow(EmptyPatchError);
  });

  it("maps every field to its wire name", () => {
    const body = new UserPatch({
      name: "bob",
      uuid: "268e4039-6dd0-4d35-b279-b97639d9eed3",
      enabled: true,
      quotaBytes: 107374182400,
      note: "phone",
    }).toBody();

    expect(body).toEqual({
      name: "bob",
      uuid: "268e4039-6dd0-4d35-b279-b97639d9eed3",
      enabled: true,
      quota_bytes: 107374182400,
      note: "phone",
    });
  });

  it("keeps a zero quota, which is how the node spells unlimited", () => {
    expect(new UserPatch({ quotaBytes: 0 }).toBody()).toEqual({ quota_bytes: 0 });
  });

  it("keeps enabled:false, which a truthiness check would drop", () => {
    expect(UserPatch.enabled(false).toBody()).toEqual({ enabled: false });
  });
});

describe("UserCreate.toBody", () => {
  it("sends only name when nothing else is given", () => {
    expect(new UserCreate({ name: "alice" }).toBody()).toEqual({ name: "alice" });
  });

  it("maps the optional fields", () => {
    const body = new UserCreate({
      name: "alice",
      quotaBytes: 1024,
      note: "phone",
      enabled: false,
      expiresAt: new Date("2026-12-31T00:00:00.000Z"),
    }).toBody();

    expect(body).toEqual({
      name: "alice",
      quota_bytes: 1024,
      note: "phone",
      enabled: false,
      expires_at: "2026-12-31T00:00:00.000Z",
    });
  });
});
