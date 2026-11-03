import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { DOMESTIC_EXCHANGES } from "./market-share-config.ts";

import {
  DEFAULT_TARGET_EXCHANGE,
  isDomesticExchange,
  normalizeTargetExchange,
  selectTargetExchangePerspective,
  selectTargetExchangeMetrics,
  targetOfficialDomesticSharePct,
} from "./target-exchange.ts";

test("target exchange normalization accepts only supported domestic venues", () => {
  assert.equal(isDomesticExchange("upbit"), true);
  assert.equal(isDomesticExchange("binance_spot"), false);
  assert.equal(normalizeTargetExchange("coinone"), "coinone");
  assert.equal(normalizeTargetExchange("unknown"), DEFAULT_TARGET_EXCHANGE);
  assert.equal(normalizeTargetExchange(null, "korbit"), "korbit");
});

function metricsFixture() {
  return {
    context: {
      market_scope: "KRW",
      window_sec: 60,
      comparison_enabled: true,
      comparison_window_sec: 60,
      selected_window_status: "CONFIRMED",
      data_source: {},
    },
    collectors: Object.fromEntries(
      DOMESTIC_EXCHANGES.map(exchange => [
        exchange,
        {
          trade_stream_ready: true,
          krw_trade_stream_ready: true,
        },
      ])
    ),
    exchanges: DOMESTIC_EXCHANGES.map((exchange, i) => ({
      exchange,
      share_pct: i * 5,
      volume_krw: i * 100,
    })),
    change_attribution: {
      status: "CONFIRMED",
      excluded_exchanges: [] as string[],
      exchanges: DOMESTIC_EXCHANGES.map((exchange, i) => ({
        exchange,
        share_change_pp: i - 2,
        contribution_pp: 900 + i,
        volume_change_krw: i * -10,
      })),
    },
    liquidity: {
      exchanges: DOMESTIC_EXCHANGES.map((exchange, i) => ({
        exchange,
        live_asset_count: 1,
        liquidity_share_pct: i * 4,
        median_spread_bps: i,
      })),
    },
    official_domestic_24h: {
      source: "OFFICIAL_DOMESTIC_24H_TICKERS",
      status: "LIVE",
      expected_exchange_count: 5,
      ready_exchange_count: 5,
      total_volume_krw: 1_000,
      exchanges: Object.fromEntries(
        DOMESTIC_EXCHANGES.map((exchange, i) => [
          exchange,
          { exchange, status: "LIVE", share_pct: i * 7, volume_krw: i * 70 },
        ])
      ),
    },
  };
}

test("all five selections keep their own shares and changes rather than Bithumb contribution", () => {
  const analysis = metricsFixture();
  DOMESTIC_EXCHANGES.forEach((exchange, i) => {
    const selected = selectTargetExchangeMetrics(analysis as never, exchange);
    assert.equal(selected.windowSharePct, i * 5);
    assert.equal(selected.windowVolumeKrw, i * 100);
    assert.equal(selected.changePP, i - 2);
    assert.notEqual(selected.changePP, 900 + i);
    assert.equal(selected.volumeChangeKrw, i * -10);
    assert.equal(selected.liquiditySharePct, i * 4);
    assert.equal(selected.officialSharePct, i * 7);
    assert.equal(selected.officialVolumeKrw, i * 70);
  });
});

test("disabled comparisons, excluded venues and missing books cannot display stale zeroes", () => {
  const analysis = metricsFixture();
  analysis.context.comparison_enabled = false;
  assert.equal(
    selectTargetExchangeMetrics(analysis as never, "bithumb").changePP,
    null
  );
  analysis.context.comparison_enabled = true;
  analysis.change_attribution.excluded_exchanges = ["korbit"];
  const korbit = analysis.liquidity.exchanges.find(
    row => row.exchange === "korbit"
  )!;
  korbit.live_asset_count = 0;
  korbit.liquidity_share_pct = 0;
  const value = selectTargetExchangeMetrics(analysis as never, "korbit");
  assert.equal(value.changePP, null);
  assert.equal(value.volumeChangeKrw, null);
  assert.equal(value.liquiditySharePct, null);
  assert.equal(value.spreadBps, null);
});

