import assert from "node:assert/strict";
import test from "node:test";
import { indexRefreshInterval } from "./index-refresh.ts";
import type { IndexDayResponse } from "./index-day.ts";

test("only collecting rolling generations use fast refresh", () => {
  const payload = (status: string, rolling = true) => ({
    storage_mode: rolling ? "ROLLING_24H_LATEST" : undefined,
    rows: [{ status }],
  }) as IndexDayResponse;
  assert.equal(indexRefreshInterval(payload("ROLLING_COLLECTING")), 15_000);
  for (const status of ["ROLLING_OK", "ROLLING_INCOMPLETE", "ROLLING_FAILED", "ROLLING_STALE", "ROLLING_SOURCE_UNAVAILABLE"]) {
    assert.equal(indexRefreshInterval(payload(status)), 60_000);
  }
  assert.equal(indexRefreshInterval(payload("ROLLING_COLLECTING", false)), 60_000);
  assert.equal(indexRefreshInterval(null), 60_000);
});
