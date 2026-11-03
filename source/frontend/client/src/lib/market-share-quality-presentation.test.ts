import assert from "node:assert/strict";
import test from "node:test";
import {
  formatQualitySignedCount,
  observationGapRecordsEmptyLabel,
  qualityFilterLabel,
  tradeAccountingStatusPresentation,
  tradeAccountingInputPresentation,
} from "./market-share-quality-presentation.ts";
import type { MarketShareAnalysis } from "./market-share-types.ts";

test("journal startup and independent process counts are not presented as durable backlog", () => {
  for (const submitted of [0, 120]) {
    const accounting = {
      status: "BALANCED",
      input_source: "DURABLE_JOURNAL",
      collector_lag_status: "NOT_COMPARABLE_PROCESS_LIFETIMES",
      journal_trades_submitted: submitted,
      collector_lag: null,
      collector_trade_events: 999,
      trades_enqueued: submitted,
      trades_processed: submitted,
      aggregator_accepted_trades: submitted,
      duplicate_trade_events: 0,
      invalid_trade_events: 0,
      trade_processing_errors: 0,
      queue_in_flight: 0,
      processed_conservation_error: 0,
      enqueued_conservation_error: 0,
    } satisfies NonNullable<
      MarketShareAnalysis["data_quality"]["trade_accounting"]
    >;
    const presentation = tradeAccountingInputPresentation(accounting);
    assert.equal(formatQualitySignedCount(accounting.collector_lag), "—");
    assert.equal(presentation.differenceLabel, "두 단계는 시작 시점이 달라 차이를 비교하지 않음");
    assert.equal(presentation.separator, " / ");
    assert.match(presentation.description, /모두 수집됐다는 뜻은 아닙니다/);
    assert.match(presentation.help, /시작 시점이 다릅니다/);
    assert.match(presentation.help, /처리 대기량이 아닙니다/);
    assert.match(presentation.help, /가장 최근 위치와 계산을 마친 위치/);
  }
});

test("queue mode retains signed differences and legacy labels", () => {
  const legacy = tradeAccountingInputPresentation({});
  assert.deepEqual(
    tradeAccountingInputPresentation({
      input_source: "COLLECTOR_QUEUE",
      collector_lag_status: "AVAILABLE",
    }),
    legacy
  );
  assert.equal(legacy.differenceLabel, "받은 수−등록 수");
  assert.equal(legacy.separator, " → ");
  assert.match(legacy.help, /음수일 수 있습니다/);
  assert.equal(
    tradeAccountingInputPresentation({
      collector_lag_status: "NOT_COMPARABLE_PROCESS_LIFETIMES",
    }).differenceLabel,
    "두 단계는 시작 시점이 달라 차이를 비교하지 않음"
  );
});

test("missing quality fields do not assert zero recorded gaps or no filters", () => {
  // Live KRW quality HTTP and WS responses at as_of 1788526220/1788526325
  // omitted these fields despite reporting 28 seconds of observation gaps.
  const quality: {
    observation_gaps?: unknown[];
    coin_filter_applied?: boolean;
    market_filter_applied?: boolean;
    components: { history_24h: { gap_seconds: number } };
  } = { components: { history_24h: { gap_seconds: 28 } } };
  assert.equal(
    observationGapRecordsEmptyLabel(quality.observation_gaps),
    "자료 끊김 기록 목록을 아직 받지 못했습니다."
  );
  assert.equal(
    qualityFilterLabel(
      quality.coin_filter_applied,
      quality.market_filter_applied
    ),
    "확인 자료 없음"
  );
  assert.equal(quality.components.history_24h.gap_seconds, 28);
});

test("explicit empty records and explicit false flags remain valid absence evidence", () => {
  assert.equal(
    observationGapRecordsEmptyLabel([]),
    "받은 기록 목록에 자료 끊김 기록이 없습니다."
  );
  assert.equal(
    observationGapRecordsEmptyLabel(null),
    "자료 끊김 기록 목록을 아직 받지 못했습니다."
  );
  assert.equal(qualityFilterLabel(false, false), "없음");
  for (const missing of [undefined, null]) {
    assert.equal(qualityFilterLabel(false, missing), "확인 자료 없음");
    assert.equal(qualityFilterLabel(missing, false), "확인 자료 없음");
    assert.equal(qualityFilterLabel(true, missing), "있음");
    assert.equal(qualityFilterLabel(missing, true), "있음");
  }
});

test("live negative collector counter differences do not imply lost trades", () => {
  const accounting = {
    status: "COLLECTOR_LAG",
    collector_trade_events: 979024,
    trades_enqueued: 979044,
    collector_lag: -20,
    processed_conservation_error: 0,
    enqueued_conservation_error: 0,
  };
  assert.equal(
    accounting.collector_trade_events - accounting.trades_enqueued,
    accounting.collector_lag
  );
  assert.deepEqual(tradeAccountingStatusPresentation(accounting.status), {
    label: "수집 집계 차이",
    tone: "gold",
  });
  assert.equal(formatQualitySignedCount(accounting.collector_lag), "-20");
  assert.equal(formatQualitySignedCount(20), "+20");
  assert.equal(formatQualitySignedCount(0), "0");
  assert.equal(formatQualitySignedCount(-0), "0");
});

test("unknown validation status and invalid counter differences stay unconfirmed", () => {
  assert.deepEqual(tradeAccountingStatusPresentation("BALANCED"), {
    label: "일치",
    tone: "profit",
  });
  assert.deepEqual(tradeAccountingStatusPresentation("MISMATCH"), {
    label: "불일치",
    tone: "loss",
  });
  for (const status of [undefined, null, "WAITING", ""]) {
    assert.deepEqual(tradeAccountingStatusPresentation(status), {
      label: "검증 상태 확인 필요",
      tone: "gold",
    });
  }
  for (const value of [undefined, null, NaN, Infinity, 0.5, 2 ** 53]) {
    assert.equal(formatQualitySignedCount(value), "—");
  }
});