test("compact intelligence rows keep confirmed values when optional collector fields are omitted", () => {
  const full = metricsFixture();
  const { collectors: _collectors, ...compact } = full;
  for (const exchange of DOMESTIC_EXCHANGES) {
    const expected = selectTargetExchangeMetrics(full as never, exchange);
    const actual = selectTargetExchangeMetrics(compact as never, exchange);
    assert.equal(actual.windowSharePct, expected.windowSharePct);
    assert.equal(actual.windowVolumeKrw, expected.windowVolumeKrw);
    assert.equal(actual.changePP, expected.changePP);
  }
  const missingFlags = { ...full, collectors: { upbit: {} } };
  assert.equal(
    selectTargetExchangeMetrics(missingFlags as never, "upbit").windowSharePct,
    selectTargetExchangeMetrics(full as never, "upbit").windowSharePct
  );
  const explicitlyUnavailable = {
    ...full,
    collectors: { upbit: { krw_trade_stream_ready: false } },
  };
  assert.equal(
    selectTargetExchangeMetrics(explicitlyUnavailable as never, "upbit")
      .windowSharePct,
    null
  );
  compact.context.selected_window_status = "WARMING";
  assert.equal(
    selectTargetExchangeMetrics(compact as never, "upbit").windowSharePct,
    null
  );
});

test("official current values stay separate from trade readiness and stale official money is labeled", () => {
  const analysis = metricsFixture();
  analysis.collectors.upbit.krw_trade_stream_ready = false;
  assert.equal(
    selectTargetExchangeMetrics(analysis as never, "upbit").windowSharePct,
    null
  );
  analysis.context.data_source = { official_24h_ticker_used: true };
  const official = selectTargetExchangeMetrics(analysis as never, "upbit");
  assert.equal(official.officialWindow, true);
  assert.notEqual(official.windowSharePct, null);
  analysis.official_domestic_24h.exchanges.upbit.status = "STALE";
  const stale = selectTargetExchangeMetrics(analysis as never, "upbit");
  assert.equal(stale.officialSharePct, null);
  assert.equal(stale.officialVolumeRetained, true);
  assert.notEqual(stale.officialVolumeKrw, null);
});

