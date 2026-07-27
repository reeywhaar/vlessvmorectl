/**
 * Structural inputs for the two status classifiers below.
 *
 * They take the minimum shape rather than VlessUser because the subscriber's share page
 * has a different type carrying the same facts, and it must reach the same verdict.
 * VlessUser satisfies both, so no existing caller changes.
 *
 * Duplicating the logic instead would eventually let the panel and the share page
 * disagree about who is over quota, and nobody would notice for months.
 */
export interface UserStateInput {
  enabled: boolean;
  disabled_reason?: "quota" | "expired";
  expires_at?: string;
}

export interface QuotaStateInput {
  /** 0 means unlimited. */
  quota_bytes: number;
  usage?: { window_total: number };
}

/**
 * Bytes, in the binary units every VPN client displays.
 *
 * `1.4 GB` reads better than `1,503,238,553`, and the exactness is never what an
 * operator wants from a traffic figure.
 */
export function formatBytes(n: number, digits = 1): string {
  if (!Number.isFinite(n)) return "—";
  if (n === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(Math.floor(Math.log(Math.abs(n)) / Math.log(1024)), units.length - 1);
  const value = n / 1024 ** i;
  // No decimals on bytes, and none once the number is already large enough to read.
  const d = i === 0 ? 0 : value >= 100 ? 0 : digits;
  return `${value.toFixed(d)} ${units[i]}`;
}

/** Parse "100GB", "500 mb", "1.5 TiB" into bytes. 0 and "" mean unlimited. */
export function parseBytes(input: string): number | null {
  const s = input.trim().toLowerCase();
  if (s === "" || s === "0") return 0;

  const m = /^([\d.]+)\s*([kmgtp]?)i?b?$/.exec(s);
  if (!m || m[1] === undefined) return null;
  const value = Number.parseFloat(m[1]);
  if (!Number.isFinite(value) || value < 0) return null;

  const exp = { "": 0, k: 1, m: 2, g: 3, t: 4, p: 5 }[m[2] ?? ""] ?? 0;
  return Math.round(value * 1024 ** exp);
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

export function formatRelative(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "never";
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "—";

  const diff = then - now;
  const abs = Math.abs(diff);
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ["year", 365 * 86_400_000],
    ["month", 30 * 86_400_000],
    ["day", 86_400_000],
    ["hour", 3_600_000],
    ["minute", 60_000],
  ];
  for (const [unit, ms] of units) {
    if (abs >= ms) return rtf.format(Math.round(diff / ms), unit);
  }
  return "just now";
}

export function formatDateTime(iso: string | undefined): string {
  if (!iso) return "—";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "—";
  return new Date(t).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/** A UTC date, for day buckets. Rendering a UTC-midnight bucket in local time labels a
 *  2026-07-25 bucket as "Jul 24" anywhere west of Greenwich. */
export function formatUTCDate(ms: number): string {
  return new Date(ms).toLocaleDateString(undefined, {
    timeZone: "UTC",
    month: "short",
    day: "numeric",
  });
}

export function formatLocalHour(ms: number): string {
  return new Date(ms).toLocaleTimeString(undefined, { hour: "numeric" });
}

/**
 * What to call a node.
 *
 * Its configured `name` when it has one, its hostname otherwise. vlessvmore omits the
 * field rather than sending an empty string, but `||` covers both — an operator who sets
 * `"name": ""` means "no name", not "a nameless label".
 */
export function serverLabel(info: { name?: string; host: string }): string {
  return info.name?.trim() || info.host;
}

/** Whether serverLabel returned something other than the hostname, i.e. whether the host
 *  still needs showing somewhere. */
export function hasServerName(info: { name?: string; host: string }): boolean {
  return serverLabel(info) !== info.host;
}

export type UserState =
  | { kind: "active"; label: "Active" }
  | { kind: "disabled"; label: "Disabled" }
  | { kind: "quota"; label: "Over quota" }
  | { kind: "expired"; label: "Expired" }
  | { kind: "expiring"; label: string };

/**
 * The one place that decides what a user's status *is*.
 *
 * Worth centralising because the inputs disagree in ways that are easy to get wrong:
 * `enabled` is false both when an operator switched someone off and when enforcement
 * did, and only `disabled_reason` tells them apart. A user can also be enabled but
 * expire tomorrow, which is not a problem yet but is the thing worth showing.
 */
export function userState(u: UserStateInput, now = Date.now()): UserState {
  if (!u.enabled) {
    if (u.disabled_reason === "quota") return { kind: "quota", label: "Over quota" };
    if (u.disabled_reason === "expired") return { kind: "expired", label: "Expired" };
    return { kind: "disabled", label: "Disabled" };
  }
  if (u.expires_at) {
    const at = Date.parse(u.expires_at);
    if (!Number.isNaN(at)) {
      if (at <= now) return { kind: "expired", label: "Expired" };
      if (at - now < 7 * 86_400_000) {
        return { kind: "expiring", label: `Expires ${formatRelative(u.expires_at, now)}` };
      }
    }
  }
  return { kind: "active", label: "Active" };
}

export interface QuotaState {
  unlimited: boolean;
  used: number;
  limit: number;
  /** 0–1, clamped. Meaningless when unlimited. */
  fraction: number;
  remaining: number;
}

/**
 * quota_bytes of 0 means unlimited, and quota_remaining is then 0 as well — which reads
 * as "nothing left" to anything that does not know the convention. This is that
 * knowledge, in one function.
 */
export function quotaState(u: QuotaStateInput): QuotaState {
  const used = u.usage?.window_total ?? 0;
  const limit = u.quota_bytes;
  if (limit <= 0) {
    return { unlimited: true, used, limit: 0, fraction: 0, remaining: 0 };
  }
  return {
    unlimited: false,
    used,
    limit,
    fraction: Math.min(1, Math.max(0, used / limit)),
    remaining: Math.max(0, limit - used),
  };
}

/**
 * vlessvmore's own build check.
 *
 * Without `with_v2ray_api` there are no per-user counters at all, so usage sits at zero
 * and quotas never fire — and nothing anywhere says so. It is the single most valuable
 * thing this panel can notice on an operator's behalf.
 */
export function hasV2RayAPI(version: string | undefined): boolean | null {
  if (!version) return null;
  return version.includes("with_v2ray_api");
}
