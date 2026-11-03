import assert from "node:assert/strict";
import test from "node:test";
import {
  formatMarketTimeMS,
  formatRecentMarketTimeMS,
} from "./market-time-format.ts";

test("market timestamps preserve Korean date boundaries regardless of device timezone", () => {
  const previous = process.env.TZ;
  try {
    for (const zone of ["Asia/Seoul", "UTC", "America/Los_Angeles"]) {
      process.env.TZ = zone;
      const midnight = Date.parse("2026-09-03T15:00:00Z");
      assert.equal(formatMarketTimeMS(midnight), "09. 04. 00:00:00");
      assert.equal(formatMarketTimeMS(midnight, "time"), "00:00");
      assert.equal(
        formatMarketTimeMS(midnight + 12345, "time-seconds"),
        "00:00:12"
      );
    }
  } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  }
  for (const value of [null, undefined, 0, -1, NaN, Infinity, 8.64e15 + 1]) {
    assert.equal(formatMarketTimeMS(value), "—");
  }
});

test("a recent time carries its date only when it is not today in Korea", () => {
  // 2026-10-28 07:00 KST is 2026-10-27 22:00 UTC.
  const now = Date.UTC(2026, 9, 27, 22, 0, 0);
  assert.equal(
    formatRecentMarketTimeMS(Date.UTC(2026, 9, 27, 21, 30, 5), now),
    "06:30:05"
  );
  assert.equal(
    formatRecentMarketTimeMS(Date.UTC(2026, 9, 27, 8, 0, 0), now),
    formatMarketTimeMS(Date.UTC(2026, 9, 27, 8, 0, 0), "date-time")
  );
  assert.equal(formatRecentMarketTimeMS(null, now), "—");
});
