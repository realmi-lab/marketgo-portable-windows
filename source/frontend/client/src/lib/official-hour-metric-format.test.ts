import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  formatOfficialHourContribution,
  formatOfficialHourMoneyChange,
  formatOfficialHourShare,
  formatOfficialHourTimestamp,
} from "./official-hour-metric-format.ts";

// Read-only production fixture: 2026-09-04 11:00 -> 12:00 KST,
// /api/market-share/official-aligned-contribution?to_ms=1788490800000.
const hour = Object.freeze({
  previousShare: 35.6042677512975,
  currentShare: 35.394890897975706,
  shareDelta: -0.20937685332179257,
  domesticDelta: -9260900122.445312,
  bithumbDelta: -9299934881.38208,
  exchanges: Object.freeze([
    Object.freeze({
      name: "bithumb",
      contribution: -0.208894283813621,
      money: -9299934881.38086,
    }),
    Object.freeze({
      name: "coinone",
      contribution: -0.01204367119236332,
      money: 974205360.5320129,
    }),
    Object.freeze({
      name: "upbit",
      contribution: 0.011561101684191657,
      money: -935170601.5952148,
    }),
  ]),
});

test("selected-hour timestamps stay in Korean time across browser time zones and date boundaries", () => {
  const previousTZ = process.env.TZ;
  try {
    for (const zone of ["Asia/Seoul", "UTC", "America/Los_Angeles"]) {
      process.env.TZ = zone;
      assert.equal(
        formatOfficialHourTimestamp(1788490800000),
        "09. 04. 12:00:00"
      );
      assert.equal(
        formatOfficialHourTimestamp(Date.parse("2026-09-03T15:00:00Z")),
        "09. 04. 00:00:00"
      );
    }
  } finally {
    if (previousTZ === undefined) delete process.env.TZ;
    else process.env.TZ = previousTZ;
  }
  for (const value of [null, undefined, 0, -1, NaN, Infinity, 8.64e15 + 1]) {
    assert.equal(formatOfficialHourTimestamp(value), "시각 정보 없음");
  }
});

test("distinguishes the reported selected-hour values without recomputing them", () => {
  const before = JSON.stringify(hour);
  assert.equal(formatOfficialHourShare(hour.previousShare), "35.60%");
  assert.equal(formatOfficialHourShare(hour.currentShare), "35.39%");
  assert.equal(formatOfficialHourContribution(hour.shareDelta), "-0.21%p");
  assert.equal(formatOfficialHourMoneyChange(hour.domesticDelta), "-92.61억");
  assert.equal(formatOfficialHourMoneyChange(hour.bithumbDelta), "-93.00억");
  assert.deepEqual(
    hour.exchanges.map(row => formatOfficialHourContribution(row.contribution)),
    ["-0.21%p", "-0.01%p", "+0.01%p"]
  );
  assert.deepEqual(
    hour.exchanges.map(row => formatOfficialHourMoneyChange(row.money)),
    ["-93.00억", "+9.74억", "-9.35억"]
  );
  assert.equal(JSON.stringify(hour), before);
});

test("preserves missing, exact zero and signed changes below display precision", () => {
  for (const missing of [undefined, null, NaN, Infinity, -Infinity]) {
    for (const format of [
      formatOfficialHourShare,
      formatOfficialHourContribution,
      formatOfficialHourMoneyChange,
    ]) {
      assert.equal(format(missing), "—");
    }
  }
  // Two decimals, three when two would show 0.00 (the 2026-09-29 rule).
  assert.equal(formatOfficialHourShare(0), "0.00%");
  assert.equal(formatOfficialHourShare(-0), "0.00%");
  assert.equal(formatOfficialHourShare(0.000001), "0.001% 미만");
  assert.equal(formatOfficialHourShare(0.004), "0.004%");
  assert.equal(formatOfficialHourContribution(0), "변화 없음");
  assert.equal(formatOfficialHourContribution(-0), "변화 없음");
  assert.equal(formatOfficialHourContribution(-0.0000001), "0.001%p 미만 하락");
  assert.equal(formatOfficialHourContribution(0.0000001), "0.001%p 미만 상승");
  assert.equal(formatOfficialHourContribution(-0.004), "-0.004%p");
  assert.equal(formatOfficialHourMoneyChange(0), "0.00억");
  assert.equal(formatOfficialHourMoneyChange(1), "0.01억 미만 증가");
  assert.equal(formatOfficialHourMoneyChange(-0.0001), "0.01억 미만 감소");
  assert.equal(
    formatOfficialHourMoneyChange(1_234_567_890_000),
    "+12,345.68억"
  );
});

