import assert from "node:assert/strict";
import test from "node:test";
import {
  currentKSTDay,
  indexCellValue,
  indexDaySource,
  indexDisplayAmount,
  normalizeIndexAvailability,
  type IndexDayResponse,
  currentTickerIndexExchanges,
  lastClosedKSTDay,
  sortIndexRows,
  type IndexDayRow,
} from "./index-day.ts";
test("both index views use rolling for today and archive for past dates across KST midnight", () => {
  const before = Date.parse("2026-10-11T14:59:59Z");
  const after = Date.parse("2026-10-11T15:00:00Z");
  assert.equal(indexDaySource("2026-10-11", before), "hourly_rolling");
  assert.equal(indexDaySource("2026-10-11", after), undefined);
  assert.equal(indexDaySource("2026-10-12", after), "hourly_rolling");
  assert.equal(indexDaySource("2026-10-10", before), undefined);
});
test("unavailable turnover stays missing without replacing observed zero", () => {
  assert.equal(indexDisplayAmount(null, true), null);
  assert.equal(indexDisplayAmount(null, false), null);
  assert.equal(indexDisplayAmount(undefined, false), null);
  assert.equal(indexDisplayAmount(123, true), 123);
  assert.equal(indexDisplayAmount(0, false), 0);
});
test("legacy unsupported placeholders remain missing in cards and coins without changing sums", () => {
  const missing = {total: 0, krw: 0, btc: null, usdt: null, share: 0, partial: true, unavailable: true};
  const observed = {...missing, unavailable: false};
  const aggregate = {...missing, total: 100, share: 100};
  const payload = {domestic: {totals: {korbit: missing, upbit: observed, domestic: aggregate}, coins: [{base: "BTC", cells: {korbit: missing}}]}} as unknown as IndexDayResponse;
  const result = normalizeIndexAvailability(payload);
  assert.equal(result.domestic?.totals.korbit.total, null);
  assert.equal(result.domestic?.coins[0].cells.korbit.share, null);
  assert.equal(indexCellValue(result.domestic?.totals.korbit, "total"), null);
  assert.equal(indexCellValue(result.domestic?.coins[0].cells.korbit, "share"), null);
  assert.equal(result.domestic?.totals.upbit.total, 0);
  assert.equal(result.domestic?.totals.domestic.total, 100);
  assert.equal(payload.domestic?.totals.korbit.total, 0);
});
test("current day uses KST midnight rather than host timezone", () => {
  assert.equal(currentKSTDay(Date.parse("2026-09-05T15:00:00Z")), "2026-09-06");
  assert.equal(currentKSTDay(Date.parse("2026-09-05T14:59:59Z")), "2026-09-05");
});
test("rolling failed zero stays visible without becoming an observed complete value", () => {
  const failed = {total: 0, krw: 0, btc: null, usdt: null, share: null, partial: true, unavailable: false};
  const payload = {storage_mode: "ROLLING_24H_LATEST", failed_markets: 1, domestic: {totals: {korbit: failed}, coins: [{base: "BTC", cells: {korbit: failed}}]}} as unknown as IndexDayResponse;
  const result = normalizeIndexAvailability(payload);
  assert.equal(result.domestic?.totals.korbit.total, 0);
  assert.equal(result.domestic?.coins[0].cells.korbit.partial, true);
  assert.equal(result.failed_markets, 1);
});
test("last closed day uses KST midnight rather than host timezone", () => {
  assert.equal(
    lastClosedKSTDay(Date.parse("2026-09-05T15:00:00Z")),
    "2026-09-05"
  );
  assert.equal(
    lastClosedKSTDay(Date.parse("2026-09-05T14:59:59Z")),
    "2026-09-04"
  );
});
test("today cards accept only a complete same-day five-exchange ticker denominator", () => {
  const summary = {
    status: "LIVE",
    source: "OFFICIAL_DOMESTIC_24H_TICKERS",
    as_of: Date.parse("2026-09-06T03:00:00Z") / 1000,
    expected_exchange_count: 5,
    ready_exchange_count: 5,
    total_volume_krw: 1000,
    exchanges: {
      bithumb: {
        status: "LIVE",
        volume_krw: 250,
        share_pct: 25,
        market_count: 200,
        as_of: Date.parse("2026-09-06T03:00:00Z") / 1000,
      },
      upbit: {
        status: "LIVE",
        volume_krw: 250,
        share_pct: 25,
        market_count: 200,
        as_of: Date.parse("2026-09-06T03:00:00Z") / 1000,
      },
      coinone: {
        status: "LIVE",
        volume_krw: 200,
        share_pct: 20,
        market_count: 200,
        as_of: Date.parse("2026-09-06T03:00:00Z") / 1000,
      },
      korbit: {
        status: "LIVE",
        volume_krw: 150,
        share_pct: 15,
        market_count: 200,
        as_of: Date.parse("2026-09-06T03:00:00Z") / 1000,
      },
      gopax: {
        status: "LIVE",
        volume_krw: 150,
        share_pct: 15,
        market_count: 200,
        as_of: Date.parse("2026-09-06T03:00:00Z") / 1000,
      },
    },
  } as never;
  const rows = currentTickerIndexExchanges(summary, "2026-09-06");
  assert.equal(rows[0].share_pct, 25);
  assert.equal(rows[0].volume_krw, 250);
  assert.equal(rows[0].status, "CURRENT_TICKER");
  assert.equal(
    currentTickerIndexExchanges(summary, "2026-09-05")[0].share_pct,
    null
  );
  assert.equal(
    currentTickerIndexExchanges(summary, "2026-09-05")[0].volume_krw,
    null
  );
  summary.ready_exchange_count = 4;
  assert.equal(
    currentTickerIndexExchanges(summary, "2026-09-06")[0].share_pct,
    null
  );
  summary.ready_exchange_count = 5;
  summary.status = "STALE";
  summary.exchanges.bithumb.status = "STALE";
  const delayed = currentTickerIndexExchanges(summary, "2026-09-06")[0];
  assert.equal(delayed.share_pct, 25);
  assert.equal(delayed.status, "STALE_TICKER");
});
test("amount sorting is numeric, reversible, nonmutating and missing-last", () => {
  const rows = [
    { market: "missing", volume_krw: null },
    { market: "small", volume_krw: 2 },
    { market: "big", volume_krw: 100 },
    { market: "zero", volume_krw: 0 },
  ] as IndexDayRow[];
  assert.deepEqual(
    sortIndexRows(rows, "volume_krw", true).map(r => r.market),
    ["big", "small", "zero", "missing"]
  );
  assert.deepEqual(
    sortIndexRows(rows, "volume_krw", false).map(r => r.market),
    ["zero", "small", "big", "missing"]
  );
  assert.equal(rows[0].market, "missing");
});

