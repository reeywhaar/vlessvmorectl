import type { UsagePoint } from "../../api/types";
import type { SnappedRange } from "../../lib/range";

export interface FilledPoint {
  /** Bucket start, epoch milliseconds. */
  t: number;
  up: number;
  down: number;
  total: number;
  /** True for the bucket that is still accumulating. */
  partial: boolean;
}

/**
 * Fill the gaps in a usage series.
 *
 * The node omits empty intervals rather than sending zeros — API.md is explicit about it,
 * on the grounds that a caller drawing a graph knows the range it asked for. So this has
 * to put them back, or a chart shows quiet hours as a shorter series rather than as
 * nothing happening.
 *
 * Pure, and separated from React entirely, because two of the three things it gets right
 * are only visible in the output and both produce bug reports when missed:
 *
 *  - **The last bucket is still accumulating.** Flagged rather than dropped, so the chart
 *    can render it differently. Without this every graph appears to end in a sudden crash
 *    towards zero, which reads as an outage.
 *  - **Buckets are UTC-aligned.** The node groups on `bucket / secs * secs` over unix
 *    seconds, so hour buckets are UTC hour starts and day buckets are UTC midnights. The
 *    grid here is built on the same alignment; labelling is the caller's problem, and a
 *    day bucket must be labelled as a UTC date or it names the wrong day west of
 *    Greenwich.
 */
export function zeroFill(series: UsagePoint[], r: SnappedRange): FilledPoint[] {
  const byBucket = new Map<number, UsagePoint>();
  let last = r.end;

  for (const p of series) {
    const t = Date.parse(p.bucket);
    if (Number.isNaN(t)) continue;
    byBucket.set(t, p);
    // Extend rather than silently drop: a clock skew between this browser and the node,
    // or a bucket the node counts as current and we do not, would otherwise lose real
    // traffic off the right-hand edge.
    if (t > last) last = t;
  }

  const out: FilledPoint[] = [];
  for (let t = r.from; t <= last; t += r.stepMs) {
    const p = byBucket.get(t);
    const up = p?.up ?? 0;
    const down = p?.down ?? 0;
    out.push({ t, up, down, total: up + down, partial: t === last });
  }
  return out;
}

/**
 * The buckets where the local calendar day turns over.
 *
 * The 7-day chart is 168 hourly buckets, and a chart library left to space the labels
 * itself puts them at whatever interval fits — "6 PM, 11 AM, 4 AM" — which says nothing
 * about where a day begins. These are the ticks that do.
 *
 * Returns bucket values rather than computed midnights, because a bucket is a UTC hour
 * start and local midnight is not one in a half-hour-offset timezone. A computed midnight
 * would then match no category on the axis and be silently dropped; the first bucket of a
 * local day always exists.
 *
 * Never the first point, whose day started before the range did.
 */
export function dayBoundaries(points: FilledPoint[]): number[] {
  const out: number[] = [];
  let prev = "";
  for (const p of points) {
    const day = localDay(p.t);
    if (prev !== "" && day !== prev) out.push(p.t);
    prev = day;
  }
  return out;
}

function localDay(ms: number): string {
  const d = new Date(ms);
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

/**
 * Y-axis ticks at powers of two.
 *
 * Left to itself a chart library picks arithmetically tidy values — 811 MB, 1.6 GB,
 * 2.4 GB — and formatBytes rounds those to "811 MB", "2 GB", "2 GB": an axis with the
 * same label twice, which reads as a rendering fault. A power-of-two step is exact in
 * binary units, so every tick formats to a distinct, round string.
 */
export function byteTicks(max: number, count = 4): number[] {
  if (!Number.isFinite(max) || max <= 0) return [0, 1024];

  const step = 2 ** Math.ceil(Math.log2(max / count));
  const ticks: number[] = [];
  for (let v = 0; v <= max + step / 2; v += step) ticks.push(v);
  // Always close the axis above the tallest column rather than clipping it.
  if (ticks[ticks.length - 1]! < max) ticks.push(ticks.length * step);
  return ticks;
}

/** Peak total across the filled series, for a direct label on the tallest column. */
export function peakOf(points: FilledPoint[]): FilledPoint | null {
  let peak: FilledPoint | null = null;
  for (const p of points) {
    if (!peak || p.total > peak.total) peak = p;
  }
  return peak && peak.total > 0 ? peak : null;
}
