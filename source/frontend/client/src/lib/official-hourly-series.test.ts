import assert from "node:assert/strict";
import test from "node:test";

import {
  buildOfficialHourlyChartSeries,
  mergeOfficialHourlyPoints,
  OFFICIAL_HOURLY_CHART_POINT_COUNT,
  officialHourlySeriesCoverageLabel,
  officialHourlyHistoryFromMS,
} from "./official-hourly-series.ts";
import type { MarketShareOfficialTicker1mPoint } from "./market-share-types.ts";

test("a single live endpoint still queries thirty durable hourly points", () => {
  const end = 1788660000000;
  assert.equal(officialHourlyHistoryFromMS(end), end - 29 * 3_600_000);
  assert.equal(officialHourlyHistoryFromMS(0), 0);
});

function point(
  bucketMS: number,
  share: number
): MarketShareOfficialTicker1mPoint {
  return {
    bucket_ms: bucketMS,
    observed_at_ms: bucketMS,
    status: "LIVE",
    total_volume_krw: 100,
    bithumb_volume_krw: share,
    bithumb_share_pct: share,
    asset_row_count: 1,
  };
}

test("inserts an unavailable bucket instead of joining a two-hour gap", () => {
  assert.deepEqual(
    buildOfficialHourlyChartSeries(
      [point(3_600_000, 30), point(10_800_000, 32)],
      3_600,
      30
    ).map(item => ({ bucket: item.bucket_ms, share: item.share })),
    [
      { bucket: 3_600_000, share: 30 },
      { bucket: 7_200_000, share: null },
      { bucket: 10_800_000, share: 32 },
    ]
  );
});

test("thirty inclusive hourly samples span twenty-nine hours, with missing counts separate", () => {
  const rows = Array.from({ length: 30 }, (_, index) =>
    point((index + 1) * 3_600_000, 30)
  );
  const full = buildOfficialHourlyChartSeries(rows, 3_600, 30);
  assert.equal(
    officialHourlySeriesCoverageLabel(full),
    "정시 자료 30/30개 · 29시간 구간"
  );
  const partial = buildOfficialHourlyChartSeries(
    rows.filter((_, index) => index !== 12),
    3_600,
    30
  );
  assert.equal(
    officialHourlySeriesCoverageLabel(partial),
    "정시 자료 29/30개 · 29시간 구간"
  );
  assert.equal(officialHourlySeriesCoverageLabel([]), "정시 자료 없음");
});

test("keeps only the latest requested number of hourly buckets", () => {
  const rows = Array.from({ length: 40 }, (_, index) =>
    point((index + 1) * 3_600_000, 30 + index / 10)
  );
  const result = buildOfficialHourlyChartSeries(rows, 3_600, 30);
  assert.equal(result.length, 30);
  assert.equal(result[0].bucket_ms, 11 * 3_600_000);
  assert.equal(result.at(-1)?.bucket_ms, 40 * 3_600_000);
});

test("fills live-history gaps from persisted official endpoints", () => {
  const result = mergeOfficialHourlyPoints(
    [point(3_600_000, 30), point(7_200_000, 31)],
    [point(3_600_000, 32), point(10_800_000, 33)]
  );

  assert.equal(OFFICIAL_HOURLY_CHART_POINT_COUNT, 30);
  assert.deepEqual(
    result.map(item => ({
      bucket: item.bucket_ms,
      share: item.bithumb_share_pct,
    })),
    [
      { bucket: 3_600_000, share: 32 },
      { bucket: 7_200_000, share: 31 },
      { bucket: 10_800_000, share: 33 },
    ]
  );
});
