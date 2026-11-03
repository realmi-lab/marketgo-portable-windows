import assert from "node:assert/strict";
import test from "node:test";

import {
  premiumIdentityAllowsReference,
  premiumStatusLabel,
  referencePremiumBps,
} from "./binance-reference.ts";

test("labels a missing converted premium as waiting even when the source is live", () => {
  assert.equal(premiumStatusLabel("LIVE", null), "원화 가격 차이 확인 대기");
  assert.equal(
    premiumStatusLabel("LIVE", Number.NaN),
    "원화 가격 차이 확인 대기"
  );
  assert.equal(premiumStatusLabel("LIVE", 0), "가격 차이 확인");
  assert.equal(
    premiumStatusLabel("BITHUMB_PRICE_UNAVAILABLE", null),
    "빗썸 실시간가 없음"
  );
});

test("uses the server-derived Bithumb premium", () => {
  assert.equal(referencePremiumBps(500, "VERIFIED_EXACT_MARKETS", "LIVE"), 500);
  assert.equal(
    referencePremiumBps(-500, "SYMBOL_MATCH_UNVERIFIED", "LIVE"),
    -500
  );
  assert.equal(referencePremiumBps(0, "EXACT_MARKET_PROOF_MISSING", "LIVE"), 0);
});

test("does not display a missing or invalid server premium", () => {
  for (const premium of [null, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.equal(
      referencePremiumBps(premium, "VERIFIED_EXACT_MARKETS", "LIVE"),
      null
    );
  }
});

test("blocks premium when same-coin identity is missing, ambiguous, or conflicting", () => {
  for (const status of [
    "CONTRACT_IDENTITY_CONFLICT",
    "BITHUMB_KRW_MARKET_UNAVAILABLE",
    "AMBIGUOUS_BITHUMB_KRW_MARKET",
  ] as const) {
    assert.equal(premiumIdentityAllowsReference(status), false);
    assert.equal(referencePremiumBps(500, status, "LIVE"), null);
  }
});

test("blocks premium when the Binance summary or row timestamp is stale", () => {
  for (const status of [
    "STALE_BINANCE_SUMMARY",
    "STALE_BINANCE_CLOSE_TIME",
  ] as const) {
    assert.equal(
      referencePremiumBps(500, "VERIFIED_EXACT_MARKETS", status),
      null
    );
  }
});
