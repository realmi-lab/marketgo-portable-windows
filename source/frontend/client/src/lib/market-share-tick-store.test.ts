import assert from "node:assert/strict";
import test from "node:test";

import {
  createMarketShareTickStore,
  type TickStoreScheduler,
} from "./market-share-tick-store.ts";
import type { MarketShareRealtimeTick } from "./market-share-types.ts";

function testScheduler() {
  const callbacks = new Map<number, FrameRequestCallback>();
  let nextID = 1;
  const scheduler: TickStoreScheduler = {
    request: callback => {
      const id = nextID++;
      callbacks.set(id, callback);
      return id;
    },
    cancel: id => callbacks.delete(id),
  };
  return {
    scheduler,
    flush: () => {
      const pending = [...callbacks.values()];
      callbacks.clear();
      pending.forEach(callback => callback(0));
    },
  };
}

test("coalesces live tick notifications to one animation frame", () => {
  const { scheduler, flush } = testScheduler();
  const store = createMarketShareTickStore(scheduler);
  const first = { generated_at: 1 } as MarketShareRealtimeTick;
  const latest = { generated_at: 2 } as MarketShareRealtimeTick;
  let notifications = 0;
  store.subscribe(() => {
    notifications += 1;
  });

  store.publish(first);
  store.publish(latest);
  assert.equal(store.getSnapshot(), latest);
  assert.equal(notifications, 0);
  flush();
  assert.equal(notifications, 1);
});

test("starts with the retained tick without emitting a loading frame", () => {
  const { scheduler } = testScheduler();
  const retained = { generated_at: 7 } as MarketShareRealtimeTick;
  const store = createMarketShareTickStore(scheduler, retained);

  assert.equal(store.getSnapshot(), retained);
});

test("keeps receiving ticks while the displayed snapshot is paused", () => {
  const { scheduler, flush } = testScheduler();
  const store = createMarketShareTickStore(scheduler);
  const first = { generated_at: 1 } as MarketShareRealtimeTick;
  const latest = { generated_at: 2 } as MarketShareRealtimeTick;
  let notifications = 0;
  store.subscribe(() => {
    notifications += 1;
  });

  store.publish(first);
  flush();
  store.pause();
  store.publish(latest);
  flush();
  assert.equal(store.getSnapshot(), first);
  assert.equal(notifications, 1);

  store.resume();
  assert.equal(store.getSnapshot(), latest);
  flush();
  assert.equal(notifications, 2);
});

test("reset clears queued work and the held snapshot", () => {
  const { scheduler, flush } = testScheduler();
  const store = createMarketShareTickStore(scheduler);
  let notifications = 0;
  store.subscribe(() => {
    notifications += 1;
  });
  store.publish({ generated_at: 1 } as MarketShareRealtimeTick);

  store.reset();
  flush();
  assert.equal(store.getSnapshot(), null);
  assert.equal(notifications, 0);
});

test("subscription churn does not retain removed listeners", () => {
  const { scheduler, flush } = testScheduler();
  const store = createMarketShareTickStore(scheduler);
  let removedNotifications = 0;
  const unsubscribes = Array.from({ length: 2_000 }, () =>
    store.subscribe(() => {
      removedNotifications += 1;
    })
  );
  unsubscribes.forEach(unsubscribe => unsubscribe());

  store.publish({ generated_at: 1 } as MarketShareRealtimeTick);
  flush();
  assert.equal(removedNotifications, 0);

  let activeNotifications = 0;
  store.subscribe(() => {
    activeNotifications += 1;
  });
  store.publish({ generated_at: 2 } as MarketShareRealtimeTick);
  flush();
  assert.equal(activeNotifications, 1);

  store.reset();
  store.publish({ generated_at: 3 } as MarketShareRealtimeTick);
  flush();
  assert.equal(activeNotifications, 1);
});
