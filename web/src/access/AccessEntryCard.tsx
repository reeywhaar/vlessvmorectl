import { useState } from "react";
import { QrDialog } from "../components/QrDialog";
import {
  Badge,
  ButtonLink,
  Card,
  IconButton,
  QrIcon,
  QuotaMeter,
  SecretField,
  cx,
} from "../components/ui";
import { formatBytes, formatDateTime, quotaState } from "../lib/format";
import { entryStatus } from "./copy";
import type { QRMatrix } from "../api/types";
import type { AccessEntry } from "./types";

/**
 * One VPN account, as its holder sees it.
 *
 * This page gets opened in public, so URLs are blurred until revealed and QR codes are not
 * rendered until their button is pressed — a code can be photographed from across a room,
 * blurred text cannot be read from there.
 */
export function AccessEntryCard({ entry }: { entry: AccessEntry }) {
  const status = entryStatus(entry);

  return (
    <Card className="p-4">
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

      {entry.available ? (
        <>
          <Figures entry={entry} />
          {/* An unconfirmed entry shows no credentials: a stale subscription URL displayed
              as current is what gets copied. */}
          {status.usable ? <Connection entry={entry} /> : null}
        </>
      ) : null}
    </Card>
  );
}

/**
 * Data and expiry on one line.
 *
 * Was two StatTiles, which spent a third of the card height on two short facts. The quota
 * bar stays: it is 3px and reads faster as a shape.
 */
function Figures({ entry }: { entry: Extract<AccessEntry, { available: true }> }) {
  const quota = quotaState(entry);
  const used = quota.unlimited
    ? `${formatBytes(quota.used)} used · no limit`
    : `${formatBytes(quota.used)} of ${formatBytes(quota.limit)} used`;
  const expiry = entry.expires_at ? `expires ${formatDateTime(entry.expires_at)}` : "never expires";

  return (
    <>
      <p className="mt-1 text-xs text-muted">
        {used} · {expiry}
      </p>
      {quota.unlimited ? null : (
        <div className="mt-2">
          <QuotaMeter fraction={quota.fraction} />
        </div>
      )}
    </>
  );
}

/** The credentials, inline and blurred. Ordered least to most technical. */
function Connection({ entry }: { entry: Extract<AccessEntry, { available: true }> }) {
  const [qr, setQr] = useState<Code | null>(null);

  const nothing = !entry.install_url && !entry.subscription_url && !entry.link;

  const qrAction = (title: string, matrix: QRMatrix | undefined, caption: string) =>
    matrix ? (
      <IconButton
        label={`Show ${title.toLowerCase()} as a QR code`}
        onClick={() => setQr({ title, qr: matrix, caption })}
      >
        <QrIcon />
      </IconButton>
    ) : undefined;

  return (
    <div className="mt-3 space-y-3 border-t border-line pt-3">
      {entry.install_url ? (
        <>
          {/* An <a>, not a button: long-press, middle-click and open-in-new-tab all have
              to work when moving a link between devices. */}
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
            New to this? Start here. Already have a VPN app? Use the subscription link.
          </p>
        </>
      ) : null}

      {entry.subscription_url ? (
        <SecretField
          label="Subscription link"
          value={entry.subscription_url}
          hint="Updates itself, and shows your data allowance in the app."
          action={qrAction(
            "Subscription link",
            entry.subscription_qr,
            "Scan this one. It keeps itself up to date and shows your remaining data in the app.",
          )}
        />
      ) : null}

      {entry.link ? (
        <SecretField
          label="One-off vless:// link"
          value={entry.link}
          hint="For apps without subscription support. Does not update."
          action={qrAction(
            "One-off vless:// link",
            entry.qr,
            "A one-off setup. It won't update, and won't show your data allowance.",
          )}
        />
      ) : null}

      {nothing ? (
        <p className="rounded-lg bg-warn/15 p-3 text-xs text-warn">
          This connection's details couldn't be loaded just now. Try Refresh in a minute.
        </p>
      ) : null}

      {/* Mounted only while open: a closed <dialog> keeps its children in the document,
          and the point is that the code is not there until asked for. */}
      {qr ? (
        <QrDialog
          title={qr.title}
          qr={qr.qr}
          caption={qr.caption}
          warning="Anyone who can see this code can use your connection. Close it once you have scanned it."
          onClose={() => setQr(null)}
        />
      ) : null}
    </div>
  );
}

interface Code {
  title: string;
  qr: QRMatrix;
  caption: string;
}
