import assert from "node:assert/strict";
import test from "node:test";

import { buildCoinContributionSummary } from "./coin-contribution-summary.ts";
import type { MarketShareChangeAsset } from "./market-share-types.ts";

function row(
  asset: string,
  contribution: number,
  bithumbGrowth: number,
  competitorGrowth: number
): MarketShareChangeAsset {
  return {
    asset,
    contribution_pp: contribution,
    bithumb_direct_effect_pp: 0,
    competitor_effect_pp: 0,
    domestic_market_size_effect_pp: 0,
    current_domestic_volume_krw: 0,
    previous_domestic_volume_krw: 0,
    domestic_volume_change_krw: bithumbGrowth + competitorGrowth,
    domestic_volume_change_pct: null,
    current_bithumb_volume_krw: 0,
    previous_bithumb_volume_krw: 0,
    bithumb_volume_change_krw: bithumbGrowth,
    bithumb_volume_change_pct: null,
    current_upbit_volume_krw: 0,
    previous_upbit_volume_krw: 0,
    upbit_volume_change_krw: 0,
    upbit_volume_change_pct: null,
    current_competitor_volume_krw: 0,
    previous_competitor_volume_krw: 0,
    competitor_volume_change_krw: competitorGrowth,
    current_bithumb_capture_pct: null,
    previous_bithumb_capture_pct: null,
    bithumb_capture_change_pp: null,
    current_domestic_share_pct: null,
    previous_domestic_share_pct: null,
    current_trade_count: 0,
    previous_trade_count: 0,
  };
}

test("builds independent top-five contribution and volume summaries", () => {
  const summary = buildCoinContributionSummary(
    [
      row("BTC", 1.2, 100, 10),
      row("ETH", -0.8, 50, 300),
      row("XRP", 2.1, -20, 200),
      row("SOL", -1.4, 500, -10),
      row("DOGE", 0, 0, 0),
    ],
    2
  );

  assert.deepEqual(
    summary.positive.map(item => item.asset),
    ["XRP", "BTC"]
  );
  assert.deepEqual(
    summary.negative.map(item => item.asset),
    ["SOL", "ETH"]
  );
  assert.deepEqual(
    summary.bithumbVolumeGrowth.map(item => item.asset),
    ["SOL", "BTC"]
  );
  assert.deepEqual(
    summary.competitorVolumeGrowth.map(item => item.asset),
    ["ETH", "XRP"]
  );
});

test("does not mutate the server response order", () => {
  const input = [row("BTC", 1, 3, 2), row("ETH", 2, 1, 4)];
  buildCoinContributionSummary(input);
  assert.deepEqual(
    input.map(item => item.asset),
    ["BTC", "ETH"]
  );
});
