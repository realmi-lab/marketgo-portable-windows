import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

function source(relativePath: string): string {
  return readFileSync(new URL(relativePath, import.meta.url), "utf8");
}

test("keeps the current page mounted while a lazy route finishes loading", () => {
  const app = source("../App.tsx");
  const shell = source("../screens/AppShell.tsx");
  const dashboard = source("../pages/MarketShareDashboard.tsx");

  assert.match(shell, /startTransition\(\(\) =>\s*setRoute\(/);
  assert.match(dashboard, /startTransition\(\(\) => setMode/);
  assert.doesNotMatch(shell, /화면을 불러오는 중/);
  assert.doesNotMatch(dashboard, /분석 화면을 불러오는 중/);
  assert.match(shell, /<Suspense fallback=\{<div className="mg-page" aria-busy="true" \/>\}>/);
  assert.match(app, /dashboard-route-placeholder/);
  assert.match(dashboard, /dashboard-route-placeholder/);
});

test("positions a new page before paint without animated scroll movement", () => {
  const layout = source("../components/DashboardLayout.tsx");
  const styles = source("../index.css");

  assert.match(layout, /useLayoutEffect/);
  assert.doesNotMatch(layout, /sectionScrollPositionsRef/);
  assert.match(layout, /behavior: "instant"/);
  assert.doesNotMatch(
    styles,
    /\.terminal-shell \.dashboard-content\s*\{[^}]*scroll-behavior:\s*smooth/s
  );
});

test("keeps live value DOM nodes mounted across numeric updates", () => {
  const marketShare = source("../pages/MarketShare.tsx");
  const intelligence = source("../pages/MarketIntelligence.tsx");
  const metric = source("../components/common/AnimatedMetricValue.tsx");

  assert.doesNotMatch(marketShare, /key=\{updateMotion\?\.revision/);
  assert.doesNotMatch(
    intelligence,
    /key=\{`(?:bid|ask)-(?:price|quantity|notional)-/
  );
  assert.doesNotMatch(metric, /signKey/);
});

test("updates the network without a transient refresh badge or animation", () => {
  const graph = source("../screens/OrbitNetwork.tsx");
  const styles = source("../market-intelligence-experience.css");

  assert.doesNotMatch(graph, /isRefreshing|data-updating|새 10초 자료 반영/);
  assert.doesNotMatch(styles, /mi-network-refresh-status|data-updating/);
});

test("retains realtime history and hides background loading state", () => {
  const realtime = source("../components/RealtimeFlowPanel.tsx");
  const intelligence = source("../pages/MarketIntelligence.tsx");

  // Stored states and the day summary survive a remount (no empty flash).
  assert.match(realtime, /retainedRealtimeFlowStates/);
  assert.match(realtime, /retainedRealtimeFlowSummary/);
  assert.match(realtime, /마지막 정상 사건 이력을 유지합니다/);
  assert.match(realtime, /\) : null\}\s*\{filteredEpisodes\.length \? \(/);
  assert.doesNotMatch(realtime, /요약 계산 중|불러오고 있습니다/);
  assert.doesNotMatch(intelligence, /자료 갱신 중/);
  assert.doesNotMatch(intelligence, /animate-spin/);
});
