import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  aggregationEngineLabel,
  currentQueueLabel,
  formatCollectorElapsed,
  liveCollectionFallbackLabel,
  observationGapReasonLabels,
  observationGapStatusLabel,
  orderbookAgeBreakdown,
  slippageExecutionPresentation,
} from "./market-share-status-presentation.ts";
import { minuteRecoveryPresentation } from "./minute-recovery-presentation.ts";

const venue = {
  status: "LIVE",
  buy_complete: true,
  sell_complete: false,
  buy_fill_pct: 100,
  sell_fill_pct: 66.2,
  buy_slippage_bps: 0,
  sell_slippage_bps: null,
};

test("unobserved and expired order books never claim zero-percent fill or insufficient depth", () => {
  for (const status of [
    "QUIET",
    "STALE",
    "WAITING",
    "CROSSED",
    "FX_UNRESOLVED",
    "UNKNOWN",
  ]) {
    for (const side of ["buy", "sell"] as const) {
      const state = slippageExecutionPresentation(
        {
          ...venue,
          status,
          buy_complete: false,
          buy_fill_pct: 0,
          sell_fill_pct: 0,
        },
        side
      );
      assert.deepEqual(state, {
        kind: "unavailable",
        detail: "호가 확인 대기",
        slippageBps: null,
        fillPct: null,
      });
      // A stale retained success is not executable either.
      assert.equal(
        slippageExecutionPresentation({ ...venue, status }, side).kind,
        "unavailable"
      );
    }
  }
});

test("fresh zero slippage and genuinely insufficient depth remain distinct", () => {
  assert.equal(slippageExecutionPresentation(venue, "buy").kind, "complete");
  assert.equal(slippageExecutionPresentation(venue, "buy").slippageBps, 0);
  assert.deepEqual(slippageExecutionPresentation(venue, "sell"), {
    kind: "incomplete",
    detail: "전량 불가",
    slippageBps: null,
    fillPct: 66.2,
  });
  assert.equal(
    slippageExecutionPresentation({ ...venue, sell_fill_pct: 0 }, "sell").kind,
    "incomplete"
  );
  assert.equal(
    slippageExecutionPresentation({ ...venue, buy_slippage_bps: null }, "buy")
      .kind,
    "unavailable"
  );
  for (const fill of [NaN, Infinity, -1, 101]) {
    assert.equal(
      slippageExecutionPresentation({ ...venue, sell_fill_pct: fill }, "sell")
        .kind,
      "unavailable"
    );
  }
});

test("quality distinguishes raw aged books from quiet diagnoses without inventing counts", () => {
  assert.equal(
    orderbookAgeBreakdown(37, { LOW_ACTIVITY_CANDIDATE: 37 }),
    "오래된 호가 37 · 그중 거래 적음 37"
  );
  assert.equal(
    orderbookAgeBreakdown(37, { LOW_ACTIVITY_CANDIDATE: 30 }),
    "오래된 호가 37 · 그중 거래 적음 30"
  );
  assert.equal(
    orderbookAgeBreakdown(0, {}),
    "오래된 호가 0 · 그중 거래 적음 0"
  );
  assert.equal(
    orderbookAgeBreakdown(37, undefined),
    "오래된 호가 37 · 그중 거래 적음 —"
  );
});

test("current waiting count is not cumulative queue registration", () => {
  const accounting = {
    trades_enqueued: 351457,
    trades_processed: 351457,
    queue_in_flight: 0,
  };
  assert.equal(
    currentQueueLabel(accounting.queue_in_flight),
    "현재 처리 대기 0건"
  );
  assert.equal(currentQueueLabel(7), "현재 처리 대기 7건");
  assert.equal(currentQueueLabel(undefined), "현재 처리 대기 —건");
});

test("collector elapsed time retains seconds and missing state across minute boundaries", () => {
  assert.equal(formatCollectorElapsed(1160.3), "19분 20.3초");
  assert.equal(formatCollectorElapsed(3661.2), "1시간 1분 1.2초");
  assert.equal(formatCollectorElapsed(59.96), "1분");
  assert.equal(formatCollectorElapsed(3599.96), "1시간");
  assert.equal(formatCollectorElapsed(0), "0초");
  assert.equal(formatCollectorElapsed(0.02), "0.1초 미만");
  for (const value of [null, undefined, NaN, Infinity, -1]) {
    assert.equal(formatCollectorElapsed(value), "—");
  }
});

