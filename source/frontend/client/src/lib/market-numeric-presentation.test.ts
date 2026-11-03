import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  captureMapMarker,
  formatObservationSeconds,
  formatRateDifferenceBps,
  formatRelativeChangePercent,
  formatShareChangeWords,
  formatSlippageImpactKrw,
  formatSlippageRate,
} from "./market-numeric-presentation.ts";

test("slippage rates distinguish a small observed cost from a true zero", () => {
  assert.equal(formatSlippageRate(0.032), "0.001% 미만");
  assert.equal(formatSlippageImpactKrw(10_000_000, 0.032), "₩32");
  assert.equal(formatSlippageRate(0), "0.000%");
  assert.equal(formatSlippageImpactKrw(10_000_000, 0), "₩0");
  assert.equal(formatSlippageRate(1.2), "0.012%");
  assert.equal(formatSlippageRate(-0.032), "0.001% 미만 유리");
  assert.equal(formatSlippageImpactKrw(10_000_000, -0.032), "₩32 유리");
});

test("fractional won impact never becomes a fabricated zero and invalid inputs wait", () => {
  assert.equal(formatSlippageImpactKrw(10_000_000, 0.0004), "1원 미만");
  assert.equal(formatSlippageImpactKrw(10_000_000, -0.0004), "1원 미만 유리");
  for (const value of [null, undefined, NaN, Infinity]) {
    assert.equal(formatSlippageRate(value), "—");
    assert.equal(formatSlippageImpactKrw(10_000_000, value), "계산 대기");
  }
  for (const notional of [NaN, Infinity, -1, 0]) {
    assert.equal(formatSlippageImpactKrw(notional, 1), "계산 대기");
  }
});

test("rate subtraction uses percentage points and preserves tiny or missing changes", () => {
  // 0.30% minus 0.20% is 0.10 percentage points, not a 0.10% relative change.
  assert.equal(formatRateDifferenceBps(30 - 20), "+0.10%p");
  assert.equal(formatRateDifferenceBps(-10), "-0.10%p");
  assert.equal(formatRateDifferenceBps(0.4), "+0.004%p");
  assert.equal(formatRateDifferenceBps(0), "변화 없음");
  assert.equal(formatRateDifferenceBps(-0.001), "0.001%p 미만 낮음");
  for (const value of [null, undefined, NaN, Infinity]) {
    assert.equal(formatRateDifferenceBps(value), "—");
  }
});

test("relative volume changes preserve small direction and do not become signed zero", () => {
  assert.equal(formatRelativeChangePercent(0.004), "0.1% 미만 증가");
  assert.equal(formatRelativeChangePercent(-0.004), "0.1% 미만 감소");
  assert.equal(formatRelativeChangePercent(0), "0.0%");
  assert.equal(formatRelativeChangePercent(2.34), "+2.3%");
  assert.equal(formatRelativeChangePercent(-2.34), "-2.3%");
  assert.equal(formatRelativeChangePercent(null), "—");
});

test("direction prose uses magnitudes and distinguishes flat from unavailable", () => {
  assert.equal(formatShareChangeWords(-1.25), "1.25%p 하락");
  assert.equal(formatShareChangeWords(1.25), "1.25%p 상승");
  assert.equal(formatShareChangeWords(0), "변화 없음");
  assert.equal(formatShareChangeWords(-0.001), "0.001%p 하락");
  assert.equal(formatShareChangeWords(-0.0001), "0.001%p 미만 하락");
  assert.equal(formatShareChangeWords(null), "변화 확인 대기");
});

test("capture map missing contributions and baselines are not neutral evidence", () => {
  assert.deepEqual(captureMapMarker(null, null, 2), {
    contributionPP: null,
    radius: 3.5,
    tone: "unknown",
  });
  assert.deepEqual(captureMapMarker(0, 0, 2), {
    contributionPP: 0,
    radius: 3.5,
    tone: "equal",
  });
  assert.deepEqual(captureMapMarker(-2, -1, 2), {
    contributionPP: -2,
    radius: 11.5,
    tone: "below",
  });
  const invalid = captureMapMarker(NaN, Infinity, NaN);
  assert.equal(invalid.contributionPP, null);
  assert.equal(invalid.tone, "unknown");
  assert.equal(Number.isFinite(invalid.radius), true);
});

test("observation counts preserve actual zero and unavailable duration", () => {
  assert.equal(formatObservationSeconds(0, 3600), "0/3,600초");
  assert.equal(formatObservationSeconds(null, null), "—/—초");
  assert.equal(formatObservationSeconds(NaN, -1), "—/—초");
});


test("quality labels match hourly sample counts and maximum connection ages", () => {
  const view = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  assert.match(view, /officialHistorySampleCoverage\(history\)/);
  assert.doesNotMatch(view, /25시간 모두 확보|"마지막 체결"|"마지막 호가"/);
  assert.match(view, /체결 수신 후 최장 경과/);
  assert.match(view, /호가 수신 후 최장 경과/);
  assert.match(view, /formatCollectorElapsed\(row\.max_trade_age_sec\)/);
  assert.match(view, /formatCollectorElapsed\(row\.max_orderbook_age_sec\)/);
  assert.match(view, /전체 등록 코인/);
  assert.match(view, /전체 등록 거래쌍/);
});

test("realtime observation cannot substitute official analysis amounts for missing trade frames", () => {
  const view = readFileSync(
    new URL("../pages/RollingShareSummary2.tsx", import.meta.url),
    "utf8"
  );
  assert.match(view, /useRef<StableExchangeObservation\[\]>\(\[\]\)/);
  assert.doesNotMatch(view, /fallbackExchanges/);
  assert.doesNotMatch(view, /같은 공식 관측 시점/);
});
