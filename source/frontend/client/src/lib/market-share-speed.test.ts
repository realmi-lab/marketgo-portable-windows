import assert from "node:assert/strict";
import test from "node:test";

import {
  currentTurnoverSpeed,
  hasObservedMomentumRate,
} from "./market-share-speed.ts";
import type { MarketShareMomentum } from "./market-share-types.ts";

function momentum(
  durationSec: number,
  overrides: Partial<MarketShareMomentum> = {}
): MarketShareMomentum {
  return {
    window: `${durationSec}s`,
    duration_sec: durationSec,
    share_pct: 30,
    volume_krw: 1_000_000,
    volume_per_sec_krw: 100_000,
    market_volume_per_sec_krw: 300_000,
    rate_denominator_sec: durationSec,
    status: "CONFIRMED",
    coverage_sec: durationSec,
    window_complete: true,
    ...overrides,
  };
}

test("always selects the current 10-second turnover speed", () => {
  const tenSeconds = momentum(10);
  const rows = [
    momentum(60),
    momentum(600),
    momentum(3_600),
    momentum(86_400),
    tenSeconds,
  ];

  assert.equal(currentTurnoverSpeed(rows), tenSeconds);
});

test("does not display a speed without observed trade coverage", () => {
  assert.equal(currentTurnoverSpeed([momentum(10, { coverage_sec: 0 })]), null);
  assert.equal(
    currentTurnoverSpeed([momentum(10, { status: "UNAVAILABLE" })]),
    null
  );
});

test("keeps a measured zero turnover rate distinct from missing data", () => {
  assert.equal(
    hasObservedMomentumRate(
      momentum(10, {
        volume_krw: 0,
        volume_per_sec_krw: 0,
        market_volume_per_sec_krw: 0,
        share_pct: null,
      })
    ),
    true
  );
  assert.equal(
    hasObservedMomentumRate(momentum(10, { coverage_sec: 0 })),
    false
  );
});
