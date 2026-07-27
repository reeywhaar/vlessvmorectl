import { describe, expect, it } from "vitest";
import { zeroFill } from "./series";
import { HOUR_MS, snapRange, type SnappedRange } from "../../lib/range";

const H = HOUR_MS;

function hourRange(buckets: number, endMs: number): SnappedRange {
  return {
    fromUnix: (endMs - (buckets - 1) * H) / 1000,
    bucket: "hour",
    stepMs: H,
    from: endMs - (buckets - 1) * H,
    end: endMs,
  };
}

describe("zeroFill", () => {
  /**
   * The node omits empty intervals rather than sending zeros. Without this the chart
   * shows quiet hours as a shorter series instead of as nothing happening.
   */
  it("fills gaps the node omitted", () => {
    const end = Date.parse("2026-07-27T05:00:00Z");
    const range = hourRange(4, end);

    const filled = zeroFill(
      [
        { bucket: "2026-07-27T02:00:00Z", up: 100, down: 200 },
        { bucket: "2026-07-27T05:00:00Z", up: 5, down: 6 },
      ],
      range,
    );

    expect(filled.map((p) => p.total)).toEqual([300, 0, 0, 11]);
    expect(filled).toHaveLength(4);
  });

  /**
   * The final bucket is still accumulating. Without flagging it, every chart appears to
   * end in a sudden crash towards zero, which reads as an outage that never happened.
   */
  it("flags the still-accumulating bucket and only that one", () => {
    const end = Date.parse("2026-07-27T05:00:00Z");
    const filled = zeroFill([], hourRange(3, end));

    expect(filled.map((p) => p.partial)).toEqual([false, false, true]);
  });

  /** A clock skew, or a bucket the node counts as current and we do not, must not lose
   *  real traffic off the right-hand edge. */
  it("extends past the range rather than dropping a later bucket", () => {
    const end = Date.parse("2026-07-27T05:00:00Z");
    const filled = zeroFill([{ bucket: "2026-07-27T06:00:00Z", up: 1, down: 1 }], hourRange(2, end));

    expect(filled).toHaveLength(3);
    expect(filled.at(-1)).toMatchObject({ total: 2, partial: true });
  });

  it("keeps up and down separate as well as summed", () => {
    const end = Date.parse("2026-07-27T05:00:00Z");
    const [point] = zeroFill([{ bucket: "2026-07-27T05:00:00Z", up: 7, down: 11 }], hourRange(1, end));

    expect(point).toMatchObject({ up: 7, down: 11, total: 18 });
  });

  /**
   * A user with no traffic at all — every newly created one — must render an empty chart
   * rather than nothing. The node is expected to send `[]` here; it used to send `null`,
   * which crashed this function on Symbol.iterator.
   */
  it("renders a flat chart for a user with no traffic", () => {
    const end = Date.parse("2026-07-27T05:00:00Z");
    const filled = zeroFill([], hourRange(3, end));

    expect(filled.map((p) => p.total)).toEqual([0, 0, 0]);
    expect(filled.at(-1)?.partial).toBe(true);
  });

  it("ignores an unparseable bucket rather than producing NaN", () => {
    const end = Date.parse("2026-07-27T05:00:00Z");
    const filled = zeroFill([{ bucket: "not a date", up: 1, down: 1 }], hourRange(2, end));

    expect(filled.every((p) => Number.isFinite(p.total))).toBe(true);
  });
});

describe("snapRange", () => {
  /**
   * Snapping is what keeps the request URL — and therefore the query key and the proxy's
   * coalescing key — byte-identical for a whole bucket. Without it every tick mints a new
   * cache entry and a permanent loading flash.
   */
  it("snaps from down to a bucket boundary", () => {
    const now = Date.parse("2026-07-27T05:37:42Z");
    const r = snapRange({ label: "24 hours", bucket: "hour", buckets: 24 }, now);

    expect(new Date(r.end).toISOString()).toBe("2026-07-27T05:00:00.000Z");
    expect(new Date(r.from).toISOString()).toBe("2026-07-26T06:00:00.000Z");
    expect(r.stepMs).toBe(H);
  });

  it("produces the same range for any instant inside one hour", () => {
    const preset = { label: "24 hours", bucket: "hour", buckets: 24 } as const;
    const a = snapRange(preset, Date.parse("2026-07-27T05:00:01Z"));
    const b = snapRange(preset, Date.parse("2026-07-27T05:59:59Z"));

    expect(a.fromUnix).toBe(b.fromUnix);
  });

  it("snaps day buckets to UTC midnight", () => {
    const now = Date.parse("2026-07-27T23:59:00Z");
    const r = snapRange({ label: "30 days", bucket: "day", buckets: 30 }, now);

    expect(new Date(r.end).toISOString()).toBe("2026-07-27T00:00:00.000Z");
  });
});
