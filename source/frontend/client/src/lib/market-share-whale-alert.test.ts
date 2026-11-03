import assert from "node:assert/strict";
import test from "node:test";

import {
  isUsableWhaleAlertState,
  visibleWhaleAlerts,
  whaleAlertCountLabel,
  whaleAlertFailureNote,
  whaleAlertKindLabel,
  whaleAlertSideLabel,
} from "./market-share-whale-alert.ts";
import type { MarketShareWhaleAlertState } from "./market-share-types.ts";

const state: MarketShareWhaleAlertState = {
  version: "WHALE_ALERT_V1",
  threshold_krw: 500_000_000,
  window_ms: 1_000,
  generated_at_ms: 1_700_000_000_000,
  events: [
    {
      id: "one",
      kind: "SINGLE",
      exchange: "bithumb",
      market: "KRW-BTC",
      asset: "BTC",
      quote: "KRW",
      side: "SELL",
      notional_krw: 600_000_000,
      base_quantity: 4,
      trade_count: 1,
      started_at_ms: 1,
      event_at_ms: 1,
      detected_at_ms: 1,
      window_ms: 0,
    },
    {
      id: "below-threshold",
      kind: "BURST",
      exchange: "upbit",
      market: "KRW-ETH",
      asset: "ETH",
      quote: "KRW",
      side: "BUY",
      notional_krw: 499_999_999,
      base_quantity: 10,
      trade_count: 2,
      started_at_ms: 2,
      event_at_ms: 2,
      detected_at_ms: 2,
      window_ms: 1_000,
    },
  ],
};

test("whale alert presentation keeps only threshold-crossing public executions", () => {
  assert.deepEqual(
    visibleWhaleAlerts(state).map(event => event.id),
    ["one"]
  );
  assert.equal(whaleAlertSideLabel(state.events[0].side), "대형 매도");
  assert.equal(whaleAlertKindLabel(state.events[0].kind), "단일 체결");
});

test("whale states distinguish unavailable zero structs from a confirmed empty list", () => {
  assert.equal(isUsableWhaleAlertState(state), true);
  assert.equal(isUsableWhaleAlertState({ ...state, events: [] }), true);
  assert.equal(
    isUsableWhaleAlertState({ ...state, generated_at_ms: 0, events: [] }),
    true
  );
  for (const broken of [
    null,
    {},
    { ...state, events: null },
    { ...state, events: [null] },
    { ...state, threshold_krw: 0 },
    { ...state, generated_at_ms: -1 },
    { ...state, status: "UNAVAILABLE" },
  ]) {
    assert.equal(isUsableWhaleAlertState(broken), false);
  }
});

test("whale alert status says when the list is old or unknown", () => {
  assert.equal(whaleAlertCountLabel(state), `최근 ${state.events.length}/100건`);
  assert.equal(whaleAlertCountLabel(null), "건수 확인 중");
  assert.equal(whaleAlertFailureNote(state, false), "");
  assert.equal(
    whaleAlertFailureNote(state, true),
    "고래 체결을 갱신하지 못해 마지막 확인 자료를 표시합니다."
  );
  assert.equal(
    whaleAlertFailureNote(null, true),
    "고래 체결 자료를 확인하지 못했습니다. 연결이 복구되면 다시 받습니다."
  );
});