test("market analysis names the fixed target and different hourly and realtime denominators", () => {
  const view = readFileSync(
    new URL("../pages/MarketIntelligence.tsx", import.meta.url),
    "utf8"
  );
  assert.match(view, /국내 5개 거래소 공식 24시간 점유율/);
  assert.match(view, /DOMESTIC_REALTIME_EXCHANGES\.map\(exchange/);
  for (const entry of [
    'upbit: "업비트"',
    'bithumb: "빗썸"',
    'coinone: "코인원"',
    'korbit: "코빗"',
    'gopax: "고팍스"',
  ]) {
    assert.ok(view.includes(entry));
  }
  assert.match(view, /정시 1시간 변화 \(업비트·빗썸·코인원\)/);
  assert.match(view, /업비트·빗썸·코인원 합계/);
  assert.match(view, /코빗·고팍스의 봉은 코인 거래수량만 제공/);
  assert.match(view, /국내 5개 거래소의 최근 1분 체결 흐름/);
  assert.match(view, /코인별 빗썸 점유율 기여/);
  assert.doesNotMatch(view, /최근 1시간 실시간 체결 흐름/);
});

test("official target share only uses a complete rolling 24-hour ticker snapshot", () => {
  const analysis = {
    official_domestic_24h: {
      source: "OFFICIAL_DOMESTIC_24H_TICKERS",
      status: "LIVE",
      expected_exchange_count: 5,
      ready_exchange_count: 5,
      total_volume_krw: 1_000,
      exchanges: {
        bithumb: { status: "LIVE", volume_krw: 378.1, share_pct: 37.8 },
      },
    },
  } as never;
  assert.equal(targetOfficialDomesticSharePct(analysis, "bithumb"), 37.8);

  for (const override of [
    { status: "PARTIAL" },
    { source: "OFFICIAL_DOMESTIC_ALIGNED_1H_CANDLES_24H" },
    { ready_exchange_count: 4 },
    { total_volume_krw: 0 },
  ]) {
    const incomplete = {
      ...analysis,
      official_domestic_24h: {
        ...(analysis as never as { official_domestic_24h: object })
          .official_domestic_24h,
        ...override,
      },
    } as never;
    assert.equal(targetOfficialDomesticSharePct(incomplete, "bithumb"), null);
  }
  assert.equal(targetOfficialDomesticSharePct(analysis, "korbit"), null);
});

test("target perspective selects existing rows without cloning analysis", () => {
  const realtime = { exchange: "upbit" };
  const change = { exchange: "upbit" };
  const liquidity = { exchange: "upbit" };
  const official = { exchange: "upbit" };
  const analysis = {
    exchanges: [realtime],
    change_attribution: { exchanges: [change] },
    liquidity: { exchanges: [liquidity] },
    official_domestic_24h: { exchanges: { upbit: official } },
  };

  const selected = selectTargetExchangePerspective(analysis as never, "upbit");
  assert.equal(selected.realtime, realtime);
  assert.equal(selected.change, change);
  assert.equal(selected.liquidity, liquidity);
  assert.equal(selected.official, official);
});

test("target perspective tolerates a compact payload without optional rows", () => {
  const selected = selectTargetExchangePerspective(
    {
      change_attribution: {},
      liquidity: {},
      official_domestic_24h: {},
    } as never,
    "bithumb"
  );

  assert.equal(selected.realtime, null);
  assert.equal(selected.change, null);
  assert.equal(selected.liquidity, null);
  assert.equal(selected.official, null);
});

// A window the stream observed only in part still has real per-exchange rows.
// Its share is shown, named by its basis, and never mistaken for the window's.
test("a partly observed window shows the per-exchange share with its basis", () => {
  const partial = metricsFixture();
  partial.context.selected_window_status = "PARTIAL";
  const value = selectTargetExchangeMetrics(partial as never, "upbit");
  assert.equal(value.windowSharePct, 5);
  assert.equal(value.windowVolumeKrw, 100);
  assert.equal(value.windowShareBasis, "관측범위기준(웹소켓)");
});

test("a complete, supplemented or official window carries no basis label", () => {
  const complete = metricsFixture();
  assert.equal(selectTargetExchangeMetrics(complete as never, "upbit").windowShareBasis, "");

  const supplemented = metricsFixture();
  supplemented.context.selected_window_status = "PARTIAL";
  supplemented.context.data_source = { official_minute_supplement_used: true } as never;
  const shown = selectTargetExchangeMetrics(supplemented as never, "upbit");
  assert.equal(shown.windowSharePct, 5);
  assert.equal(shown.windowShareBasis, "");

  const official = metricsFixture();
  official.context.data_source = { official_24h_ticker_used: true } as never;
  assert.equal(selectTargetExchangeMetrics(official as never, "upbit").windowShareBasis, "");
});

test("an observed-range share still respects a withheld share and a dead trade stream", () => {
  const withheld = metricsFixture();
  withheld.context.selected_window_status = "PARTIAL";
  (withheld.exchanges[1] as { share_pct: number | null }).share_pct = null;
  const none = selectTargetExchangeMetrics(withheld as never, "upbit");
  assert.equal(none.windowSharePct, null);
  assert.equal(none.windowShareBasis, "");

  const dead = metricsFixture();
  dead.context.selected_window_status = "PARTIAL";
  dead.collectors.upbit.krw_trade_stream_ready = false;
  const off = selectTargetExchangeMetrics(dead as never, "upbit");
  assert.equal(off.windowSharePct, null);
  assert.equal(off.windowVolumeKrw, null);
  assert.equal(off.windowShareBasis, "");
});

test("an unknown window status still shows nothing", () => {
  const unknown = metricsFixture();
  unknown.context.selected_window_status = "WARMING";
  const value = selectTargetExchangeMetrics(unknown as never, "upbit");
  assert.equal(value.windowSharePct, null);
  assert.equal(value.windowShareBasis, "");
});
