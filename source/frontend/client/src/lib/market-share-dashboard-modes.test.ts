import assert from "node:assert/strict";
import test from "node:test";

import {
  initialMarketShareDashboardMode,
  MARKET_SHARE_DASHBOARD_MODES,
  marketShareViewForDashboardMode,
} from "./market-share-dashboard-modes.ts";

test("keeps official movement, selected summary and selected detail reachable", () => {
  assert.deepEqual(MARKET_SHARE_DASHBOARD_MODES, [
    "movement",
    "summary",
    "window",
  ]);
  assert.equal(marketShareViewForDashboardMode("movement"), null);
  assert.equal(marketShareViewForDashboardMode("summary"), "summary");
  assert.equal(marketShareViewForDashboardMode("window"), "overview");
});

test("preserves the legacy overview hash entry point", () => {
  assert.equal(initialMarketShareDashboardMode("#overview"), "window");
  assert.equal(initialMarketShareDashboardMode("#summary"), "movement");
});
