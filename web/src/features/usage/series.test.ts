import { describe, expect, it } from "vitest";
import { byteTickLabel, byteTicks, dayBoundaries, zeroFill, type FilledPoint } from "./series";
import { HOUR_MS, snapRange, type SnappedRange } from "../../lib/range";
import { parseBytes } from "../../lib/format";

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

describe("dayBoundaries", () => {
  /** Hourly buckets from a local midnight, which is what the 7-day chart is made of. */
  function hourly(start: number, count: number): FilledPoint[] {
    return Array.from({ length: count }, (_, i) => ({
      t: start + i * H,
      up: 0,
      down: 0,
      total: 0,
      partial: i === count - 1,
    }));
  }

  it("marks the first bucket of each day but not the first bucket of the range", () => {
    const midnight = new Date(2026, 6, 27, 0, 0, 0, 0).getTime();

    expect(dayBoundaries(hourly(midnight, 72))).toEqual([
      new Date(2026, 6, 28, 0, 0, 0, 0).getTime(),
      new Date(2026, 6, 29, 0, 0, 0, 0).getTime(),
    ]);
  });

  /** The 24-hour chart starts mid-day, so its one boundary is wherever the date turns over. */
  it("finds the turnover in a range that does not start at midnight", () => {
    const noon = new Date(2026, 6, 27, 12, 0, 0, 0).getTime();

    expect(dayBoundaries(hourly(noon, 24))).toEqual([new Date(2026, 6, 28, 0, 0, 0, 0).getTime()]);
  });

  // What tells UsageChart to keep labelling hours rather than days.
  it("returns nothing for a range inside one day", () => {
    const morning = new Date(2026, 6, 27, 1, 0, 0, 0).getTime();

    expect(dayBoundaries(hourly(morning, 6))).toEqual([]);
    expect(dayBoundaries([])).toEqual([]);
  });

  /**
   * Local, not UTC. A bucket is a UTC hour start, so anywhere east or west of Greenwich the
   * UTC date turns over in the middle of somebody's afternoon — and a boundary drawn there
   * would sit under a column labelled 4 PM.
   */
  it("splits on the local date, not the UTC one", () => {
    const localMidnight = new Date(2026, 6, 28, 0, 0, 0, 0);
    const [boundary] = dayBoundaries(hourly(localMidnight.getTime() - 3 * H, 6));

    expect(boundary).toBe(localMidnight.getTime());
    expect(new Date(boundary!).getHours()).toBe(0);
  });
});

describe("byteTicks", () => {
  /**
   * A spread of peaks, including the band that used to break: anything between 1 and 2 GB
   * gets a 512 MB step, whose third multiple is 1.5 GB — no whole-number spelling, so the
   * axis printed "0 B, 512 MB, 1 GB, 2 GB, 2 GB" and read as a rendering fault.
   */
  const peaks = [
    1,
    900,
    5_000,
    3_100_000,
    700e6,
    900 * 1024 ** 2,
    1.5 * 1024 ** 3,
    1.9 * 1024 ** 3,
    2.06e9,
    2.9 * 1024 ** 3,
    1.4 * 1024 ** 4,
  ];

  it("never produces two ticks with the same label", () => {
    for (const max of peaks) {
      const labels = byteTicks(max).map(byteTickLabel);
      expect(new Set(labels).size, `duplicate label in ${labels.join(", ")}`).toBe(labels.length);
    }
  });

  /**
   * The stronger property, and the one distinctness was standing in for: a label must be the
   * value it sits next to. "0 B, 512 MB, 1 GB, 1.5 GB→2 GB" has no duplicates once the top
   * tick is 1.5 GB itself, and is still an axis whose top says 2 GB about 1.5.
   */
  it("labels every tick exactly", () => {
    for (const max of peaks) {
      for (const tick of byteTicks(max)) {
        expect(parseBytes(byteTickLabel(tick)), `${byteTickLabel(tick)} is not ${tick}`).toBe(tick);
      }
    }
  });

  it("starts at zero and closes above the tallest column", () => {
    for (const max of [1, 900, 2.9 * 1024 ** 3, 613 * 1024 ** 3]) {
      const ticks = byteTicks(max);
      expect(ticks[0]).toBe(0);
      expect(ticks.at(-1)).toBeGreaterThanOrEqual(max);
    }
  });

  it("uses a power-of-two step, which is what makes the labels exact", () => {
    const ticks = byteTicks(2.9 * 1024 ** 3);
    const step = ticks[1]!;
    expect(Math.log2(step) % 1).toBe(0);
    ticks.forEach((v, i) => expect(v).toBe(i * step));
  });

  it("survives an all-zero series", () => {
    expect(() => byteTicks(0)).not.toThrow();
    expect(byteTicks(0)[0]).toBe(0);
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
