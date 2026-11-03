import assert from "node:assert/strict";
import test from "node:test";

import {
  buildPriceGapCandidate,
  formatRelativePricePercent,
  parsePercentRate,
} from "./binance-price-gap.ts";

test("relative price gaps do not claim a price rose or fell over time", () => {
  assert.equal(formatRelativePricePercent(0.001), "0.01% 미만 높음");
  assert.equal(formatRelativePricePercent(-0.001), "0.01% 미만 낮음");
  assert.equal(formatRelativePricePercent(0), "0.00%");
  assert.equal(formatRelativePricePercent(1.25), "+1.25%");
  assert.equal(formatRelativePricePercent(-1.25), "-1.25%");
  for (const value of [null, undefined, NaN, Infinity]) {
    assert.equal(formatRelativePricePercent(value), "—");
  }
});

test("crossed Binance quotes cannot create an apparent positive price-gap candidate", () => {
  assert.equal(buildPriceGapCandidate(100, 102, 98, 0, 0), null);
  assert.notEqual(buildPriceGapCandidate(100, 100, 100, 0, 0), null);
});

test("selects the stronger executable-side Binance and Bithumb price gap", () => {
  const candidate = buildPriceGapCandidate(105, 99, 100, 0.0004, 0.001);
  assert.equal(candidate?.gross.direction, "Binance → 빗썸");
  assert.ok(Math.abs((candidate?.gross.gross ?? 0) - 0.05) < 1e-12);
  assert.ok((candidate?.net?.net ?? 0) < (candidate?.gross.gross ?? 0));
});

test("keeps reverse-premium direction available", () => {
  const candidate = buildPriceGapCandidate(95, 100, 101, 0, 0);
  assert.equal(candidate?.gross.direction, "빗썸 → Binance");
  assert.ok((candidate?.gross.gross ?? 0) > 0.05);
});

test("rejects unusable prices and parses editable percent assumptions", () => {
  assert.equal(buildPriceGapCandidate(null, 100, 101, 0, 0), null);
  assert.equal(parsePercentRate("0.10"), 0.001);
  assert.equal(parsePercentRate("0"), 0);
  assert.equal(parsePercentRate("100"), 1);
  assert.equal(parsePercentRate(" .04 "), 0.0004);
  for (const value of [
    "invalid",
    "",
    " ",
    "-1",
    "100.1",
    "NaN",
    "Infinity",
    "0x10",
  ]) {
    assert.equal(parsePercentRate(value), null);
  }
});

test("invalid fee assumptions preserve gross price comparisons without inventing net returns", () => {
  for (const fee of [null, NaN, Infinity, -0.01, 1.01]) {
    const candidate = buildPriceGapCandidate(105, 99, 100, fee, 0.001);
    assert.ok(Math.abs((candidate?.gross.gross ?? 0) - 0.05) < 1e-12);
    assert.equal(candidate?.net, null);
    assert.equal(candidate?.gross.net, null);
    assert.equal(buildPriceGapCandidate(105, 99, 100, 0, fee)?.net, null);
  }
  const zeroFee = buildPriceGapCandidate(105, 99, 100, 0, 0);
  assert.equal(zeroFee?.net?.net, zeroFee?.gross.gross);
});
