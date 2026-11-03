import assert from "node:assert/strict";
import test from "node:test";
import { formatNoticeScheduleRange, formatNoticeTime } from "./notice-time.ts";

const start = Date.parse("2026-09-05T18:07:39+09:00");
const end = Date.parse("2026-09-05T19:08:41+09:00");

test("minute and hour schedules never display finer source precision", () => {
  assert.equal(
    formatNoticeScheduleRange({
      start_ts_ms: start,
      end_ts_ms: end,
      precision: "MINUTE",
      timezone: "KST",
    }),
    "2026. 9. 5. 18:07 → 2026. 9. 5. 19:08"
  );
  assert.equal(
    formatNoticeScheduleRange({
      start_ts_ms: start,
      end_ts_ms: 0,
      precision: "HOUR",
      timezone: "KST",
    }),
    "2026. 9. 5. 18시"
  );
});

test("schedule precision respects explicit timezone and unknown endpoints", () => {
  assert.equal(
    formatNoticeScheduleRange({
      start_ts_ms: 0,
      end_ts_ms: end,
      precision: "HOUR",
      timezone: "KST",
    }),
    "2026. 9. 5. 19시"
  );
  assert.equal(
    formatNoticeScheduleRange({
      start_ts_ms: start,
      end_ts_ms: 0,
      precision: "MINUTE",
      timezone: "UTC",
    }),
    "2026. 9. 5. 09:07 (UTC)"
  );
  for (const precision of ["MINUTE", "HOUR"]) {
    for (const value of [0, NaN, Infinity, Number.MAX_VALUE]) {
      assert.equal(
        formatNoticeScheduleRange({
          start_ts_ms: value,
          end_ts_ms: 0,
          precision,
          timezone: "UTC",
        }),
        "미정"
      );
    }
  }
});

test("second, day, recurring and unknown schedule behavior stays unchanged", () => {
  for (const precision of ["SECOND", "RECURRING", "UNKNOWN", undefined]) {
    assert.equal(
      formatNoticeScheduleRange({
        start_ts_ms: start,
        end_ts_ms: end,
        precision,
        timezone: "KST",
      }),
      `${formatNoticeTime(start)} → ${formatNoticeTime(end)}`
    );
  }
  assert.equal(
    formatNoticeScheduleRange({
      start_ts_ms: start,
      end_ts_ms: end,
      precision: "DAY",
    }),
    "2026. 9. 5."
  );
});
