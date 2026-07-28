import { useState } from "react";
import { QrDialog } from "../components/QrDialog";
import {
  Badge,
  ButtonLink,
  Card,
  CopyButton,
  Dialog,
  IconButton,
  QrIcon,
  QuotaMeter,
  StatTile,
  cx,
} from "../components/ui";
import { formatBytes, formatDateTime, quotaState } from "../lib/format";
import { entryStatus } from "./copy";
import type { QRMatrix } from "../api/types";
import type { AccessEntry } from "./types";

/**
 * One VPN account, summarised — and nothing more until it is asked for.
 *
 * # Why the credentials are not on the page
 *
 * A QR code is a credential in a form anyone can capture from across a room, without
 * touching the device and without the holder noticing. Somebody opening this link on a
 * train, in a café, or at a desk with a colleague behind them would otherwise have every
 * account they hold rendered at scanning size the moment the page loaded, for as long as
 * the tab stayed open.
 *
 * So the card shows only what is safe to leave on screen — which server, whether it
 * works, how much data is left — and the link, the subscription URL and both QR codes
 * live behind a tap. The tap is the point: revealing a credential should be something the
 * person chose to do at a moment of their choosing, not the default state of a page they
 * opened to check a number.
 *
 * It also happens to make the list readable. Three accounts used to be three QR codes and
 * six URLs; now it is three rows.
 */
export function AccessEntryCard({ entry }: { entry: AccessEntry }) {
  const [open, setOpen] = useState(false);
  const status = entryStatus(entry);

  // An entry we could not confirm has nothing to reveal, so it is not a button. Showing a
  // credential we could not check, as if it were current, is worse than showing none.
  const openable = entry.available && status.usable;

  const summary = (
    <>
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

      {entry.available ? <Summary entry={entry} /> : null}
    </>
  );

  if (!openable) return <Card className="p-5">{summary}</Card>;

  return (
    <Card className="p-5">
      {/*
        The whole card is the tap target, which matters more on a phone than a small
        "Show" link would. The quota meter is deliberately rendered outside it: a
        role="meter" is not permitted inside a button's content model, and nesting it
        makes the row announce as a jumble.
      */}
      <button
        type="button"
        className="w-full cursor-pointer text-left"
        onClick={() => setOpen(true)}
        aria-haspopup="dialog"
      >
        {summary}
        <p className="mt-3 text-xs text-accent">Show connection details →</p>
      </button>
      <Meter entry={entry} />
      {/*
        Mounted only while open, rather than rendered with open={false}.

        A closed native <dialog> is display:none but its children are still in the
        document — so the links and the QR would be present, merely invisible. Not being
        on screen is what this page cares about, so that would technically hold; not being
        in the document at all is the stronger and more obviously correct property, and it
        is what the test asserts. It also means each open starts from a clean state rather
        than the last one's.
      */}
      {open ? <ConnectionDialog entry={entry} onClose={() => setOpen(false)} /> : null}
    </Card>
  );
}

/** The figures that are safe to leave on screen: no credential, nothing scannable. */
function Summary({ entry }: { entry: Extract<AccessEntry, { available: true }> }) {
  const quota = quotaState(entry);
  return (
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
  );
}

function Meter({ entry }: { entry: Extract<AccessEntry, { available: true }> }) {
  const quota = quotaState(entry);
  if (quota.unlimited) return null;
  return (
    <div className="mt-3">
      <QuotaMeter fraction={quota.fraction} />
    </div>
  );
}

/**
 * The credentials, once asked for.
 *
 * Text and buttons only — no QR is drawn here. A code big enough to scan is big enough to
 * photograph from across a room, so it gets its own modal behind its own button rather
 * than appearing the moment this one opens. Two taps to put a credential on screen at
 * scanning size is the right number when the page is designed to be opened in public.
 *
 * The ordering is install page, then subscription, then the one-off link, which is
 * roughly least to most technical: somebody with no VPN app yet needs the first, somebody
 * who has one needs the second, and the third exists for clients that cannot do
 * subscriptions at all.
 */