test("formats raw values without a rounded intermediate at a decimal boundary", () => {
  assert.equal(formatOfficialHourShare(99.999999), "100% 미만");
  assert.equal(formatOfficialHourShare(100), "100.00%");
  assert.equal(formatOfficialHourShare(100.000001), "확인 필요");
  assert.equal(formatOfficialHourShare(35.604249), "35.60%");
  assert.equal(formatOfficialHourContribution(-0.209449), "-0.21%p");
  assert.equal(
    formatOfficialHourMoneyChange(92.6049999999 * 100_000_000),
    "+92.60억"
  );
});

const source = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

test("keeps precise fixed-hour presentation opt-in after the briefing replaced the old summary", () => {
  // The main app's briefing (screens/) replaced the old summary page, which
  // stays opt-in for the other apps that still render it.
  assert.doesNotMatch(source("../App.tsx"), /MarketShareDashboard/);
  assert.doesNotMatch(source("../screens/AppShell.tsx"), /MarketShareDashboard/);
  assert.doesNotMatch(
    source("../../../unix_share/src/App.tsx"),
    /preciseHourlyMetrics/
  );
  for (const path of [
    "../pages/MarketShareDashboard.tsx",
    "../pages/RollingShareSummary2.tsx",
  ]) {
    const component = source(path);
    assert.match(component, /preciseHourlyMetrics = false/);
    assert.match(component, /preciseHourlyMetrics=\{preciseHourlyMetrics\}/);
  }
  assert.match(
    source("../pages/RollingShareSummary2.tsx"),
    /preciseHourlyMetrics \? \(\s*<OfficialSelectedHourMetrics/
  );
  assert.match(
    source("../pages/RollingShareSummary2.tsx"),
    /const selectedHourTimestamp = preciseHourlyMetrics\s*\? formatOfficialHourTimestamp\s*: timestamp;/
  );
  assert.match(
    source("../pages/RollingShareSummary2.tsx"),
    /selectedHourTimestamp\(contribution\.previous\?\.observed_at_ms\)/
  );
  assert.equal(
    (
      source("../pages/RollingShareSummary2.tsx").match(
        /selectedHourTimestamp\(contribution\.current\?\.observed_at_ms\)/g
      ) ?? []
    ).length,
    2
  );
});

test("fixed-hour values have a single semantic copy and do not enter the rounded animation hook", () => {
  const component = source("../components/OfficialSelectedHourMetrics.tsx");
  assert.doesNotMatch(
    component,
    /AnimatedMetricValue|useAnimatedMetrics|sr-only|toFixed/
  );
  assert.match(
    component,
    /formatOfficialHourShare\(\s*contribution\.previous\?\.bithumb_share_pct\s*\)/
  );
  assert.match(
    component,
    /formatOfficialHourContribution\(row\.contribution_pp\)/
  );
  assert.match(component, /정시마다 직전 24시간 합계를 비교합니다/);
  assert.match(component, /최근 한 시간에 새로 체결된 금액이 아닙니다/);
  assert.match(component, /각 값을 따로 반올림/);
  assert.match(component, /거래소별 기여/);
  assert.match(component, /<dt>거래소 전체 금액 변화<\/dt>/);
  assert.match(component, /<dt>가장 큰 기여 코인<\/dt>/);
  assert.match(component, /row\.leading_asset \|\| "확인 자료 없음"/);
});

test("precise fixed-hour layout is main-only and preserves wrapping on small cards", () => {
  const styles = source("../index.css").replace(/\s+/g, " ");
  assert.match(
    styles,
    /\.main-dashboard \.official-hour-precise \.official-selected-hour-exchanges strong\s*\{[^}]*overflow: visible;[^}]*white-space: normal;[^}]*overflow-wrap: anywhere;/
  );
  assert.match(
    styles,
    /@media \(max-width: 767px\)\s*\{\s*\.main-dashboard \.official-hour-precise \.official-selected-hour-exchanges\s*\{\s*grid-template-columns: minmax\(0, 1fr\)/
  );
});
