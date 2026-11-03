import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { formatReferenceKrwPrice } from "./binance-reference.ts";
import {
  intelligenceCaptureDescription,
  intelligenceContributionCopy,
} from "./market-intelligence-presentation.ts";
import { formatNoticeTime, formatNoticeScheduleRange } from "./notice-time.ts";

const source = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

test("reference prices preserve the actual sub-won price instead of amount rounding", () => {
  assert.equal(formatReferenceKrwPrice(0.0049499999999999995), "₩0.00495");
  assert.equal(formatReferenceKrwPrice(0.000000001), "₩0.000000001");
  assert.equal(formatReferenceKrwPrice(110_657_000), "₩110,657,000");
  assert.equal(formatReferenceKrwPrice(0), "₩0");
  for (const value of [null, undefined, NaN, Infinity]) {
    assert.equal(formatReferenceKrwPrice(value), "—");
  }
  const panel = source("../components/BinanceReferencePanel.tsx");
  assert.match(
    panel,
    /formatReferenceKrwPrice\(asset\.premium_bithumb_mid_krw\)/
  );
  assert.doesNotMatch(panel, /krw\(asset\.premium_bithumb_mid_krw\)/);
});

test("contribution and capture text follow the supplied window and evidence layer", () => {
  assert.equal(
    intelligenceContributionCopy(60, "REALTIME").title,
    "최근 1분 코인별 기여"
  );
  assert.equal(
    intelligenceContributionCopy(3600, "REALTIME").title,
    "최근 1시간 코인별 기여"
  );
  assert.match(
    intelligenceContributionCopy(60, "OFFICIAL").title,
    /공식 시간봉/
  );
  assert.match(
    intelligenceContributionCopy(60, "UNAVAILABLE").source,
    /확인 중/
  );
  assert.doesNotMatch(intelligenceCaptureDescription(60), /1시간/);
  assert.match(intelligenceCaptureDescription(60), /최근 1분.*같은 1분/);
  const page = source("../pages/MarketIntelligence.tsx");
  assert.match(page, /title=\{contribution\.title\}/);
  assert.match(
    page,
    /intelligenceCaptureDescription\(\s*analysis\.context\.window_sec\s*\)/
  );
});

test("Korean notice dates survive a device in another timezone", () => {
  const previous = process.env.TZ;
  try {
    process.env.TZ = "America/Los_Angeles";
    const midnight = Date.parse("2026-09-04T00:00:00+09:00");
    assert.equal(formatNoticeTime(midnight, true), "2026. 9. 4.");
    assert.match(formatNoticeTime(midnight), /^2026\. 9\. 4\./);
    assert.equal(
      formatNoticeScheduleRange({
        start_ts_ms: midnight,
        end_ts_ms: midnight,
        precision: "DAY",
        timezone: "Asia/Seoul",
      }),
      "2026. 9. 4."
    );
    assert.equal(
      formatNoticeScheduleRange({
        start_ts_ms: midnight,
        end_ts_ms: 0,
        precision: "DAY",
        timezone: "UTC",
      }),
      "2026. 9. 3. (UTC)"
    );
    assert.equal(formatNoticeTime(NaN), "미정");
    assert.equal(formatNoticeTime(0), "미정");
    assert.equal(formatNoticeTime(Number.MAX_VALUE), "미정");
  } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  }
  assert.match(
    source("../pages/MarketNotices.tsx"),
    /formatNoticeScheduleRange as noticeScheduleRange/
  );
  assert.match(
    source("./market-share-reaction.ts"),
    /formatNoticeTime\(notice\.published_ts_ms, true\)/
  );
});

test("mobile momentum keeps period and amount units together without hiding status", () => {
  const css = source("../mobile.css");
  assert.match(
    css,
    /\.main-dashboard \.summary-momentum-heading > strong\s*\{[^}]*white-space: nowrap/s
  );
  assert.match(
    css,
    /\.main-dashboard \.summary-momentum-heading > \.summary-status\s*\{[^}]*grid-row: 2/s
  );
  assert.match(
    source("../pages/MarketShare.tsx"),
    /className=\{`summary-status /
  );
  assert.match(
    css,
    /\.summary-momentum-heading > strong:last-child\s*\{[^}]*grid-column: 2;[^}]*grid-row: 1/s
  );
  assert.match(
    css,
    /\.main-dashboard \.summary-momentum-speed\s*\{[^}]*flex-direction: column/s
  );
});
