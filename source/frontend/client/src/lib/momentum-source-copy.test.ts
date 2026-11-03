import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  MOMENTUM_SOURCE_HELP,
  momentumSourcePresentation,
} from "./momentum-source-copy.ts";
import { formatObservedWindowCoverage } from "./observed-window-coverage.ts";

test("momentum description follows whole closed minutes and conditional KRW tickers", () => {
  assert.match(MOMENTUM_SOURCE_HELP, /10초·30초는 실제 체결만/);
  assert.match(
    MOMENTUM_SOURCE_HELP,
    /선택 구간 안에 온전히 들어오는 종료된 1분만/
  );
  assert.match(
    MOMENTUM_SOURCE_HELP,
    /이미 관측한 거래금액을 빼고 부족한 금액만/
  );
  assert.match(MOMENTUM_SOURCE_HELP, /초나 10분 단위로 나눠 넣지 않습니다/);
  assert.match(MOMENTUM_SOURCE_HELP, /업비트·빗썸·코인원의 원화 마켓만/);
  assert.match(
    MOMENTUM_SOURCE_HELP,
    /국내 5개 거래소의 공식 자료가 모두 준비되면/
  );
});

test("ticker-only source does not claim an unused minute supplement", () => {
  const source = momentumSourcePresentation([
    {},
    { data_source: { official_24h_ticker_used: true } },
  ]);
  assert.equal(
    source.detail,
    "실시간 체결 + 공식 최근 24시간 자료 · 현재 시각 기준"
  );
  assert.doesNotMatch(source.detail, /1분|부족 금액/);
});

test("minute and ticker flags independently describe actual sources", () => {
  assert.equal(
    momentumSourcePresentation([{}]).detail,
    "실시간 체결 · 현재 시각 기준"
  );
  assert.equal(
    momentumSourcePresentation([
      { data_source: { official_minute_supplement_used: true } },
    ]).detail,
    "실시간 체결 + 종료된 공식 1분 자료의 부족 금액 · 현재 시각 기준"
  );
  assert.equal(
    momentumSourcePresentation([
      {
        data_source: {
          official_minute_supplement_used: true,
          official_24h_ticker_used: true,
        },
      },
    ]).detail,
    "실시간 체결 + 종료된 공식 1분 자료의 부족 금액 + 공식 최근 24시간 자료 · 현재 시각 기준"
  );
});

test("coverage display keeps seconds at the zero minute hour and day boundaries", () => {
  for (const [seconds, label] of [
    [0, "0초"],
    [59, "59초"],
    [60, "1분"],
    [3599, "59분 59초"],
    [3600, "1시간"],
    [86325, "23시간 58분 45초"],
    [86400, "24시간"],
  ] as const) {
    assert.equal(
      formatObservedWindowCoverage(seconds, 86400, 0),
      `관측 ${label} / 필요 24시간 · 누락 0초`
    );
  }
  assert.equal(
    formatObservedWindowCoverage(86325, 86400, 75),
    "관측 23시간 58분 45초 / 필요 24시간 · 누락 1분 15초"
  );
  assert.equal(
    formatObservedWindowCoverage(null, undefined, NaN),
    "관측 — / 필요 — · 누락 —"
  );
});

test("quality view uses scoped precise coverage without replacing the shared duration formatter", () => {
  const source = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  assert.match(source, /description=\{MOMENTUM_SOURCE_HELP\}/);
  assert.match(source, /momentumSourcePresentation\(analysis\.momentum\)/);
  assert.match(
    source,
    /analysis\.context\.window_sec === 86400\s*\? components\.history_24h\.window_complete/
  );
  assert.doesNotMatch(
    source,
    /analysis\.window_sec === 86400\s*\? components\.history_24h\.window_complete/
  );
  assert.match(
    source,
    /formatObservedWindowCoverage\(components\.history_24h\.coverage_sec, components\.history_24h\.required_sec, components\.history_24h\.gap_seconds\)/
  );
  assert.doesNotMatch(
    source,
    /duration\(components\.history_24h\.coverage_sec\)/
  );
  assert.doesNotMatch(source, /이 자료를 10분 단위로 묶고/);
  const panel = readFileSync(
    new URL("../components/common/Panel.tsx", import.meta.url),
    "utf8"
  );
  assert.match(panel, /<MobileHelp/);
  assert.match(panel, /mobile-description-copy/);
  const numericStatus = readFileSync(
    new URL("../components/NumericIntegrityStatus.tsx", import.meta.url),
    "utf8"
  );
  assert.match(numericStatus, /계산 일치와 자료의 완전성은 별개입니다/);
});