test("a partial venue without any confirmed amount is unknown, not 0 won or 0%", () => {
  const missing = { total: null, krw: null, btc: null, usdt: null, share: null, partial: true, unavailable: false };
  for (const field of ["total", "krw", "share"] as const) {
    assert.equal(indexCellValue(missing, field), null);
  }
  const observed = { ...missing, total: 5e8, krw: 5e8, share: 12.5 };
  assert.equal(indexCellValue(observed, "share"), 12.5);
  const confirmedZero = { ...missing, total: 0, partial: false };
  assert.equal(indexCellValue(confirmedZero, "total"), 0);
  assert.equal(indexCellValue(undefined, "total"), null);
});

test("a partial period preserves verified zero turnover and a server-supplied zero share", () => {
  // One known zero candle and unobserved hours still have an observed 0 won.
  // Another asset can provide the positive denominator for this coin's 0%.
  const observedZero = {
    total: 0,
    krw: 0,
    btc: null,
    usdt: null,
    share: 0,
    partial: true,
    unavailable: false,
  };
  const before = JSON.stringify(observedZero);
  assert.equal(indexCellValue(observedZero, "total"), 0);
  assert.equal(indexCellValue(observedZero, "krw"), 0);
  assert.equal(indexCellValue(observedZero, "share"), 0);
  assert.equal(indexCellValue(observedZero, "btc"), null);
  assert.equal(JSON.stringify(observedZero), before);
});
