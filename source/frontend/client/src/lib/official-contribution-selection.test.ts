import assert from "node:assert/strict";
import test from "node:test";
import { selectOfficialContributions } from "./official-contribution-selection.ts";
import type {
  MarketShareOfficialMovementEvent,
  MarketShareOfficialTicker1mState,
} from "./market-share-types.ts";
const old = {
  event_id: "ended",
  drivers: [{ asset: "RAY" }],
  exchange_drivers: [{ exchange: "bithumb" }],
} as MarketShareOfficialMovementEvent;
const live = {
  status: "LIVE",
  previous: { bucket_ms: 1 },
  current: { bucket_ms: 2 },
  drivers: [{ asset: "USDC" }],
  exchange_drivers: [{ exchange: "upbit" }],
} as MarketShareOfficialTicker1mState;
test("default follows new hourly contributions despite retained ended events", () => {
  assert.equal(
    selectOfficialContributions([old], "", live).drivers[0].asset,
    "USDC"
  );
  const next = { ...live, drivers: [{ ...live.drivers[0], asset: "BTC" }] };
  assert.equal(
    selectOfficialContributions([old], "", next).drivers[0].asset,
    "BTC"
  );
  assert.equal(
    selectOfficialContributions([], "", live).exchangeRows[0].exchange,
    "upbit"
  );
});
test("history is explicit and latest can be restored", () => {
  assert.equal(selectOfficialContributions([old], "ended", live).selected, old);
  assert.equal(
    selectOfficialContributions([old], "ended", live).drivers,
    old.drivers
  );
  assert.equal(
    selectOfficialContributions([old], "", live).drivers,
    live.drivers
  );
  assert.equal(
    selectOfficialContributions([], "ended", live).drivers,
    live.drivers
  );
});
test("missing latest evidence never falls back to historical numbers", () => {
  for (const trend of [
    null,
    { ...live, status: "GAP" },
    { ...live, previous: null },
  ]) {
    const result = selectOfficialContributions([old], "", trend);
    assert.deepEqual(result.drivers, []);
    assert.deepEqual(result.exchangeRows, []);
  }
});
