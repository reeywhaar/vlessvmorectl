import { userState } from "../lib/format";
import type { Tone } from "../components/ui";
import type { AccessEntry } from "./types";

/**
 * Subscriber-facing wording for a status.
 *
 * The classification stays in userState — the page must not disagree with the panel about
 * who is over quota. Only the words differ: userState returns operator vocabulary ("Over
 * quota", "Disabled") that is accurate and useless to the reader.
 *
 * Nothing here may mention a token, a node, a panel or a proxy. There is a test.
 */
export interface EntryStatus {
  tone: Tone;
  label: string;
  /** Shown under the badge. Absent when there is nothing worth saying. */
  detail?: string;
  /** Whether to show credentials at all. */
  usable: boolean;
}

export function entryStatus(entry: AccessEntry, now = Date.now()): EntryStatus {
  if (!entry.available) {
    switch (entry.reason) {
      case "unavailable":
        return {
          tone: "warn",
          label: "Temporarily unavailable",
          // This sentence is load-bearing. A server's management interface being
          // unreachable says nothing at all about whether the VPN itself is passing
          // traffic — they are different processes. Without it, every reboot generates a
          // message to whoever set this up.
          detail:
            "We couldn't reach this server just now. Your connection is probably still working — " +
            "this page just can't show its details. Try Refresh in a minute.",
          usable: false,
        };
      case "removed":
        return {
          tone: "danger",
          label: "No longer available",
          detail: "This connection has been removed.",
          usable: false,
        };
      case "unconfigured":
        return {
          tone: "danger",
          label: "No longer available",
          detail: "This server isn't set up any more. Ask whoever gave you this link.",
          usable: false,
        };
    }
  }

  const state = userState(entry, now);
  switch (state.kind) {
    case "active":
      return { tone: "ok", label: "Active", usable: true };
    case "expiring":
      return {
        tone: "warn",
        // userState already phrases this one for a human ("Expires in 3 days").
        label: state.label,
        detail: "Ask whoever set this up if you need it extended.",
        usable: true,
      };
    case "expired":
      return {
        tone: "danger",
        label: "Expired",
        detail: "This connection has run out. Ask whoever set it up to renew it.",
        usable: true,
      };
    case "quota":
      return {
        tone: "danger",
        label: "Data limit reached",
        detail:
          "You've used all the data on this connection. It will start working again when " +
          "the limit resets, or when someone raises it.",
        usable: true,
      };
    case "disabled":
      return {
        tone: "muted",
        label: "Turned off",
        detail: "This connection has been switched off.",
        usable: true,
      };
  }
}
