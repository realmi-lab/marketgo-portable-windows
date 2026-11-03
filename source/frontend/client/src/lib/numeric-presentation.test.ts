import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  formatKRW,
  formatKrwCompact,
  formatSignedPercentagePoints,
} from "./formatters.ts";
import { marketShareWindowLabel } from "./market-share-labels.ts";
import { formatObservedWindowCoverage } from "./observed-window-coverage.ts";

const perspective = readFileSync(
  new URL("../components/TargetExchangePerspectivePanel.tsx", import.meta.url),
  "utf8"
);
const layout = readFileSync(
  new URL("../components/DashboardLayout.tsx", import.meta.url),
  "utf8"
);
const rolling = readFileSync(
  new URL("../pages/RollingShareSummary2.tsx", import.meta.url),
  "utf8"
);

test("perspective names its actual current and comparison periods", () => {
  assert.match(
    perspective,
    /marketShareWindowLabel\(analysis\.context\.window_sec\)/
  );
  assert.doesNotMatch(
    perspective,
    /marketShareWindowLabel\(analysis\.window_sec\)/
  );
  assert.match(perspective, /analysis\.context\.comparison_window_sec/);
  assert.match(perspective, /`최근 \$\{windowLabel\} 관측 점유율`/);
  assert.match(perspective, /metricsValue\.officialWindow/);
  assert.match(perspective, /선택 기간 공식 24시간 점유율/);
  assert.match(perspective, /`직전 \$\{comparisonWindowLabel\} 대비`/);
  assert.match(perspective, /analysis\.context\.comparison_enabled/);
  assert.match(perspective, /직전 구간 비교 안 함/);
});

test("perspective separates level money from signed changes and tiny points", () => {
  assert.match(perspective, /formatKRW\(value\)/);
  assert.match(perspective, /money\(metricsValue\.volumeChangeKrw, true\)/);
  assert.match(perspective, /formatSignedPercentagePoints\(value\)/);
  assert.doesNotMatch(perspective, /value\.toFixed\(2\).*%p/);
});

test("summary explicitly stays Bithumb-specific on desktop and mobile", () => {
  assert.match(
    layout,
    /const fixedSummaryPerspective = activeSection === "summary"/
  );
  assert.equal(layout.match(/fixedSummaryPerspective && \(/g)?.length, 2);
  assert.equal(layout.match(/요약은 빗썸 기준/g)?.length, 2);
});

test("rolling coverage retains observed required and missing fields without claiming continuity", () => {
  assert.doesNotMatch(rolling, /현재 연속 관측/);
  assert.match(rolling, /formatObservedWindowCoverage\(/);
  assert.match(rolling, /currentTick\?\.gap_seconds/);
  assert.match(rolling, /rollingCoverage\?\.required_sec/);
});

test("live audit coverage preserves the full missing time instead of calling it continuous", () => {
  assert.equal(
    formatObservedWindowCoverage(85_926, 86_400, 474),
    "관측 23시간 52분 6초 / 필요 24시간 · 누락 7분 54초"
  );
  assert.equal(
    formatObservedWindowCoverage(10, 10, 0),
    "관측 10초 / 필요 10초 · 누락 0초"
  );
  assert.equal(
    formatObservedWindowCoverage(undefined, 86_400, null),
    "관측 — / 필요 24시간 · 누락 —"
  );
  assert.equal(
    formatObservedWindowCoverage(NaN, -1, Infinity),
    "관측 — / 필요 — · 누락 —"
  );
});

test("level signs, tiny changes and window labels retain numeric meaning", () => {
  assert.equal(formatKRW(638_000_000), "₩6.38억");
  assert.equal(formatKrwCompact(638_000_000), "+₩6.38억");
  assert.equal(formatKrwCompact(-638_000_000), "-₩6.38억");
  assert.equal(
    formatSignedPercentagePoints(-0.0012985824791054605),
    "-0.001%p"
  );
  assert.equal(formatSignedPercentagePoints(0.0012985824791054605), "+0.001%p");
  assert.equal(formatSignedPercentagePoints(0), "변화 없음");
  assert.equal(formatSignedPercentagePoints(-1.25), "-1.25%p");
  assert.deepEqual([10, 30, 60, 600, 3600, 86400].map(marketShareWindowLabel), [
    "10초",
    "30초",
    "1분",
    "10분",
    "1시간",
    "24시간",
  ]);
});
