import assert from "node:assert/strict";
import test from "node:test";

import { createMarketShareRealtimeFlowStore } from "./market-share-realtime-flow-store.ts";
import type { MarketShareRealtimeFlowState } from "./market-share-types.ts";

function state(asOfMS: number): MarketShareRealtimeFlowState {
  return {
    version: "REALTIME_FLOW_V3",
    as_of_ms: asOfMS,
    status: "NORMAL",
    direction: "STABLE",
    reason: "test",
    primary_window_sec: 10,
    share_delta_pp: 0,
    stored_driver_count: 0,
    total_driver_count: 0,
    residual_contribution_pp: 0,
    domestic_volume_1s_krw: 100,
    bithumb_volume_1s_krw: 30,
    bithumb_share_1s_pct: 30,
    recent_share_1s: [],
    windows: [],
    drivers: [],
    exchange_drivers: [],
    current_assets: [],
    data_quality: {},
  };
}

test("starts with the retained realtime frame", () => {
  const retained = state(500);
  const store = createMarketShareRealtimeFlowStore(retained);

  assert.equal(store.getSnapshot(), retained);
});

test("realtime-flow store notifies only its subscribers", () => {
  const store = createMarketShareRealtimeFlowStore();
  let notifications = 0;
  const unsubscribe = store.subscribe(() => {
    notifications += 1;
  });
  const first = state(1_000);

  store.publish(first);
  assert.equal(store.getSnapshot(), first);
  assert.equal(notifications, 1);

  store.publish(first);
  assert.equal(notifications, 1);

  unsubscribe();
  store.publish(state(2_000));
  assert.equal(notifications, 1);
});

test("realtime-flow store reset clears state and subscriptions", () => {
  const store = createMarketShareRealtimeFlowStore();
  let notifications = 0;
  store.subscribe(() => {
    notifications += 1;
  });

  store.publish(state(1_000));
  store.reset();
  store.publish(state(2_000));

  assert.equal(store.getSnapshot()?.as_of_ms, 2_000);
  assert.equal(notifications, 1);
});

test("a delayed analysis cannot rewind the latest one-second frame", () => {
  const store = createMarketShareRealtimeFlowStore();
  const observed: number[] = [];
  store.subscribe(() => observed.push(store.getSnapshot()!.as_of_ms));
  const live = state(1788533042000);

  store.publish(live);
  // Actual arrival order: the slower analysis carried a frame five seconds old.
  store.publish(state(1788533037000));
  assert.equal(store.getSnapshot(), live);
  assert.deepEqual(observed, [1788533042000]);

  store.publish(state(1788533043000));
  assert.deepEqual(observed, [1788533042000, 1788533043000]);
});

test("retained frames stay current while same-time quality corrections apply", () => {
  const retained = state(1788533042000);
  const store = createMarketShareRealtimeFlowStore(retained);
  store.publish(state(1788533037000));
  assert.equal(store.getSnapshot(), retained);

  const corrected = { ...retained, status: "DEGRADED" as const };
  store.publish(corrected);
  assert.equal(store.getSnapshot(), corrected);

  store.publish(null);
  assert.equal(store.getSnapshot(), null);
  store.publish(state(1788533037000));
  assert.equal(store.getSnapshot()?.as_of_ms, 1788533037000);
});
