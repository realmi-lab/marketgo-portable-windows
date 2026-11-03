import assert from "node:assert/strict";
import test from "node:test";
import {
  resolveOfficialHourSelection,
  restoreOfficialHourSelection,
  selectedOfficialHourState,
} from "./official-hour-selection.ts";
import { buildOfficialHourlyChartSeries } from "./official-hourly-series.ts";
import type { MarketShareOfficialAlignedContribution } from "./market-share-types.ts";

test("reentry replaces a saved warm-up hour but retains a comparable historical hour", () => {
  const available = new Set([3_600_000, 7_200_000, 10_800_000]);
  assert.equal(
    restoreOfficialHourSelection(3_600_000, available, 10_800_000, 3600),
    10_800_000
  );
  assert.equal(
    restoreOfficialHourSelection(7_200_000, available, 10_800_000, 3600),
    7_200_000
  );
  assert.equal(
    restoreOfficialHourSelection(
      3_600_000,
      new Set([3_600_000]),
      3_600_000,
      3600
    ),
    3_600_000
  );
});

function contribution(): MarketShareOfficialAlignedContribution {
  const point = (bucket_ms: number, bithumb_share_pct: number) => ({
    bucket_ms,
    observed_at_ms: bucket_ms,
    status: "LIVE",
    total_volume_krw: 100,
    bithumb_volume_krw: bithumb_share_pct,
    bithumb_share_pct,
    asset_row_count: 1,
  });
  return {
    status: "LIVE",
    reason: "",
    window_sec: 3_600,
    previous: point(3_600_000, 30),
    current: point(7_200_000, 30.00001),
    share_delta_pp: 0.00001,
    domestic_volume_delta_krw: 0,
    bithumb_volume_delta_krw: 0.00001,
    asset_drivers: [],
    exchange_drivers: [],
    endpoint_comparison_available: true,
    observed_sample_count: 2,
    expected_sample_count: 2,
    missing_sample_count: 0,
  };
}

test("new hour selection hides the old hour's delta while its request is pending", () => {
  const old = contribution();
  assert.deepEqual(selectedOfficialHourState(old, 10_800_000), {
    matchesSelection: false,
    comparisonReady: false,
    shareDeltaPP: null,
  });
  assert.deepEqual(selectedOfficialHourState(old, 7_200_000), {
    matchesSelection: true,
    comparisonReady: true,
    shareDeltaPP: 0.00001,
  });
});

test("same current hour with a nonadjacent endpoint cannot supply a one-hour delta", () => {
  const value = contribution();
  value.previous!.bucket_ms = 1;
  assert.equal(selectedOfficialHourState(value, 7_200_000).shareDeltaPP, null);
  value.previous!.bucket_ms = 3_600_000;
  value.share_delta_pp = 0;
  assert.equal(selectedOfficialHourState(value, 7_200_000).shareDeltaPP, 0);
});

test("a persisted endpoint outside the thirty visible samples returns to the latest visible hour", () => {
  const rows = Array.from({ length: 48 }, (_, index) => ({
    ...contribution().current!,
    bucket_ms: (index + 1) * 3_600_000,
    observed_at_ms: (index + 1) * 3_600_000,
  }));
  const visible = buildOfficialHourlyChartSeries(rows, 3_600, 30);
  const available = new Set(
    visible.filter(point => point.available).map(point => point.bucket_ms)
  );
  const latest = 48 * 3_600_000;
  assert.equal(
    resolveOfficialHourSelection(rows[0].bucket_ms, available, latest),
    latest
  );
  assert.equal(
    resolveOfficialHourSelection(rows[20].bucket_ms, available, latest),
    rows[20].bucket_ms
  );
  assert.equal(resolveOfficialHourSelection(latest, new Set(), latest), 0);
});

test("the live hour follows a newly completed hour while a historical selection stays fixed", () => {
  const available = new Set([3_600_000, 7_200_000, 10_800_000]);
  assert.equal(
    resolveOfficialHourSelection(7_200_000, available, 10_800_000, 7_200_000),
    10_800_000
  );
  assert.equal(
    resolveOfficialHourSelection(3_600_000, available, 10_800_000, 7_200_000),
    3_600_000
  );
  assert.equal(
    resolveOfficialHourSelection(
      7_200_000,
      new Set([7_200_000]),
      10_800_000,
      7_200_000
    ),
    7_200_000
  );
});
