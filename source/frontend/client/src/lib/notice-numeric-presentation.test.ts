import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { formatNoticeSourceTime } from "./notice-time.ts";
import { formatOfficialHourTimestamp } from "./official-hour-metric-format.ts";
import { formatNoticeVolumeMultiplier } from "./notice-reaction-format.ts";
import { formatSharePercent } from "./formatters.ts";

const source = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

test("source seconds and official event milliseconds retain the same Korean time on foreign devices", () => {
  // Production source scan value from /api/market-share/analysis, Unix seconds.
  const sourceSeconds = 1788525867.2249637;
  const previous = process.env.TZ;
  try {
    for (const zone of ["Asia/Seoul", "UTC", "America/Los_Angeles"]) {
      process.env.TZ = zone;
      assert.match(
        formatNoticeSourceTime(sourceSeconds),
        /^2026\. 9\. 4\. 21(?:시 |:)44(?:분 |:)27초?$/
      );
      assert.equal(
        formatOfficialHourTimestamp(sourceSeconds * 1_000),
        "09. 04. 21:44:27"
      );
      const midnightMS = Date.parse("2026-09-03T15:00:00Z");
      assert.match(
        formatNoticeSourceTime(midnightMS / 1_000),
        /^2026\. 9\. 4\. 0{1,2}(?:시 |:)0{1,2}(?:분 |:)0{1,2}초?$/
      );
    }
  } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  }
  for (const value of [
    null,
    undefined,
    0,
    -1,
    NaN,
    Infinity,
    Number.MAX_VALUE,
  ]) {
    assert.equal(formatNoticeSourceTime(value), "기록 없음");
    assert.equal(formatOfficialHourTimestamp(value), "시각 정보 없음");
  }
  const notices = source("../pages/MarketNotices.tsx");
  assert.match(notices, /formatNoticeSourceTime as noticeSourceTime/);
  assert.doesNotMatch(notices, /6시간 뒤 다시 확인/);
  const movements = source("../components/OfficialMovementPanel.tsx");
  assert.match(movements, /formatOfficialHourTimestamp as timestamp/);
  assert.doesNotMatch(movements, /new Date\(|value < 10_000_000_000/);
});

test("notice reaction ratios keep true zero, a small positive observation, and absent baselines distinct", () => {
  // Observed production multipliers remain ratios, not percentages.
  assert.equal(formatNoticeVolumeMultiplier(2.8821252211727773), "2.88배");
  assert.equal(formatNoticeVolumeMultiplier(0.683196094502778), "0.68배");
  assert.equal(formatNoticeVolumeMultiplier(0), "0.00배");
  assert.equal(formatNoticeVolumeMultiplier(-0), "0.00배");
  assert.equal(formatNoticeVolumeMultiplier(0.0001), "0.01배 미만");
  for (const value of [null, undefined, NaN, Infinity, -1]) {
    assert.equal(formatNoticeVolumeMultiplier(value), "기준 없음");
  }
  assert.match(
    source("../pages/MarketNotices.tsx"),
    /formatNoticeVolumeMultiplier\(study\.exchange_volume_multiplier\)/
  );
});

test("bounded notice shares and confidence do not claim exact zero or certainty after rounding", () => {
  assert.equal(formatSharePercent(0.001, 1), "0.1% 미만");
  assert.equal(formatSharePercent(99.9, 0), "100% 미만");
  assert.equal(formatSharePercent(100, 0), "100%");
  assert.equal(formatSharePercent(null, 0), "—");
  const notices = source("../pages/MarketNotices.tsx");
  assert.match(notices, /return formatSharePercent\(value, digits\)/);
  assert.doesNotMatch(notices, /percent\(study\.evidence_confidence_pct, 0\)/);
});
