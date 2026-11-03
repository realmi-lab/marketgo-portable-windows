import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";

import { buildOfficialContributionInsight } from "./official-contribution-insight.ts";

test("summarizes breadth, concentration and cross-cancellation", () => {
  const result = buildOfficialContributionInsight(
    [
      {
        contribution_pp: 0.5,
        bithumb_volume_change_krw: 1,
        competitor_volume_change_krw: 0,
      },
      {
        contribution_pp: 0.3,
        bithumb_volume_change_krw: 1,
        competitor_volume_change_krw: 0,
      },
      {
        contribution_pp: -0.4,
        bithumb_volume_change_krw: -1,
        competitor_volume_change_krw: 0,
      },
      {
        contribution_pp: -0.2,
        bithumb_volume_change_krw: -1,
        competitor_volume_change_krw: 0,
      },
    ],
    0.2
  );
  assert.equal(result.positiveCount, 2);
  assert.equal(result.available, true);
  assert.equal(result.negativeCount, 2);
  assert.equal(result.positiveGrossPP, 0.8);
  assert.ok(
    result.negativeGrossPP !== null &&
      Math.abs(result.negativeGrossPP - 0.6) < 1e-9
  );
  assert.equal(result.positiveTopFiveSharePct, 100);
  assert.equal(result.negativeTopFiveSharePct, 100);
  assert.ok(
    result.cancellationPct !== null &&
      Math.abs(result.cancellationPct - 85.71428571428572) < 1e-9
  );
});

test("keeps empty contribution insight explicitly unavailable", () => {
  const unavailable = {
    available: false,
    positiveCount: null,
    negativeCount: null,
    positiveGrossPP: null,
    negativeGrossPP: null,
    positiveTopFiveSharePct: null,
    negativeTopFiveSharePct: null,
    cancellationPct: null,
  };
  assert.deepEqual(buildOfficialContributionInsight([], null), unavailable);
  // Valid total endpoints do not prove that any coin evidence was retained.
  assert.deepEqual(buildOfficialContributionInsight([], 0.2), unavailable);
  assert.deepEqual(buildOfficialContributionInsight([], 0), unavailable);
});

test("keeps genuine observed zeroes separate from missing or invalid coin evidence", () => {
  const drivers = [
    {
      contribution_pp: 0,
      bithumb_volume_change_krw: 0,
      competitor_volume_change_krw: 0,
    },
  ];
  const zero = buildOfficialContributionInsight(drivers, 0);
  assert.equal(zero.available, true);
  assert.equal(zero.positiveCount, 0);
  assert.equal(zero.negativeCount, 0);
  assert.equal(zero.positiveGrossPP, 0);
  assert.equal(zero.negativeGrossPP, 0);
  assert.equal(zero.cancellationPct, null);
  for (const net of [null, undefined, Number.NaN, Infinity]) {
    assert.equal(
      buildOfficialContributionInsight(drivers, net).available,
      false
    );
  }
  assert.equal(
    buildOfficialContributionInsight(drivers, 0, false).available,
    false
  );
  for (const invalid of [Number.NaN, Infinity, -Infinity]) {
    const result = buildOfficialContributionInsight(
      [...drivers, { ...drivers[0], contribution_pp: invalid }],
      0
    );
    assert.equal(result.available, false);
    assert.equal(result.positiveGrossPP, null);
  }
});

test("the summary and ranks gate coin evidence independently from endpoint totals", () => {
  const source = readFileSync(
    new URL("../pages/RollingShareSummary2.tsx", import.meta.url),
    "utf8"
  );
  assert.match(
    source,
    /buildOfficialContributionInsight\(\s*contribution\.asset_drivers,\s*contribution\.share_delta_pp,\s*ready && fullDriverListReady/
  );
  assert.match(
    source,
    /const fullDriverListReady =\s*retainedOfficialContributionFullHours\.has\(displayedEndMS\) &&\s*officialAlignedContributionIsComplete\(contribution, displayedEndMS\)/
  );
  assert.match(
    source,
    /!retainedOfficialContributionFullHours\.has\(latestEndMS\) &&/
  );
  assert.match(
    source,
    /value=\{\s*contributionInsight\.negativeGrossPP === null\s*\? null/
  );
  assert.equal(
    (source.match(/available=\{contributionInsight\.available\}/g) ?? [])
      .length,
    2
  );
  assert.match(source, /코인별 계산 자료 확인 중/);
  assert.doesNotMatch(source, /<span>\{rows.length\}\/20<\/span>/);
  assert.match(
    source,
    /selectedExchangeRow\s*\? currentExchangeFrameIsComplete\s*\? "최신 수신값"\s*:\s*"마지막 수신값"\s*:\s*"자료 대기"/
  );
  assert.doesNotMatch(source, /selectedExchangeRow \? "정상"/);
});

test("a streamed top-30 subset cannot claim whole-market breadth or concentration", () => {
  const full = Array.from({ length: 31 }, (_, index) => ({
    contribution_pp: index === 30 ? -0.001 : 1,
    bithumb_volume_change_krw: 1,
    competitor_volume_change_krw: 0,
  }));
  const preview = buildOfficialContributionInsight(
    full.slice(0, 30),
    29.999,
    false
  );
  assert.equal(preview.available, false);
  assert.equal(preview.positiveCount, null);
  assert.equal(preview.negativeCount, null);
  assert.equal(preview.positiveTopFiveSharePct, null);
  const detail = buildOfficialContributionInsight(full, 29.999, true);
  assert.equal(detail.positiveCount, 30);
  assert.equal(detail.negativeCount, 1);
  assert.equal(detail.negativeGrossPP, 0.001);
});
