import type { UsagePoint } from "../../api/types";
import type { SnappedRange } from "../../lib/range";
import { formatBytes, parseBytes } from "../../lib/format";

export interface FilledPoint {
  /** Bucket start, epoch milliseconds. */
  t: number;
  up: number;
  down: number;
  total: number;
  /** True for the bucket that is still accumulating. */
  partial: boolean;
  /** How many buckets this point covers: 1, unless groupByHours merged some. Carried so a
   *  label can state the span a column really holds rather than the one it usually holds. */
  span: number;
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
    out.push({ t, up, down, total: up + down, partial: t === last, span: 1 });
  }
  return out;
}

/**
 * Aggregate consecutive hourly buckets into columns of `hours`.
 *
 * Grouped on the *local* hour, so a column never straddles local midnight: each local day
 * owns its own columns, and dayBoundaries still lands exactly on a column start rather than
 * three hours into one. The cost is that the leftmost column can cover fewer hours than the
 * rest — the window starts where it starts — which is why every column carries its span.
 */
export function groupByHours(points: FilledPoint[], hours: number): FilledPoint[] {
  if (hours <= 1) return points;

  const out: FilledPoint[] = [];
  for (const p of points) {
    const open = out[out.length - 1];
    if (!open || new Date(p.t).getHours() % hours === 0) {
      out.push({ ...p });
      continue;
    }
    open.up += p.up;
    open.down += p.down;
    open.total += p.total;
    open.span += p.span;
    // The column holding the accumulating bucket is itself still accumulating.
    open.partial ||= p.partial;
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

/** How the y-axis prints a tick. Shared with byteTicks, which chooses the ticks by what they
 *  print, so the two cannot drift apart. */
export function byteTickLabel(v: number): string {
  return formatBytes(v, 0);
}

/**
 * Y-axis ticks whose labels are exact.
 *
 * Left to itself a chart library picks arithmetically tidy values — 811 MB, 1.6 GB, 2.4 GB —
 * and rounding those to whole units gives "811 MB", "2 GB", "2 GB": an axis with the same
 * label twice, which reads as a rendering fault.
 *
 * A power-of-two step is the start of the answer but not all of it, because a *multiple* of
 * one need not be whole in the unit it prints in: with a peak of 1.9 GB the step is 512 MB
 * and the fourth tick is 1.5 GB, which has no whole-number spelling and comes out as a
 * second "2 GB". So the step doubles until every tick's label parses back to the tick — the
 * one test that catches both a repeated label and a truthful-looking label that is 33% off.
 *
 * `count` is the number of divisions aimed for, not promised: dropping to three honest ticks
 * beats five with a lie among them.
 */
export function byteTicks(max: number, count = 4): number[] {
  if (!Number.isFinite(max) || max <= 0) return [0, 1024];

  let step = 2 ** Math.ceil(Math.log2(max / count));
  let ticks = ticksUpTo(max, step);
  // Terminates at two ticks: zero, and a power of two, whose label is always exact.
  while (ticks.length > 2 && !labelsExact(ticks)) {
    step *= 2;
    ticks = ticksUpTo(max, step);
  }
  return ticks;
}

/** Multiples of step, closing the axis above the tallest column rather than clipping it. */
function ticksUpTo(max: number, step: number): number[] {
  const ticks: number[] = [];
  for (let v = 0; v < max; v += step) ticks.push(v);
  ticks.push(ticks.length * step);
  return ticks;
}

function labelsExact(ticks: number[]): boolean {
  return ticks.every((v) => parseBytes(byteTickLabel(v)) === v);
}

/** Peak total across the filled series, for a direct label on the tallest column. */
export function peakOf(points: FilledPoint[]): FilledPoint | null {
  let peak: FilledPoint | null = null;
  for (const p of points) {
    if (!peak || p.total > peak.total) peak = p;
  }
  return peak && peak.total > 0 ? peak : null;
}
