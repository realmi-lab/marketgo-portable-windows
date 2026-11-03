import assert from "node:assert/strict";
import test from "node:test";

import {
  liveChartPointIndex,
  liveChartRange,
  liveChartTime,
  liveChartTicks,
  normalizeChartRange,
} from "./live-chart-explorer.ts";
import {
  appendLiveHistoryPoint,
  type LiveChartHistoryPoint,
} from "./live-chart.ts";

const rows: LiveChartHistoryPoint[] = [
  { at: 1_000, share_bithumb: 20 },
  { at: 1_000.001, gap: 1 },
  { at: 31_000, share_bithumb: null },
  { at: 61_000, share_bithumb: 0, rate_bithumb: 0 },
  { at: 91_000, share_bithumb: 22, partial: 1 },
];

test("time ranges select received rows without interpolating null, zero or connection gaps", () => {
  const before = structuredClone(rows);
  assert.deepEqual(liveChartRange(rows, 30_000), [3, 4]);
  assert.deepEqual(liveChartRange(rows, 60_000), [2, 4]);
  assert.deepEqual(liveChartRange(rows, null), [0, 4]);
  assert.deepEqual(liveChartRange(rows, null, [1_000.001, 61_000]), [1, 3]);
  assert.deepEqual(rows, before);
});

test("held snapshot retains exact timestamps while the live buffer rolls and compacts", () => {
  const held = rows;
  const live = appendLiveHistoryPoint(rows, { at: 500_000, share_bithumb: 30 });
  assert.equal(liveChartPointIndex(live, 1_000), -1);
  assert.equal(liveChartPointIndex(held, 1_000), 0);
  assert.equal(liveChartPointIndex(held, 1_000.0009), -1);
  assert.equal(liveChartPointIndex(held, null), -1);
  assert.equal(held[0].share_bithumb, 20);
});

test("range handles stay ordered and within actual rows, including empty and single-point streams", () => {
  assert.deepEqual(liveChartRange([], 30_000), [0, 0]);
  assert.deepEqual(liveChartRange(rows.slice(0, 1), 30_000), [0, 0]);
  assert.deepEqual(normalizeChartRange(5, 4, 0), [3, 4]);
  assert.deepEqual(normalizeChartRange(5, -20, 80), [0, 4]);
  assert.deepEqual(normalizeChartRange(5, NaN, Infinity), [0, 4]);
});

test("the linked charts use the same explicit Korean time zone", () => {
  assert.equal(liveChartTime(Date.UTC(2026, 9, 27, 3, 4, 5)), "12:04:05");
  assert.equal(liveChartTime(undefined), "—");
  assert.equal(liveChartTime(NaN), "—");
});

test("startup and subsecond histories have distinct time labels drawn from received timestamps", () => {
  assert.deepEqual(
    liveChartTicks([{ at: 1000 }, { at: 1100 }, { at: 1900 }], 5),
    [1900]
  );
  const points = Array.from({ length: 120 }, (_, index) => ({
    at: 1000 + index * 100,
  }));
  for (const count of [3, 5]) {
    const ticks = liveChartTicks(points, count);
    assert.equal(ticks.length, count);
    assert.equal(new Set(ticks.map(at => Math.floor(at / 1000))).size, count);
    assert.ok(ticks.every(at => points.some(row => row.at === at)));
    assert.equal(ticks.at(-1), points.at(-1)?.at);
  }
  assert.deepEqual(liveChartTicks([], 3), []);
});
