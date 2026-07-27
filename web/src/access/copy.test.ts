import { describe, expect, it } from "vitest";
import { entryStatus } from "./copy";
import type { AccessEntry } from "./types";

const base = { id: "e1", server_label: "Amsterdam" };

function ok(over: Partial<Extract<AccessEntry, { available: true }>> = {}): AccessEntry {
  return { ...base, available: true, enabled: true, quota_bytes: 0, ...over };
}

describe("entryStatus", () => {
  it("says nothing extra about a working connection", () => {
    const s = entryStatus(ok());
    expect(s.label).toBe("Active");
    expect(s.tone).toBe("ok");
    expect(s.detail).toBeUndefined();
    expect(s.usable).toBe(true);
  });

  it("uses the reader's words for a quota, not the operator's", () => {
    const s = entryStatus(ok({ enabled: false, disabled_reason: "quota" }));
    // userState calls this "Over quota", which is jargon to somebody who does not run
    // the panel. The classification is shared; the wording is not.
    expect(s.label).toBe("Data limit reached");
    expect(s.detail).toMatch(/start working again/);
  });

  it("reassures rather than alarms when a server cannot be reached", () => {
    const s = entryStatus({ ...base, available: false, reason: "unavailable" });
    expect(s.tone).toBe("warn");
    // The load-bearing sentence: a server's management interface being unreachable says
    // nothing about whether the VPN is passing traffic. Without it, every node reboot
    // generates a support message.
    expect(s.detail).toMatch(/probably still working/);
    expect(s.usable).toBe(false);
  });

  it("distinguishes a removed account from an unreachable server", () => {
    const removed = entryStatus({ ...base, available: false, reason: "removed" });
    const unreachable = entryStatus({ ...base, available: false, reason: "unavailable" });
    expect(removed.label).not.toBe(unreachable.label);
    expect(removed.tone).toBe("danger");
  });

  it("never shows credentials for an entry we could not confirm", () => {
    for (const reason of ["unavailable", "removed", "unconfigured"] as const) {
      expect(entryStatus({ ...base, available: false, reason }).usable).toBe(false);
    }
  });

  it("covers every state, and none of them leaks operator vocabulary", () => {
    const cases: AccessEntry[] = [
      ok(),
      ok({ expires_at: new Date(Date.now() + 3 * 86_400_000).toISOString() }),
      ok({ enabled: false, disabled_reason: "expired" }),
      ok({ enabled: false, disabled_reason: "quota" }),
      ok({ enabled: false }),
      { ...base, available: false, reason: "unavailable" },
      { ...base, available: false, reason: "removed" },
      { ...base, available: false, reason: "unconfigured" },
    ];

    // A cheap, durable guard against panel language reaching a stranger's screen.
    const banned = /\b(token|panel|node|proxy|bearer|sub_token|admin)\b/i;
    for (const c of cases) {
      const s = entryStatus(c);
      expect(s.label).toBeTruthy();
      expect(banned.test(s.label)).toBe(false);
      if (s.detail) expect(banned.test(s.detail)).toBe(false);
    }
  });
});
