import assert from "node:assert/strict";
import test from "node:test";

import { observedLiquidityValue } from "./market-share-liquidity.ts";

test("treats zero depth as unavailable when no asset has a live orderbook", () => {
  assert.equal(observedLiquidityValue(0, 0), null);
  assert.equal(observedLiquidityValue(null, 0), null);
});

test("preserves an observed zero as a real value", () => {
  assert.equal(observedLiquidityValue(1, 0), 0);
});

test("rejects non-finite liquidity values", () => {
  assert.equal(observedLiquidityValue(1, Number.NaN), null);
  assert.equal(observedLiquidityValue(1, Number.POSITIVE_INFINITY), null);
});
