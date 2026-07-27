import type { QRMatrix } from "../api/types";

/**
 * Renders vlessvmore's QR bit matrix as SVG.
 *
 * The node returns modules rather than a PNG so a client can draw at any size in any
 * colours. Two things about that are load-bearing:
 *
 *  - The quiet zone must be added. Without `quiet_zone` modules of light margin, many
 *    scanners will not lock on, and the failure looks like a corrupt code rather than a
 *    missing border — which is a very hard thing to diagnose from "it doesn't work".
 *  - The background must be explicitly light, even in dark mode. A QR code is dark
 *    modules on light; inverted, most scanners see nothing.
 *
 * Rows are run-length encoded into one <rect> per run of dark modules rather than one per
 * module. A 57×57 code is ~3,200 elements drawn naively and a few hundred this way.
 */
export function QrMatrix({ qr, size = 220, label }: { qr: QRMatrix; size?: number; label?: string }) {
  const quiet = qr.quiet_zone;
  const side = qr.size + quiet * 2;

  const rects: string[] = [];
  qr.rows.forEach((row, y) => {
    let runStart = -1;
    for (let x = 0; x <= row.length; x++) {
      const dark = row[x] === "1";
      if (dark && runStart < 0) runStart = x;
      if (!dark && runStart >= 0) {
        rects.push(`M${runStart + quiet},${y + quiet}h${x - runStart}v1h-${x - runStart}z`);
        runStart = -1;
      }
    }
  });

  return (
    <svg
      viewBox={`0 0 ${side} ${side}`}
      width={size}
      height={size}
      shapeRendering="crispEdges"
      role="img"
      aria-label={label ?? "Configuration QR code"}
      className="rounded-lg"
    >
      {/* Always white, never var(--color-card): a scanner needs dark-on-light. */}
      <rect width={side} height={side} fill="#ffffff" />
      <path d={rects.join("")} fill="#000000" />
    </svg>
  );
}
