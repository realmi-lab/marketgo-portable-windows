import assert from "node:assert/strict";
import test from "node:test";

import {
  formatDuration,
  formatKRW,
  formatKrwCompact,
  formatKrwMagnitude,
  formatKrwPrice,
  formatPercentagePointMagnitude,
  formatSignedPercent,
  formatSignedPercentagePoints,
  formatSharePercent,
} from "./formatters.ts";

test("keeps sub-won prices distinct from zero", () => {
  assert.equal(formatKrwPrice(0.00035), "₩0.00035");
  assert.equal(formatKrwPrice(0), "₩0");
  assert.equal(formatKrwPrice(Number.NaN), "—");
  assert.equal(formatKrwPrice(0.000000001), "₩0.000000001");
  assert.equal(formatKrwPrice(0.0049499999999999995), "₩0.00495");
});

test("compact turnover and changes keep sub-won observations distinct from zero", () => {
  assert.equal(formatKRW(0.4), "₩1 미만");
  assert.equal(formatKRW(-0.4), "-₩1 미만");
  assert.equal(formatKrwCompact(0.4), "+₩1 미만");
  assert.equal(formatKrwCompact(-0.4), "-₩1 미만");
  assert.equal(formatKrwMagnitude(-0.4), "₩1 미만");
  assert.equal(formatKrwCompact(-0), "₩0");
  assert.equal(formatKRW(-0), "₩0");
});

test("compact money promotes a rounded amount across won, man, eok and jo boundaries", () => {
  for (const [amount, text] of [
    [9_999, "₩9,999"],
    [9_999.6, "₩1.0만"],
    [10_000, "₩1.0만"],
    [99_999_000, "₩9,999.9만"],
    [759_879_000_000, "₩7,598.79억"],
    [99_999_999, "₩1.00억"],
    [100_000_000, "₩1.00억"],
    [999_999_999_999, "₩1.00조"],
    [1_000_000_000_000, "₩1.00조"],
    [1_234_567_890_000, "₩1.23조"],
  ] as const) {
    assert.equal(formatKRW(amount), text);
    assert.equal(formatKrwCompact(-amount), `-${text}`);
  }
});

test("percentage levels do not turn near-zero or near-complete shares into endpoints", () => {
  assert.equal(formatSharePercent(0), "0.00%");
  assert.equal(formatSharePercent(-0), "0.00%");
  assert.equal(formatSharePercent(0.000001), "0.001% 미만");
  assert.equal(formatSharePercent(0.004), "0.004%");
  assert.equal(formatSharePercent(99.999), "100% 미만");
  assert.equal(formatSharePercent(100), "100.00%");
  assert.equal(formatSharePercent(100.001), "확인 필요");
  assert.equal(formatSharePercent(101), "확인 필요");
  assert.equal(formatSharePercent(-0.001), "확인 필요");
  assert.equal(formatSharePercent(-1), "확인 필요");
  assert.equal(formatSharePercent(37.125, 3), "37.125%");
  for (const value of [null, undefined, NaN, Infinity, -Infinity]) {
    assert.equal(formatSharePercent(value), "—");
  }
});

test("unit prices retain digits instead of using turnover abbreviations", () => {
  assert.equal(formatKrwPrice(10_001), "₩10,001");
  assert.equal(formatKrwPrice(110_657_000), "₩110,657,000");
  assert.equal(formatKrwPrice(12_345.67), "₩12,345.67");
  assert.equal(formatKrwPrice(-12_345.67), "-₩12,345.67");
});

test("unavailable compact money is not a NaN or infinite won amount", () => {
  for (const value of [NaN, Infinity, -Infinity]) {
    assert.equal(formatKRW(value), "—");
  }
  assert.equal(formatKRW(0), "₩0");
  assert.equal(formatKRW(123_450_000), "₩1.23억");
});

test("formats a change magnitude without contradicting increase/decrease prose", () => {
  assert.equal(formatKrwMagnitude(9_078_000), "₩907.8만");
  assert.equal(formatKrwMagnitude(-9_078_000), "₩907.8만");
  assert.equal(formatKrwMagnitude(0), "₩0");
});

test("formats a point magnitude without repeating the direction sign", () => {
  assert.equal(formatPercentagePointMagnitude(1.812), "1.81%p");
  assert.equal(formatPercentagePointMagnitude(-1.812), "1.81%p");
  assert.equal(formatPercentagePointMagnitude(-1.8124, 3), "1.812%p");
  assert.equal(formatPercentagePointMagnitude(-0.004), "0.004%p");
  assert.equal(formatPercentagePointMagnitude(-0.0004), "0.001%p 미만");
  assert.equal(formatPercentagePointMagnitude(0), "변화 없음");
});

test("does not expose a negative zero after rounding signed points", () => {
  assert.equal(formatSignedPercentagePoints(-0.004), "-0.004%p");
  assert.equal(formatSignedPercentagePoints(0.004), "+0.004%p");
  assert.equal(formatSignedPercentagePoints(-0.0004), "0.001%p 미만 하락");
  assert.equal(formatSignedPercentagePoints(0), "변화 없음");
  assert.equal(formatSignedPercentagePoints(-0.006), "-0.01%p");
  assert.equal(formatSignedPercentagePoints(0.006), "+0.01%p");
  assert.equal(formatSignedPercentagePoints(-0.001234, 3), "-0.001%p");
  assert.equal(formatSignedPercentagePoints(0.001234, 4), "+0.0012%p");
});

test("relative percentages keep both small-change direction and a real zero", () => {
  assert.equal(formatSignedPercent(0), "0.00%");
  assert.equal(formatSignedPercent(-0), "0.00%");
  assert.equal(formatSignedPercent(0.001), "+0.001%");
  assert.equal(formatSignedPercent(-0.0004), "0.001% 미만 낮음");
  assert.equal(formatSignedPercent(1.25), "+1.25%");
  assert.equal(formatSignedPercent(-1.25), "-1.25%");
  for (const value of [null, undefined, NaN, Infinity]) {
    assert.equal(formatSignedPercent(value), "—");
  }
});

test("durations keep every part so observed and missing spans add up to the window", () => {
  const observed = 11 * 3_600 + 38 * 60 + 30;
  assert.equal(formatDuration(observed), "11시간 38분 30초");
  assert.equal(formatDuration(86_400 - observed), "12시간 21분 30초");
  assert.equal(formatDuration(6_568), "1시간 49분 28초");
  assert.equal(formatDuration(3_600), "1시간");
  assert.equal(formatDuration(0), "0초");
  for (const value of [null, undefined, NaN, -1]) {
    assert.equal(formatDuration(value), "—");
  }
});
