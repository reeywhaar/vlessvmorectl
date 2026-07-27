import { describe, expect, it } from "vitest";
import {
  formatBytes,
  hasServerName,
  hasV2RayAPI,
  parseBytes,
  quotaState,
  serverLabel,
  userState,
} from "./format";
import type { VlessUser } from "../api/types";

function user(over: Partial<VlessUser> = {}): VlessUser {
  return {
    id: "u_1",
    name: "alice",
    uuid: "268e4039-6dd0-4d35-b279-b97639d9eed3",
    enabled: true,
    quota_bytes: 0,
    usage_reset_at: "2026-07-01T00:00:00Z",
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...over,
  };
}

describe("formatBytes", () => {
  it("uses binary units", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1024)).toBe("1.0 KB");
    expect(formatBytes(1024 ** 3)).toBe("1.0 GB");
    expect(formatBytes(107374182400)).toBe("100 GB");
  });
});

describe("parseBytes", () => {
  it("accepts what an operator would actually type", () => {
    expect(parseBytes("100GB")).toBe(100 * 1024 ** 3);
    expect(parseBytes("100 gb")).toBe(100 * 1024 ** 3);
    expect(parseBytes("1.5TiB")).toBe(Math.round(1.5 * 1024 ** 4));
    expect(parseBytes("500")).toBe(500);
  });

  it("treats empty and zero as unlimited, which is the node's own spelling", () => {
    expect(parseBytes("")).toBe(0);
    expect(parseBytes("0")).toBe(0);
  });

  it("rejects nonsense rather than guessing", () => {
    expect(parseBytes("lots")).toBeNull();
    expect(parseBytes("-5GB")).toBeNull();
  });
});

describe("quotaState", () => {
  /**
   * quota_bytes of 0 means unlimited, and the node then reports quota_remaining as 0 too
   * — which reads as "nothing left" to anything that does not know the convention.
   */
  it("reads a zero quota as unlimited, not as exhausted", () => {
    const s = quotaState(user({ quota_bytes: 0 }));
    expect(s.unlimited).toBe(true);
    expect(s.fraction).toBe(0);
  });

  it("measures against the window, not the lifetime total", () => {
    const s = quotaState(
      user({
        quota_bytes: 1000,
        usage: {
          up: 5000,
          down: 5000,
          total: 10000, // lifetime, across previous windows
          window_up: 100,
          window_down: 150,
          window_total: 250,
          quota_bytes: 1000,
          quota_remaining: 750,
        },
      }),
    );
    expect(s.used).toBe(250);
    expect(s.remaining).toBe(750);
    expect(s.fraction).toBeCloseTo(0.25);
  });

  it("clamps rather than reporting over 100%", () => {
    const s = quotaState(
      user({
        quota_bytes: 100,
        usage: {
          up: 0,
          down: 0,
          total: 500,
          window_up: 0,
          window_down: 500,
          window_total: 500,
          quota_bytes: 100,
          quota_remaining: 0,
        },
      }),
    );
    expect(s.fraction).toBe(1);
    expect(s.remaining).toBe(0);
  });
});

describe("userState", () => {
  const now = Date.parse("2026-07-27T00:00:00Z");

  it("tells an operator-disabled user from an enforcement-disabled one", () => {
    expect(userState(user({ enabled: false }), now).kind).toBe("disabled");
    expect(userState(user({ enabled: false, disabled_reason: "quota" }), now).kind).toBe("quota");
    expect(userState(user({ enabled: false, disabled_reason: "expired" }), now).kind).toBe(
      "expired",
    );
  });

  it("warns before an expiry rather than only after", () => {
    expect(userState(user({ expires_at: "2026-07-30T00:00:00Z" }), now).kind).toBe("expiring");
    expect(userState(user({ expires_at: "2026-09-30T00:00:00Z" }), now).kind).toBe("active");
    expect(userState(user({ expires_at: "2026-07-26T00:00:00Z" }), now).kind).toBe("expired");
  });

  it("is active with no quota and no expiry", () => {
    expect(userState(user(), now).kind).toBe("active");
  });
});

describe("hasV2RayAPI", () => {
  /** Without this tag there are no per-user counters, so usage sits at zero and quotas
   *  never fire — silently. It is the single most valuable thing the panel can notice. */
  it("detects the build tag", () => {
    expect(hasV2RayAPI("sing-box version 1.13.14\nTags: with_utls,with_v2ray_api")).toBe(true);
    expect(hasV2RayAPI("sing-box version 1.13.14\nTags: with_utls")).toBe(false);
    expect(hasV2RayAPI(undefined)).toBeNull();
  });
});

describe("serverLabel", () => {
  /**
   * vlessvmore tags `name` omitempty, so an unnamed node simply has no field. The
   * fallback is what keeps every node identifiable rather than blank.
   */
  it("prefers the configured name and falls back to the host", () => {
    expect(serverLabel({ name: "Amsterdam", host: "vpn-nl.example.com" })).toBe("Amsterdam");
    expect(serverLabel({ host: "vpn-nl.example.com" })).toBe("vpn-nl.example.com");
  });

  it("treats an empty or blank name as unset", () => {
    expect(serverLabel({ name: "", host: "vpn.example.com" })).toBe("vpn.example.com");
    expect(serverLabel({ name: "   ", host: "vpn.example.com" })).toBe("vpn.example.com");
  });

  /** Drives whether the host is worth printing alongside: when it is already the title,
   *  repeating it reads as a rendering fault. */
  it("reports whether the label differs from the host", () => {
    expect(hasServerName({ name: "Amsterdam", host: "vpn-nl.example.com" })).toBe(true);
    expect(hasServerName({ host: "vpn-nl.example.com" })).toBe(false);
    expect(hasServerName({ name: "  ", host: "vpn-nl.example.com" })).toBe(false);
  });
});
