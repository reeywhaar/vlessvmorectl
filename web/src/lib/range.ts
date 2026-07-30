export type Bucket = "hour" | "day";

export interface Preset {
  label: string;
  bucket: Bucket;
  buckets: number;
  /**
   * How many buckets one column covers, aggregated in the browser.
   *
   * The node only groups by hour or by day, and 168 hourly columns in a card this wide are
   * three pixels each — thinner than the gap between them, and impossible to hover. Four
   * hours is six columns a day, which reads as a shape.
   */
  group: number;
}

export const PRESETS: Preset[] = [
  { label: "24 hours", bucket: "hour", buckets: 24, group: 1 },
  { label: "7 days", bucket: "hour", buckets: 24 * 7, group: 4 },
  { label: "30 days", bucket: "day", buckets: 30, group: 1 },
  { label: "90 days", bucket: "day", buckets: 90, group: 1 },
];

export interface SnappedRange {
  /** Unix seconds, aligned to a bucket boundary. The shortest stable spelling of
   *  `from`, and one that vlessvmore's parseTime accepts directly. */
  fromUnix: number;
  bucket: Bucket;
  /** Milliseconds per bucket. */
  stepMs: number;
  /** Milliseconds, aligned. */
  from: number;
  /** Start of the current, still-accumulating bucket. */
  end: number;
}

export const HOUR_MS = 3_600_000;
export const DAY_MS = 86_400_000;

/**
 * Snap a range to bucket boundaries.
 *
 * The naive form — `from = now - 24h`, `to = now` — is wrong twice over. The query key
 * would change on every render, so each tick creates a fresh cache entry: a permanent
 * loading flash, unbounded cache growth, and keepPreviousData doing nothing. And the
 * URL would change with it, which also churns the proxy's single-flight key so
 * concurrent tabs stop coalescing.
 *
 * Snapping `from` down to a boundary and omitting `to` entirely — letting the node
 * default it to now — makes the request byte-identical for a whole hour (or day).
 *
 * The node groups on `bucket / secs * secs` over unix seconds, so hour buckets are UTC
 * hour starts and day buckets are UTC midnights. Aligning to the same grid is what
 * makes zeroFill line up.
 */
export function snapRange(preset: Preset, now: number = Date.now()): SnappedRange {
  const stepMs = preset.bucket === "hour" ? HOUR_MS : DAY_MS;
  const end = Math.floor(now / stepMs) * stepMs;
  const from = end - (preset.buckets - 1) * stepMs;
  return { fromUnix: Math.floor(from / 1000), bucket: preset.bucket, stepMs, from, end };
}
