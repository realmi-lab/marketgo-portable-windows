import assert from "node:assert/strict";
import test from "node:test";

import {
  buildOfficialAlignedContributionPath,
  buildOfficialAlignedHistoryPath,
  INCOMPLETE_OFFICIAL_CONTRIBUTION_RETRY_MS,
  latestSafeOfficialHourMS,
  officialAlignedContributionIsComplete,
  officialHistoryKSTInputValue,
  officialHistorySampleCoverage,
  parseOfficialHistoryKSTInput,
} from "./market-share-official-history.ts";

test("inclusive hourly samples are counted as points rather than elapsed hours", () => {
  assert.equal(
    officialHistorySampleCoverage({
      observed_point_count: 25,
      expected_point_count: 25,
      missing_point_count: 0,
    }),
    "정시 자료 25/25개 확보"
  );
  assert.equal(
    officialHistorySampleCoverage({
      observed_point_count: 21,
      expected_point_count: 25,
      missing_point_count: 4,
    }),
    "정시 자료 21/25개 확보 · 4개 없음"
  );
  assert.equal(
    officialHistorySampleCoverage({
      observed_point_count: 2,
      expected_point_count: 2,
      missing_point_count: 0,
    }),
    "정시 자료 2/2개 확보"
  );
  assert.equal(
    officialHistorySampleCoverage({
      observed_point_count: 0,
      expected_point_count: 25,
      missing_point_count: 25,
    }),
    "정시 자료 0/25개 확보 · 25개 없음"
  );
  assert.equal(officialHistorySampleCoverage(null), "조회 불가");
  for (const observed of [NaN, -1, 26, 1.5]) {
    assert.equal(
      officialHistorySampleCoverage({
        observed_point_count: observed,
        expected_point_count: 25,
        missing_point_count: 0,
      }),
      "정시 자료 수 확인 중"
    );
  }
});

test("official aligned contribution path keeps the selected exact hour", () => {
  assert.equal(
    buildOfficialAlignedContributionPath(1_800_003_600_000),
    "/api/market-share/official-aligned-contribution?to_ms=1800003600000"
  );
});

test("official aligned history path preserves exact endpoints", () => {
  assert.equal(
    buildOfficialAlignedHistoryPath(1_800_000_000_000, 1_800_003_600_000),
    "/api/market-share/official-aligned-history?from_ms=1800000000000&to_ms=1800003600000"
  );
});

test("KST history input round-trips an exact UTC hour", () => {
  const endpoint = Date.parse("2026-08-24T00:00:00+09:00");
  const input = officialHistoryKSTInputValue(endpoint);
  assert.equal(input, "2026-08-24T00:00");
  assert.equal(parseOfficialHistoryKSTInput(input), endpoint);
  assert.equal(parseOfficialHistoryKSTInput("2026-08-24T00:30"), null);
});

test("latest safe official hour honors the close grace", () => {
  assert.equal(
    officialHistoryKSTInputValue(
      latestSafeOfficialHourMS(Date.parse("2026-08-26T10:01:30+09:00"))
    ),
    "2026-08-26T09:00"
  );
  assert.equal(
    officialHistoryKSTInputValue(
      latestSafeOfficialHourMS(Date.parse("2026-08-26T10:02:01+09:00"))
    ),
    "2026-08-26T10:00"
  );
});

test("incomplete aligned contributions stay eligible for a bounded retry", () => {
  const selectedEndMS = 1_800_003_600_000;
  const complete = {
    window_sec: 3_600,
    status: "LIVE",
    endpoint_comparison_available: true,
    missing_sample_count: 0,
    previous: { bucket_ms: selectedEndMS - 3_600_000 },
    current: { bucket_ms: selectedEndMS },
    asset_drivers: [],
    exchange_drivers: [],
  } as Parameters<typeof officialAlignedContributionIsComplete>[0];

  assert.equal(INCOMPLETE_OFFICIAL_CONTRIBUTION_RETRY_MS, 10_000);
  assert.equal(
    officialAlignedContributionIsComplete(complete, selectedEndMS),
    true
  );
  assert.equal(
    officialAlignedContributionIsComplete(
      { ...complete, status: "WARMING", previous: null },
      selectedEndMS
    ),
    false
  );
  assert.equal(
    officialAlignedContributionIsComplete(
      {
        ...complete,
        missing_sample_count: 1,
        endpoint_comparison_available: false,
      },
      selectedEndMS
    ),
    false
  );
});