function ConnectionDialog({
  entry,
  onClose,
}: {
  entry: Extract<AccessEntry, { available: true }>;
  onClose: () => void;
}) {
  // Which QR is on screen, if any. One level deeper than this dialog, because a code
  // large enough to scan is also large enough to photograph — the same reasoning that
  // put this dialog behind a tap, applied once more to the one thing on the page a
  // stranger can capture without touching the device.
  const [qr, setQr] = useState<Code | null>(null);

  const nothing = !entry.install_url && !entry.subscription_url && !entry.link;

  return (
    <Dialog open onClose={onClose} title={entry.server_label}>
      <div className="space-y-4">
        {entry.install_url ? (
          <>
            {/*
              The primary action, and an <a> rather than a button: long-press, middle-click
              and "open in new tab" all have to work, because those are exactly the
              gestures somebody reaches for when moving a link between two devices — which
              is the entire situation. vlessvmore's install page also walks a first-timer
              through picking a client, which a bare subscription URL does not.
            */}
            <ButtonLink
              variant="primary"
              className="w-full"
              href={entry.install_url}
              target="_blank"
              rel="noopener noreferrer"
            >
              Set this up
            </ButtonLink>
            <p className="text-xs text-muted">
              Opens a page that walks you through it. If you already have a VPN app, use
              the subscription link below instead.
            </p>
          </>
        ) : null}

        {entry.subscription_url ? (
          <CredentialRow
            label="Subscription link"
            value={entry.subscription_url}
            hint="Keeps itself up to date, and shows your remaining data in the app."
            {...(entry.subscription_qr
              ? {
                  onShowQr: () =>
                    setQr({
                      title: "Subscription QR",
                      qr: entry.subscription_qr!,
                      caption:
                        "Scan this one. It keeps itself up to date and shows your remaining data in the app.",
                    }),
                }
              : {})}
          />
        ) : null}

        {entry.link ? (
          <CredentialRow
            label="One-off vless:// link"
            value={entry.link}
            hint="For apps that don't do subscriptions. It won't update, and carries no data allowance."
            {...(entry.qr
              ? {
                  onShowQr: () =>
                    setQr({
                      title: "vless:// QR",
                      qr: entry.qr!,
                      caption: "A one-off setup. It won't update, and won't show your data allowance.",
                    }),
                }
              : {})}
          />
        ) : null}

        {nothing ? (
          <p className="rounded-lg bg-warn/15 p-3 text-xs text-warn">
            This connection's details couldn't be loaded just now. Close this and try
            Refresh in a minute.
          </p>
        ) : null}
      </div>

      {/*
        Rendered inside this dialog's tree, not beside it. Dialog resolves "innermost" by
        document order when Escape is pressed, so a QR modal mounted elsewhere would take
        this dialog down with it — the bug the "close only the topmost dialog" change
        exists to prevent.
      */}
      {qr ? (
        <QrDialog
          title={qr.title}
          qr={qr.qr}
          caption={qr.caption}
          warning="Anyone who can see this code can use your connection. Close it once you have scanned it."
          onClose={() => setQr(null)}
        />
      ) : null}
    </Dialog>
  );
}

interface Code {
  title: string;
  qr: QRMatrix;
  caption: string;
}

/**
 * A credential, with its two actions against it.
 *
 * Icon buttons rather than labelled ones: two text buttons would be wider than the value
 * they act on, and on a phone that pushes the value down to a line of its own for no
 * gain. Both carry an accessible name, so nothing is lost to a screen reader.
 *
 * The value is shown rather than blurred. The deliberate act the blur exists to require
 * already happened, at the tap that opened the dialog — asking for a second reveal here
 * would be friction with nothing left to protect.
 */
function CredentialRow({
  label,
  value,
  hint,
  onShowQr,
}: {
  label: string;
  value: string;
  hint: string;
  onShowQr?: () => void;
}) {
  return (
    <div>
      <div className="mb-1 text-sm font-medium">{label}</div>
      <div className="flex items-center gap-1">
        <code className="min-w-0 flex-1 truncate rounded-lg border border-line bg-bg px-3 py-2 text-xs">
          {value}
        </code>
        <CopyButton value={value} label={`Copy ${label.toLowerCase()}`} icon />
        {onShowQr ? (
          <IconButton label={`Show ${label.toLowerCase()} as a QR code`} onClick={onShowQr}>
            <QrIcon />
          </IconButton>
        ) : null}
      </div>
      <p className="mt-1 text-xs text-muted">{hint}</p>
    </div>
  );
}

