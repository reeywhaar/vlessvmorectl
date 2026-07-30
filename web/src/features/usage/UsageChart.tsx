import {
  Bar,
  BarChart,
  CartesianGrid,
  Rectangle,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type BandPosition,
  type BarShapeProps,
} from "recharts";
import { formatBytes, formatLocalDay, formatLocalHour, formatUTCDate } from "../../lib/format";
import { byteTickLabel, byteTicks, dayBoundaries, type FilledPoint } from "./series";
import { HOUR_MS, type Bucket } from "../../lib/range";

/**
 * Traffic over time.
 *
 * Stacked columns rather than a line. The data is a discrete per-interval aggregate, and
 * a line would interpolate across gaps that are genuine zeros — drawing traffic that
 * never happened. The two series are stacked because their *sum* is the meaningful
 * quantity: the quota is measured against the total, not against either direction.
 *
 * Colours are assigned by direction and never by rank, so hiding one series does not
 * repaint the other. They are also distinct from the accent, which shifts between themes.
 *
 * Lazily imported by the drawer that uses it, so the login and overview screens never
 * carry recharts.
 */
export function UsageChart({ points, bucket }: { points: FilledPoint[]; bucket: Bucket }) {
  // Where the local day turns over. Past a couple of them the hours have stopped being
  // orientation and started being noise — a week of hourly columns labelled "6 PM, 11 AM,
  // 4 AM" says nothing about where a day begins — so the day starts become the labels, and
  // get a hairline each.
  //
  // Below that, at 24 hours, the hour labels are the orientation and midnight is one of
  // them, so this leaves that chart alone rather than drawing a line it does not need.
  const days = bucket === "hour" ? dayBoundaries(points) : [];
  const byDay = days.length >= 2;
  const label = byDay ? formatLocalDay : bucket === "hour" ? formatLocalHour : formatUTCDate;

  // Explicit ticks rather than Recharts' own: see byteTicks for why arithmetically tidy
  // values produce an axis with the same label twice.
  const ticks = byteTicks(Math.max(...points.map((p) => p.total), 0));
  const top = ticks[ticks.length - 1] ?? 0;

  return (
    <div className="h-56 w-full">
      <ResponsiveContainer>
        <BarChart data={points} margin={{ top: 8, right: 4, bottom: 0, left: 4 }} barCategoryGap="18%">
          {/*
            Solid hairlines, never dashed: a dashed grid competes with the marks. Vertically
            there is no grid at all — only the day boundaries, drawn here rather than as
            ReferenceLines so they can sit on a column's edge; see dayEdges.
          */}
          <CartesianGrid
            stroke="var(--color-line)"
            vertical={byDay}
            verticalCoordinatesGenerator={({ xAxis }) => dayEdges(days, xAxis?.scale)}
          />
          <XAxis
            dataKey="t"
            // A spread, because exactOptionalPropertyTypes rejects an explicit undefined.
            {...(byDay ? { ticks: days } : {})}
            tickFormatter={label}
            stroke="var(--color-line)"
            tick={{ fill: "var(--color-muted)", fontSize: 11 }}
            // Kept even with explicit ticks, so a narrow window drops labels rather than
            // overlapping them.
            minTickGap={28}
          />
          {/* One axis, never two. */}
          <YAxis
            ticks={ticks}
            domain={[0, top]}
            tickFormatter={byteTickLabel}
            stroke="var(--color-line)"
            tick={{ fill: "var(--color-muted)", fontSize: 11 }}
            width={56}
          />
          <Tooltip
            cursor={{ fill: "var(--color-line)", opacity: 0.35 }}
            contentStyle={{
              background: "var(--color-card)",
              border: "1px solid var(--color-line)",
              borderRadius: 10,
              color: "var(--color-ink)",
              fontSize: 12,
            }}
            labelFormatter={(t, payload) => bucketLabel(Number(t), bucket, spanOf(payload))}
            formatter={(value, name) => [formatBytes(Number(value)), name === "down" ? "Down" : "Up"]}
          />
          <Bar
            dataKey="down"
            stackId="t"
            fill="var(--color-series-down)"
            name="down"
            shape={bucketShape}
          />
          <Bar
            dataKey="up"
            stackId="t"
            fill="var(--color-series-up)"
            name="up"
            radius={[3, 3, 0, 0]}
            shape={bucketShape}
          />
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

/**
 * One column, faded while its bucket is still accumulating.
 *
 * A `shape` function rather than a `<Cell>` per point: Cell is deprecated in Recharts 3 and
 * goes in 4, and it was the wrong tool anyway — the fade comes from a field on the datum, not
 * from the column's position.
 *
 * The props are picked out rather than spread, because BarShapeProps carries chart internals
 * (tooltipPosition, parentViewBox, …) that React would pass on to the DOM.
 */
function bucketShape({ x, y, width, height, fill, radius, payload }: BarShapeProps) {
  return (
    <Rectangle
      x={x}
      y={y}
      width={width}
      height={height}
      fill={fill}
      radius={radius ?? 0}
      opacity={payload?.partial ? 0.45 : 1}
    />
  );
}

/** Just enough of Recharts' scale to ask where a column starts. */
type BandScale = { map(v: unknown, options?: { position?: BandPosition }): number | undefined };

/**
 * The pixel x of each day boundary, at the start of a column rather than its middle.
 *
 * `position: "start"` is the whole point, and the reason these are gridlines rather than
 * ReferenceLines: a ReferenceLine asks the band scale for the middle of the band, so the line
 * ran down the centre of the day's first column. Invisible while the columns were three pixels
 * wide, plainly wrong once they are four hours of traffic.
 */
function dayEdges(days: number[], scale: BandScale | undefined): number[] {
  if (!scale) return [];
  return days
    .map((t) => scale.map(t, { position: "start" }))
    .filter((x): x is number => x !== undefined);
}

/** How many buckets the hovered column covers, off the datum Recharts hands the tooltip. */
function spanOf(payload: readonly { payload?: FilledPoint }[] | undefined): number {
  return payload?.[0]?.payload?.span ?? 1;
}

/** What one column covers, for the tooltip and for the table's first cell. */
function bucketLabel(t: number, bucket: Bucket, span: number): string {
  const d = new Date(t);
  if (bucket === "day") {
    // Explicitly UTC. Rendering a UTC-midnight bucket in local time labels a 2026-07-25
    // bucket as "Jul 24" anywhere west of Greenwich.
    return `${d.toLocaleDateString(undefined, { timeZone: "UTC", dateStyle: "medium" })} (UTC)`;
  }
  const start = d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
  if (span <= 1) return start;
  // The span rather than the preset's group size, so the short column at the left edge and
  // the accumulating one at the right edge both say what they actually hold.
  const end = new Date(t + span * HOUR_MS);
  return `${start} – ${end.toLocaleTimeString(undefined, { timeStyle: "short" })}`;
}

/**
 * The same numbers as a real table.
 *
 * Not a fallback — a WCAG-clean twin. Nothing in the chart is only available on hover,
 * and an operator pasting figures into a ticket can select them.
 */
export function UsageTable({ points, bucket }: { points: FilledPoint[]; bucket: Bucket }) {
  const rows = [...points].reverse();
  // "Interval" once the columns are groups of hours: every row then names a span, and calling
  // that column "Hour" would be describing the chart this table is a twin of, not this table.
  const grouped = bucket === "hour" && points.some((p) => p.span > 1);
  return (
    <div className="max-h-56 overflow-y-auto rounded-lg border border-line">
      <table className="w-full text-left text-xs">
        <thead className="sticky top-0 bg-card text-muted">
          <tr>
            <th className="px-3 py-2 font-medium">
              {bucket === "day" ? "Day (UTC)" : grouped ? "Interval" : "Hour"}
            </th>
            <th className="px-3 py-2 text-right font-medium">Down</th>
            <th className="px-3 py-2 text-right font-medium">Up</th>
            <th className="px-3 py-2 text-right font-medium">Total</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((p) => (
            <tr key={p.t} className="border-t border-line">
              <td className="px-3 py-1.5">
                {bucket === "day" ? formatUTCDate(p.t) : bucketLabel(p.t, bucket, p.span)}
                {p.partial ? <span className="ml-1 text-muted">(current)</span> : null}
              </td>
              <td className="tnum px-3 py-1.5 text-right">{formatBytes(p.down)}</td>
              <td className="tnum px-3 py-1.5 text-right">{formatBytes(p.up)}</td>
              <td className="tnum px-3 py-1.5 text-right font-medium">{formatBytes(p.total)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