test("current Go and Rust engines are recognized without asserting unknown engines healthy", () => {
  assert.equal(aggregationEngineLabel("rust-ffi"), "고속 계산");
  assert.equal(aggregationEngineLabel("go-oracle"), "기본 계산");
  assert.equal(aggregationEngineLabel("rust-pyo3"), "고속 계산");
  assert.equal(aggregationEngineLabel("unknown"), "집계 경로 확인 필요");
});

test("closed connection gaps are not labeled missing-data recovery", () => {
  assert.equal(observationGapStatusLabel("RECOVERED"), "연결 재개");
  assert.equal(observationGapStatusLabel("OPEN"), "진행 중");
  assert.equal(
    observationGapStatusLabel("CLOSED_ON_RESTART"),
    "재시작으로 종료"
  );
  assert.equal(observationGapStatusLabel("unknown"), "상태 확인 필요");
});

test("gap reasons read as plain Korean instead of server codes", () => {
  assert.deepEqual(
    observationGapReasonLabels([
      "COLLECTOR_OBSERVATION_INCOMPLETE",
      "WEBSOCKET_DISCONNECTED",
    ]),
    ["수집 서버 자료 불완전", "실시간 연결 끊김"]
  );
  assert.deepEqual(
    observationGapReasonLabels([
      "TRADE_EVENT_PARSE_ERROR",
      "MISSING_TRADE_EVENTS:1200",
      "MISSING_TRADE_ROWS:1300",
      "TRADE_PARSE_ERROR",
    ]),
    ["체결 자료를 읽지 못함", "빠진 체결 1,200건", "빠진 체결 기록 1,300건"]
  );
  assert.deepEqual(
    observationGapReasonLabels(["MISSING_TRADE_EVENTS:x", "NEW_REASON"]),
    ["빠진 체결", "기타 사유"]
  );
  assert.deepEqual(observationGapReasonLabels([]), []);
});

test("live collection fallback and selected-minute supplementation can differ", () => {
  const recovery = minuteRecoveryPresentation(
    {
      official_minute_supplement_status: "READY",
      official_minute_supplement_used: true,
      official_minute_supplement_volume_krw: 500,
      official_minute_supplement_row_count: 1,
      official_candle_supplement_interval_sec: 60,
    },
    3600
  );
  assert.equal(recovery.label, "1분봉으로 보완");
  assert.equal(
    liveCollectionFallbackLabel(false),
    "사용 안 함 · 분봉 보완과 별도"
  );
});

test("actual view wires state helpers and fixes summary perspective without changing saved selection", () => {
  const view = readFileSync(
    new URL("../pages/MarketShare.tsx", import.meta.url),
    "utf8"
  );
  const panel = readFileSync(
    new URL(
      "../components/TargetExchangePerspectivePanel.tsx",
      import.meta.url
    ),
    "utf8"
  );
  assert.match(view, /observationGapStatusLabel\(gap\.status\)/);
  assert.match(view, /currentQueueLabel\(accounting\.queue_in_flight\)/);
  assert.match(view, /tradeAccountingInputPresentation\(accounting \?\? \{\}\)/);
  assert.match(view, /accountingInput\.countersLabel/);
  assert.match(view, /실시간 체결 대체 수집/);
  assert.doesNotMatch(view, /"공식 보완 자료"/);
  assert.match(
    view,
    /targetExchangeOverride=\{\s*surface === "summary" \|\| view === "drivers" \? "bithumb" : undefined\s*\}/
  );
  assert.match(panel, /targetExchangeOverride \?\? "bithumb"/);
  assert.doesNotMatch(panel, /useTargetExchange/);
  assert.doesNotMatch(view, /useTargetExchange/);
  assert.match(view, /\[selectedExchange, setTargetExchange\] = useState\("bithumb"\)/);
  assert.doesNotMatch(panel, /setTargetExchange|localStorage/);
});
