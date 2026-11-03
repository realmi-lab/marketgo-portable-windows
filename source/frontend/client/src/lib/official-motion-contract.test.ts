import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

function readSource(path: string): string {
  return readFileSync(new URL(path, import.meta.url), "utf8");
}

test("keeps animated metrics stable, accessible, and identity-aware", () => {
  const metric = readSource("../components/common/AnimatedMetricValue.tsx");
  const hook = readSource("../hooks/useAnimatedMetrics.ts");
  const card = readSource("../components/common/KpiCard.tsx");

  assert.match(metric, /resetKey/);
  assert.doesNotMatch(metric, /signKey/);
  assert.match(metric, /aria-hidden="true"/);
  assert.match(metric, /className="sr-only"/);
  assert.match(hook, /frameIntervalMS = 32/);
  assert.match(hook, /Math\.max\(0, durationMS\)/);
  assert.match(hook, /addEventListener\("change"/);
  assert.doesNotMatch(card, /key=\{/);
});

test("retains received live values without substituting official exchange totals", () => {
  const rolling = readSource("../pages/RollingShareSummary2.tsx");
  const realtime = readSource("../components/RealtimeFlowPanel.tsx");

  assert.match(rolling, /fallbackRealtimeFlow/);
  assert.doesNotMatch(rolling, /fallbackExchanges/);
  assert.match(rolling, /selectStableExchangeFrame\(/);
  assert.match(rolling, /domesticExchangeFrameIsComplete\(/);
  assert.doesNotMatch(rolling, /tickLive \? currentTick\.exchanges : \[\]/);
  assert.match(realtime, /selectStableRealtimeFlowState\(/);
  assert.match(
    realtime,
    /const currentTenSecondSample = realtimeFlowTenSecondSample\(currentState\)/
  );
  assert.match(realtime, /마지막 정상 10초/);
});

test("limits motion to bounded value slots and honors reduced motion", () => {
  const styles = readSource("../index.css");
  const trend = readSource("../components/OfficialTrendCharts.tsx");
  const rolling = readSource("../pages/RollingShareSummary2.tsx");

  for (const width of ["compact", "standard", "wide"]) {
    assert.ok(
      styles.includes(`data-metric-width="${width}"`),
      `missing reserved width for ${width}`
    );
  }
  assert.match(
    styles,
    /\.terminal-shell \.dashboard-content[\s\S]*scrollbar-gutter: stable/
  );
  assert.match(styles, /prefers-reduced-motion: reduce/);
  assert.match(styles, /official-data-refresh/);
  assert.match(trend, /<Area[\s\S]*isAnimationActive=\{false\}/);
  assert.match(rolling, /key=\{`\$\{direction\}-\$\{animationKey\}`\}/);
});
