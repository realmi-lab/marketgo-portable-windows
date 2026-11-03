import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  buildLiveChartMobileAxes,
  formatLiveChartAxisTick,
} from "./live-chart-mobile.ts";

test("mobile rate axes share explicit units without changing the input history", () => {
  const history = [
    { at: 1, rate_bithumb: 14_000_000, trades_bithumb: 24 },
    { at: 2, rate_upbit: 22_000_000, trades_upbit: 10.5 },
    { at: 3, gap: 1 as const, rate_bithumb: null },
  ];
  const before = structuredClone(history);
  const axes = buildLiveChartMobileAxes(history);
  assert.deepEqual(axes, {
    volume: { divisor: 10_000, unit: "만원/초" },
    trades: { divisor: 1, unit: "회/초" },
  });
  assert.equal(
    formatLiveChartAxisTick(14_000_000, axes.volume.divisor),
    "1400"
  );
  assert.equal(formatLiveChartAxisTick(24, axes.trades.divisor), "24");
  assert.deepEqual(history, before);
});

test("axis units follow the visible finite values including large and missing inputs", () => {
  assert.deepEqual(buildLiveChartMobileAxes([]), {
    volume: { divisor: 1, unit: "원/초" },
    trades: { divisor: 1, unit: "회/초" },
  });
  const axes = buildLiveChartMobileAxes([
    { at: 1, rate_bithumb: 1e8, rate_upbit: Infinity, trades_upbit: 25_000 },
    { at: 2, rate_bithumb: null, rate_coinone: 1e12 },
  ]);
  assert.deepEqual(axes, {
    volume: { divisor: 1e12, unit: "조원/초" },
    trades: { divisor: 1e4, unit: "만회/초" },
  });
  assert.equal(formatLiveChartAxisTick(NaN, 1), "—");
  assert.notEqual(formatLiveChartAxisTick(0.00001, 1), "0");
  assert.equal(formatLiveChartAxisTick(-0, 1), "0");
});

test("compact axes are main-mobile only and retain chart data and detailed tooltips", () => {
  const source = readFileSync(
    new URL("../components/LiveExchangeChart.tsx", import.meta.url),
    "utf8"
  );
  assert.match(source, /closest\("\.main-dashboard, \.mg"\)/);
  assert.match(source, /matchMedia\("\(max-width: 767px\)"\)/);
  assert.match(source, /addEventListener\("change", updateCompactAxes\)/);
  assert.match(source, /removeEventListener\("change", updateCompactAxes\)/);
  assert.equal(
    (source.match(/width=\{compactAxes \? "auto" : 72\}/g) ?? []).length,
    2
  );
  assert.match(source, /fontSize: 13/);
  assert.match(source, /거래금액\(\{mobileAxes\.volume\.unit\}\)/);
  assert.match(source, /체결\(\{mobileAxes\.trades\.unit\}\)/);
  assert.match(source, /data=\{history\}/);
  assert.match(source, /domain=\{shareChart \? shareDomain : \[0, "auto"\]\}/);
  assert.match(source, /percent\(Number\(value\), 2\)/);
  assert.match(source, /`\$\{money\(Number\(value\)\)\}\/초`/);
  assert.match(source, /live-exchange-chart-plot/);
  assert.match(
    source,
    /interval=\{compactAxes \? "preserveStartEnd" : undefined\}/
  );
});
