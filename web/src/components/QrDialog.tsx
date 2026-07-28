import { Dialog } from "./ui";
import { QrMatrix } from "./QrMatrix";
import type { QRMatrix } from "../api/types";

/**
 * A QR code, alone, behind a deliberate act.
 *
 * Shared by both bundles, which is unusual for this codebase and is the point: a code big
 * enough to scan is big enough for somebody else in the room to photograph, and that is
 * equally true of an operator's screen-share and a subscriber's phone on a train. Having
 * one component means the two cannot drift into disagreeing about how careful to be.
 *
 * It renders nothing on its own — the caller decides when a code goes on screen. Callers
 * should also mount this only while it is open rather than passing `open={false}`: a
 * closed <dialog> keeps its children in the document, and the whole point is that the
 * code is not there until asked for.
 */
export function QrDialog({
  title,
  qr,
  caption,
  warning,
  onClose,
}: {
  title: string;
  qr: QRMatrix;
  /** What this particular code is for. */
  caption?: string;
  /** Who else can use it if they see it. Phrased for the audience by the caller. */
  warning: string;
  onClose: () => void;
}) {
  return (
    <Dialog open onClose={onClose} title={title}>
      <div className="flex flex-col items-center gap-3">
        <QrMatrix qr={qr} label={title} />
        {caption ? (
          <p className="max-w-xs text-center text-xs text-muted">{caption}</p>
        ) : null}
        <p className="max-w-xs text-center text-xs text-warn">{warning}</p>
      </div>
    </Dialog>
  );
}
