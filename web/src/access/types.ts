import type { QRMatrix } from "../api/types";

/**
 * What GET /api/access/{token} returns.
 *
 * These live with the island rather than in api/types.ts because they are not vlessvmore's
 * wire model — they are a projection this panel assembles, and the only consumer is this
 * page. Keeping them here means the operator's bundle has no reason to import from
 * access/, which is the direction the split cares about.
 */
export interface AccessResponse {
  subscriber: { name: string };
  entries: AccessEntry[];
  /** Server time. The page states its own age rather than polling; see AccessPage. */
  fetched_at: string;
}

/**
 * One account, in one of two shapes.
 *
 * Discriminated on `available`, and the distinction matters to the reader far more than
 * it looks: "we could not reach that server just now" and "this one is gone" mean
 * opposite things, and collapsing them would tell somebody their VPN had been revoked
 * every time a node rebooted.
 */
export type AccessEntry = AccessEntryOk | AccessEntryUnavailable;

interface AccessEntryBase {
  id: string;
  server_label: string;
  /** The operator's own word for this account: "phone", "the laptop". */
  label?: string;
}

export interface AccessEntryOk extends AccessEntryBase {
  available: true;
  name?: string;
  enabled: boolean;
  disabled_reason?: "quota" | "expired";
  expires_at?: string;
  /** 0 means unlimited. */
  quota_bytes: number;
  usage?: AccessUsage;
  link?: string;
  subscription_url?: string;
  install_url?: string;
  qr?: QRMatrix;
  subscription_qr?: QRMatrix;
}

export interface AccessEntryUnavailable extends AccessEntryBase {
  available: false;
  /**
   * "unavailable": the node did not answer — retryable, and says nothing about whether
   * the person's VPN is actually passing traffic. "removed": the account is gone.
   * "unconfigured": this panel no longer manages that server.
   *
   * A closed set on purpose. The backend has a URL and a Go error in hand at this point
   * and deliberately sends neither.
   */
  reason: "unavailable" | "removed" | "unconfigured";
}

export interface AccessUsage {
  window_up: number;
  window_down: number;
  window_total: number;
  /** 0 when unlimited, which is not "none left". */
  quota_remaining: number;
}
