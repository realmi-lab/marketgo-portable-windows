import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { formatLiveTradeRate } from "./live-chart-rate-format.ts";

test("trade rates distinguish observed zero, sparse trading, and missing values", () => {
  assert.equal(formatLiveTradeRate(0), "0회/초");
  for (const rate of [1 / 30, 1 / 60, 1 / 3600, Number.MIN_VALUE, 0.09999]) {
    assert.equal(formatLiveTradeRate(rate), "0.1회/초 미만");
  }
  assert.equal(formatLiveTradeRate(0.1), "0.1회/초");
  assert.equal(formatLiveTradeRate(3.25), "3.3회/초");
  assert.equal(formatLiveTradeRate(20), "20회/초");
  for (const rate of [null, undefined, Number.NaN, Infinity, -Infinity, -1]) {
    assert.equal(formatLiveTradeRate(rate), "—");
  }
});

test("legend and tooltip share the sparse-rate formatter without changing chart data or layout", () => {
  const source = readFileSync(
    new URL("../components/LiveExchangeChart.tsx", import.meta.url),
    "utf8"
  );
  assert.match(source, /formatLiveTradeRate\(trades\)/);
  assert.match(
    source,
    /String\(name\)\.endsWith\("체결\/초"\)\s*\? formatLiveTradeRate\(Number\(value\)\)/
  );
  assert.match(source, /data=\{history\}/);
  assert.match(source, /width=\{compactAxes \? "auto" : 72\}/);
  assert.doesNotMatch(source, /function countRate\(/);
});
