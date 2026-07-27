import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { formatBytes, formatLocalHour, formatUTCDate } from "../../lib/format";
import { byteTicks, type FilledPoint } from "./series";
import type { Bucket } from "../../lib/range";

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
  const label = (t: number) => (bucket === "hour" ? formatLocalHour(t) : formatUTCDate(t));

  // Explicit ticks rather than Recharts' own: see byteTicks for why arithmetically tidy
  // values produce an axis with the same label twice.
  const ticks = byteTicks(Math.max(...points.map((p) => p.total), 0));
  const top = ticks[ticks.length - 1] ?? 0;

  return (
    <div className="h-56 w-full">
      <ResponsiveContainer>
        <BarChart data={points} margin={{ top: 8, right: 4, bottom: 0, left: 4 }} barCategoryGap="18%">
          {/* Solid hairlines, never dashed: a dashed grid competes with the marks. */}
          <CartesianGrid stroke="var(--color-line)" vertical={false} />
          <XAxis
            dataKey="t"
            tickFormatter={label}
            stroke="var(--color-line)"
            tick={{ fill: "var(--color-muted)", fontSize: 11 }}
            minTickGap={28}
          />
          {/* One axis, never two. */}
          <YAxis
            ticks={ticks}
            domain={[0, top]}
            tickFormatter={(v: number) => formatBytes(v, 0)}
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
            labelFormatter={(t) => tooltipLabel(Number(t), bucket)}
            formatter={(value, name) => [formatBytes(Number(value)), name === "down" ? "Down" : "Up"]}
          />
          <Bar dataKey="down" stackId="t" fill="var(--color-series-down)" name="down">
            {points.map((p) => (
              <Cell key={p.t} opacity={p.partial ? 0.45 : 1} />
            ))}
          </Bar>
          <Bar dataKey="up" stackId="t" fill="var(--color-series-up)" name="up" radius={[3, 3, 0, 0]}>
            {points.map((p) => (
              <Cell key={p.t} opacity={p.partial ? 0.45 : 1} />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}

function tooltipLabel(t: number, bucket: Bucket): string {
  const d = new Date(t);
  if (bucket === "day") {
    // Explicitly UTC. Rendering a UTC-midnight bucket in local time labels a 2026-07-25
    // bucket as "Jul 24" anywhere west of Greenwich.
    return `${d.toLocaleDateString(undefined, { timeZone: "UTC", dateStyle: "medium" })} (UTC)`;
  }
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

/**
 * The same numbers as a real table.
 *
 * Not a fallback — a WCAG-clean twin. Nothing in the chart is only available on hover,
 * and an operator pasting figures into a ticket can select them.
 */
export function UsageTable({ points, bucket }: { points: FilledPoint[]; bucket: Bucket }) {
  const rows = [...points].reverse();
  return (
    <div className="max-h-56 overflow-y-auto rounded-lg border border-line">
      <table className="w-full text-left text-xs">
        <thead className="sticky top-0 bg-card text-muted">
          <tr>
            <th className="px-3 py-2 font-medium">{bucket === "day" ? "Day (UTC)" : "Hour"}</th>
            <th className="px-3 py-2 text-right font-medium">Down</th>
            <th className="px-3 py-2 text-right font-medium">Up</th>
            <th className="px-3 py-2 text-right font-medium">Total</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((p) => (
            <tr key={p.t} className="border-t border-line">
              <td className="px-3 py-1.5">
                {bucket === "day" ? formatUTCDate(p.t) : new Date(p.t).toLocaleString()}
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
