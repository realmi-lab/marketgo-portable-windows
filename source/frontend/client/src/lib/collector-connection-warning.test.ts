import assert from "node:assert/strict";
import test from "node:test";
import {
  advanceCollectorWarnings,
  collectorConnectionTone,
  deferredCollectorWarnings,
} from "./collector-connection-warning.ts";
import type { MarketShareReadiness } from "./market-share-types.ts";

const exchanges = ["upbit", "bithumb", "coinone", "korbit", "gopax"];
function ready(blocking: string[] = []): MarketShareReadiness {
  return {
    phase: blocking.length ? "CONNECTING" : "READY",
    enabled_exchange_count: 5,
    configured_exchange_count: 5,
    ready_exchange_count: 5 - blocking.length,
    ready_exchanges: exchanges.filter(exchange => !blocking.includes(exchange)),
    blocking_exchanges: blocking,
    registered_market_count: 500,
    connected_market_count: 500 - blocking.length * 100,
    connected_market_pct: 100 - blocking.length * 20,
    aggregator_consumer_alive: true,
    active_trade_parse_error_chunks: 0,
    elapsed_sec: 100,
    updated_at: 100,
  };
}

test("only the three requested venues receive a bounded presentation grace", () => {
  for (const exchange of exchanges) {
    const snapshot = ready([exchange]);
    const original = structuredClone(snapshot);
    const healthy = advanceCollectorWarnings({}, ready(), 0);
    const disconnected = advanceCollectorWarnings(healthy, snapshot, 1_000);
    const expected = ["gopax", "korbit", "coinone"].includes(exchange)
      ? [exchange]
      : [];
    assert.deepEqual(deferredCollectorWarnings(disconnected, 30_999), expected);
    assert.deepEqual(deferredCollectorWarnings(disconnected, 31_000), []);
    assert.equal(
      collectorConnectionTone(snapshot, expected),
      expected.length ? "muted" : "warn"
    );
    assert.deepEqual(snapshot, original); // Raw readiness/counts stay truthful.
  }
});

test("initial unknown connections are never assumed to have briefly disconnected", () => {
  const state = advanceCollectorWarnings({}, ready(["gopax"]), 1_000);
  assert.deepEqual(deferredCollectorWarnings(state, 1_000), []);
});

test("older frames without exchange lists stay unknown without crashing", () => {
  const legacy = ready();
  Reflect.deleteProperty(legacy, "ready_exchanges");
  Reflect.deleteProperty(legacy, "blocking_exchanges");
  assert.deepEqual(advanceCollectorWarnings({}, legacy, 0), {});
  assert.equal(collectorConnectionTone(legacy, []), "warn");
});

test("new frames and navigation do not restart the thirty-second clock", () => {
  let state = advanceCollectorWarnings({}, ready(), 0);
  state = advanceCollectorWarnings(state, ready(["korbit"]), 1_000);
  state = advanceCollectorWarnings(state, null, 15_000);
  state = advanceCollectorWarnings(state, ready(["korbit"]), 30_000);
  assert.deepEqual(deferredCollectorWarnings(state, 30_999), ["korbit"]);
  assert.deepEqual(deferredCollectorWarnings(state, 31_000), []);
  state = advanceCollectorWarnings(state, ready(), 32_000);
  assert.deepEqual(deferredCollectorWarnings(state, 32_000), []);
  state = advanceCollectorWarnings(state, ready(["korbit"]), 33_000);
  assert.deepEqual(deferredCollectorWarnings(state, 33_000), ["korbit"]);
});

test("each venue keeps its own onset and a major venue still warns immediately", () => {
  let state = advanceCollectorWarnings({}, ready(), 0);
  state = advanceCollectorWarnings(state, ready(["gopax"]), 1_000);
  state = advanceCollectorWarnings(state, ready(["gopax", "coinone"]), 10_000);
  const deferred = deferredCollectorWarnings(state, 31_000);
  assert.deepEqual(deferred, ["coinone"]);
  assert.equal(
    collectorConnectionTone(ready(["gopax", "coinone"]), deferred),
    "warn"
  );
  assert.equal(
    collectorConnectionTone(ready(["upbit", "coinone"]), deferred),
    "warn"
  );
  assert.deepEqual(deferredCollectorWarnings(state, 40_000), []);
});

test("processing, configuration, and consumer failures bypass grace without rearming it", () => {
  for (const fault of [
    { phase: "DEGRADED" as const },
    { active_trade_parse_error_chunks: 1 },
    { aggregator_consumer_alive: false },
    { configured_exchange_count: 4 },
  ]) {
    let state = advanceCollectorWarnings({}, ready(), 0);
    state = advanceCollectorWarnings(state, ready(["coinone"]), 1_000);
    state = advanceCollectorWarnings(
      state,
      { ...ready(["coinone"]), ...fault },
      2_000
    );
    assert.deepEqual(deferredCollectorWarnings(state, 2_000), []);
    state = advanceCollectorWarnings(state, ready(["coinone"]), 3_000);
    assert.deepEqual(deferredCollectorWarnings(state, 3_000), []);
  }
});
