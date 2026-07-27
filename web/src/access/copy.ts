import { userState } from "../lib/format";
import type { Tone } from "../components/ui";
import type { AccessEntry } from "./types";

/**
 * What to tell the person reading their own share page.
 *
 * The *classification* is not decided here — that is userState in lib/format.ts, and it
 * deliberately stays shared, because a page that disagreed with the panel about who is
 * over quota would be the worst bug this feature could have.
 *
 * What is decided here is the wording, and it is a genuinely different job. userState
 * returns operator vocabulary: "Over quota", "Disabled". Those are accurate and useless
 * to somebody who does not run this panel — "Disabled" reads as an accusation, and "Over
 * quota" as jargon. Every string below answers the two questions the reader actually has:
 * what is wrong, and what do I do about it.
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
