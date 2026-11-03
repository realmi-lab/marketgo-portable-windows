import assert from "node:assert/strict";
import test from "node:test";

import {
  actualMarketRegistryEvents,
  buildMarketRegistryHistoryPath,
  latestMarketRegistryRefreshes,
  normalizeMarketRegistryHistory,
} from "./market-registry-history.ts";
import type {
  MarketRegistryLifecycleEvent,
  MarketRegistryRefreshObservation,
} from "./market-share-types.ts";

function lifecycle(
  eventType: MarketRegistryLifecycleEvent["event_type"],
  observedAtMS: number,
  market: string,
  initialDiscovery = false
): MarketRegistryLifecycleEvent {
  return {
    observed_at_ms: observedAtMS,
    exchange: "upbit",
    market,
    base: "XRP",
    quote: "KRW",
    scope: "domestic",
    event_type: eventType,
    initial_discovery: initialDiscovery,
    maker_fee_rate: null,
    taker_fee_rate: null,
    fee_source: "",
  };
}

function refresh(
  exchange: string,
  observedAtMS: number,
  status = "SUCCESS"
): MarketRegistryRefreshObservation {
  return {
    observed_at_ms: observedAtMS,
    exchange,
    status,
    authoritative: status === "SUCCESS",
    registry_version: 1,
    previous_market_count: 1,
    observed_market_count: 1,
    active_market_count: 1,
    active_asset_count: 1,
    quote_counts: { KRW: 1 },
    public_fee_market_count: 0,
    discovered_count: 0,
    added_count: 0,
    removed_count: 0,
    error: "",
  };
}

test("filters initial baseline and its paired ADDED event", () => {
  const events = [
    lifecycle("DISCOVERED", 1_000, "KRW-BTC"),
    lifecycle("ADDED", 1_000, "KRW-BTC"),
    lifecycle("ADDED", 2_000, "KRW-XRP"),
    lifecycle("REMOVED", 3_000, "KRW-ETH"),
    lifecycle("ADDED", 4_000, "KRW-USDT", true),
  ];
  assert.deepEqual(
    actualMarketRegistryEvents(events).map(item => [
      item.event_type,
      item.market,
    ]),
    [
      ["REMOVED", "KRW-ETH"],
      ["ADDED", "KRW-XRP"],
    ]
  );
});

test("normalizes null collections and malformed rows at the API boundary", () => {
  assert.deepEqual(normalizeMarketRegistryHistory(null), {
    refreshes: [],
    events: [],
    generated_at: 0,
  });
  const normalized = normalizeMarketRegistryHistory({
    refreshes: [null, { observed_at_ms: 1_000, exchange: "UPBIT" }],
    events: [
      null,
      { observed_at_ms: 2_000, exchange: "UPBIT", market: "krw-xrp" },
    ],
    generated_at: null,
  });
  assert.equal(normalized.refreshes[0]?.exchange, "upbit");
  assert.equal(normalized.events[0]?.market, "KRW-XRP");
});

test("selects the latest refresh for each requested exchange", () => {
  const result = latestMarketRegistryRefreshes(
    [
      refresh("upbit", 1_000),
      refresh("upbit", 3_000),
      refresh("bithumb", 2_000, "ERROR"),
    ],
    ["bithumb", "upbit", "coinone"]
  );
  assert.deepEqual(
    result.map(item => [item.exchange, item.observed_at_ms, item.status]),
    [
      ["bithumb", 2_000, "ERROR"],
      ["upbit", 3_000, "SUCCESS"],
    ]
  );
});

test("builds a server-bounded registry history path", () => {
  assert.equal(
    buildMarketRegistryHistoryPath(5_000),
    "/api/market-share/registry-history?limit=200"
  );
  assert.equal(
    buildMarketRegistryHistoryPath(0),
    "/api/market-share/registry-history?limit=1"
  );
});
