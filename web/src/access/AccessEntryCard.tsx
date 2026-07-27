import { useState } from "react";
import { QrMatrix } from "../components/QrMatrix";
import {
  Badge,
  ButtonLink,
  Card,
  QuotaMeter,
  SecretField,
  StatTile,
  cx,
} from "../components/ui";
import { formatBytes, formatDateTime, quotaState } from "../lib/format";
import { entryStatus } from "./copy";
import type { AccessEntry } from "./types";

/**
 * One VPN account, as its holder sees it.
 *
 * Two layout decisions worth keeping, because both look arbitrary and are not:
 *
 * The actions come *first in the DOM* and the QR is moved left only at `sm:`. On a phone
 * the QR is close to useless — you cannot scan your own screen — so the tap targets have
 * to lead, for reading order and for a screen reader alike. On a laptop the QR is the
 * whole point, because the second device is the phone. `order-*` buys both without
 * duplicating the markup or lying to assistive tech about the mobile order.
 *
 * The install page, not the subscription URL, is the primary action. vlessvmore's install
 * page walks a first-timer through picking a client; a subscription URL assumes they have
 * one already. It is a ButtonLink rather than a Button because long-press, middle-click
 * and "open in new tab" all have to work — those are exactly the gestures somebody
 * reaches for when moving a link between two devices, which is the entire situation.
 */
export function AccessEntryCard({ entry }: { entry: AccessEntry }) {
  const status = entryStatus(entry);

  return (
    <Card className="p-5">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="truncate font-semibold">{entry.server_label}</h2>
          {entry.label ? <p className="text-xs text-muted">{entry.label}</p> : null}
        </div>
        <Badge tone={status.tone}>{status.label}</Badge>
      </div>

      {status.detail ? (
        <p
          className={cx(
            "mt-3 rounded-lg p-3 text-xs",
            status.tone === "danger" && "bg-danger/15 text-danger",
            status.tone === "warn" && "bg-warn/15 text-warn",
            status.tone === "muted" && "bg-line text-muted",
          )}
        >
          {status.detail}
        </p>
      ) : null}

      {/*
        An unreachable entry shows no link, no QR and no numbers. Showing a credential we
        could not confirm, next to figures we could not refresh, is worse than showing
        nothing: it looks current, and it is the state in which somebody would copy a
        subscription URL that has since been rotated.
      */}
      {entry.available && status.usable ? <Connection entry={entry} /> : null}
    </Card>
  );
}

function Connection({ entry }: { entry: Extract<AccessEntry, { available: true }> }) {
  // The subscription is the better thing to scan: the client re-fetches it every 24 hours
  // so config and key changes propagate, and its response headers are what make the
  // client show remaining traffic and an expiry date. The vless:// URI is the fallback
  // for clients that do not do subscriptions — and it is a much denser code.
  const codes = [
    entry.subscription_qr && {
      key: "sub" as const,
      label: "Subscription",
      qr: entry.subscription_qr,
    },
    entry.qr && { key: "vless" as const, label: "vless://", qr: entry.qr },
  ].filter((c) => c !== undefined);

  const [shown, setShown] = useState<"sub" | "vless">("sub");
  const active = codes.find((c) => c.key === shown) ?? codes[0];
  const quota = quotaState(entry);

  return (
    <>
      <div className="mt-4 flex flex-col gap-4 sm:flex-row">
        {/* DOM order: actions first. See the component comment. */}
        <div className="min-w-0 flex-1 space-y-3 sm:order-2">
          {entry.install_url ? (
            <ButtonLink
              variant="primary"
              className="w-full sm:w-auto"
              href={entry.install_url}
              target="_blank"
              rel="noopener noreferrer"
            >
              Set this up
            </ButtonLink>
          ) : null}

          {entry.subscription_url ? (
            <SecretField label="Subscription link" value={entry.subscription_url} masked={false} />
          ) : null}

          {entry.link ? (
            <details>
              <summary className="cursor-pointer text-xs text-muted hover:text-ink">
                The raw vless:// link
              </summary>
              <div className="mt-2">
                <SecretField label="vless://" value={entry.link} masked={false} />
              </div>
            </details>
          ) : null}

          {!entry.install_url && !entry.subscription_url && !entry.link ? (
            <p className="rounded-lg bg-warn/15 p-3 text-xs text-warn">
              This connection's details couldn't be loaded just now. Try Refresh in a
              minute.
            </p>
          ) : null}
        </div>

        {active ? (
          <div className="shrink-0 sm:order-1">
            <QrMatrix qr={active.qr} label={`${active.label} QR code`} />
            {codes.length > 1 ? (
              <div className="mt-2 flex gap-1">
                {codes.map((c) => (
                  <button
                    key={c.key}
                    onClick={() => setShown(c.key)}
                    className={cx(
                      "rounded-lg px-2 py-1 text-xs",
                      c.key === active.key
                        ? "bg-accent text-accent-ink"
                        : "text-muted hover:bg-line",
                    )}
                  >
                    {c.label}
                  </button>
                ))}
              </div>
            ) : null}
            <p className="mt-2 max-w-[220px] text-xs text-muted">
              {active.key === "sub"
                ? "Scan this one. It keeps itself up to date and shows your remaining data in the app."
                : "A one-off setup. It won't update, and won't show your data allowance."}
            </p>
          </div>
        ) : null}
      </div>

      <div className="mt-4 grid grid-cols-2 gap-3">
        <StatTile
          label="Data used"
          value={formatBytes(quota.used)}
          hint={quota.unlimited ? "No limit" : `of ${formatBytes(quota.limit)}`}
        />
        <StatTile
          label="Expires"
          value={entry.expires_at ? formatDateTime(entry.expires_at) : "Never"}
        />
      </div>
      {quota.unlimited ? null : (
        <div className="mt-3">
          <QuotaMeter fraction={quota.fraction} />
        </div>
      )}
    </>
  );
}
