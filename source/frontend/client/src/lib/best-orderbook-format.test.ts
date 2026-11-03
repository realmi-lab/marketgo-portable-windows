import assert from "node:assert/strict";
import test from "node:test";
import {
  bestBookExactNotional,
  bestBookNotional,
  bestBookPrice,
  bestBookQuantity,
} from "./best-orderbook-format.ts";
import { animatedMetricTarget } from "./animated-metric-target.ts";

test("top-of-book quantities preserve fractional BTC in both static and animated display", () => {
  // Live Upbit BTC ask: 0.00002622 BTC = KRW 2,854.88604, not zero liquidity.
  for (const quantity of [0.00002622, 0.00001, 0.00000001, 0.00003567]) {
    assert.equal(
      bestBookQuantity(quantity),
      quantity.toLocaleString("ko-KR", { maximumSignificantDigits: 10 })
    );
    assert.equal(
      bestBookQuantity(animatedMetricTarget(quantity, 4, true)),
      bestBookQuantity(quantity)
    );
  }
  assert.equal(bestBookQuantity(0.00001, "BTC"), "0.00001 BTC");
  assert.equal(bestBookQuantity(0), "0");
  assert.notEqual(bestBookQuantity(1e-12), "0");
});

test("book amounts and prices distinguish missing, actual zero, sub-won, and unit boundaries", () => {
  assert.equal(bestBookNotional(0.4), "₩1 미만");
  assert.equal(bestBookExactNotional(2854.88604), "₩2,854.88604");
  assert.equal(bestBookNotional(99999999), "₩1.00억");
  assert.equal(bestBookPrice(0.00000001), "₩0.00000001");
  assert.equal(bestBookPrice(1000.5), "₩1,000.5");
  for (const value of [null, undefined, NaN, Infinity, -1]) {
    assert.equal(bestBookQuantity(value), "—");
    assert.equal(bestBookNotional(value), "—");
    assert.equal(bestBookPrice(value), "—");
  }
});

test("reused book formatters preserve the previous locale output and precision", () => {
  const values = [
    0, -0, Number.MIN_VALUE, 1e-12, 9.999e-9, 1e-8,
    0.00002622, 0.0049499999999999995, 1.23456789123456,
    1000.5, 110657000, 12345678912345.5, Number.MAX_VALUE,
  ];
  for (const value of values) {
    assert.equal(bestBookPrice(value), `₩${Math.abs(value).toLocaleString("ko-KR", { maximumSignificantDigits: 12 })}`);
    assert.equal(bestBookExactNotional(value), `₩${value.toLocaleString("ko-KR", { maximumSignificantDigits: 21 })}`);
    for (const significantDigits of [6, 10, 3, 21, 6.5]) {
      const previous = value.toLocaleString("ko-KR", {
        notation: value > 0 && value < 1e-8 ? "scientific" : "standard",
        maximumSignificantDigits: significantDigits,
      });
      assert.equal(bestBookQuantity(value, undefined, significantDigits), previous);
      assert.equal(bestBookQuantity(value, "BTC", significantDigits), `${previous} BTC`);
    }
  }
  for (const significantDigits of [0, 22, NaN, Infinity]) {
    assert.throws(() => bestBookQuantity(1, undefined, significantDigits), RangeError);
  }
  for (const value of [null, undefined, NaN, Infinity, -1]) {
    assert.equal(bestBookExactNotional(value), "—");
  }
});
