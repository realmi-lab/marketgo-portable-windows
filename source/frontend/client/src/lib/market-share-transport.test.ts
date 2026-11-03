import assert from "node:assert/strict";
import test from "node:test";
import { marketShareTransportHealth as health } from "./market-share-transport.ts";

test("transport heartbeat preserves stale market status without reconnecting", () => {
  assert.deepEqual(health(40_000, 1_000, 2_000, 39_000, 0), {
    reconnect: false,
    stale: true,
  });
});
test("heartbeat before first data keeps waiting values and marks delayed data", () => {
  assert.deepEqual(health(40_000, 1_000, 0, 39_000, 0), {
    reconnect: false,
    stale: true,
  });
});
test("silent transport reconnects including when no first data arrived", () => {
  assert.equal(health(40_000, 1_000, 2_000, 2_000, 0).reconnect, true);
  assert.equal(health(40_000, 1_000, 0, 0, 0).reconnect, true);
});
test("browser resume grace preserves stale status but delays reconnect", () => {
  assert.deepEqual(health(40_000, 1_000, 2_000, 2_000, 50_000), {
    reconnect: false,
    stale: true,
  });
  assert.equal(health(51_000, 1_000, 2_000, 2_000, 50_000).reconnect, true);
});
