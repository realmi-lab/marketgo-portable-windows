import {
  DOMESTIC_EXCHANGES,
  type DomesticExchange,
} from "./market-share-config.ts";
import type { MarketShareQualityAnalysis } from "./market-share-types.ts";
import { OBSERVED_RANGE_BASIS_LABEL } from "./selected-window-presentation.ts";

export const DEFAULT_TARGET_EXCHANGE: DomesticExchange = "bithumb";
export const TARGET_EXCHANGE_STORAGE_KEY = "market-share.target-exchange.v1";

const DOMESTIC_EXCHANGE_SET = new Set<string>(DOMESTIC_EXCHANGES);

export function isDomesticExchange(value: unknown): value is DomesticExchange {
  return typeof value === "string" && DOMESTIC_EXCHANGE_SET.has(value);
}

export function normalizeTargetExchange(
  value: unknown,
  fallback: DomesticExchange = DEFAULT_TARGET_EXCHANGE
): DomesticExchange {
  return isDomesticExchange(value) ? value : fallback;
}

export function selectTargetExchangePerspective(
  analysis: MarketShareQualityAnalysis,
  exchange: DomesticExchange
) {
  const realtimeRows = Array.isArray(analysis.exchanges)
    ? analysis.exchanges
    : [];
  const changeRows = Array.isArray(analysis.change_attribution?.exchanges)
    ? analysis.change_attribution.exchanges
    : [];
  const liquidityRows = Array.isArray(analysis.liquidity?.exchanges)
    ? analysis.liquidity.exchanges
    : [];
  const officialRows = analysis.official_domestic_24h?.exchanges ?? {};
  return {
    exchange,
    realtime: realtimeRows.find(row => row.exchange === exchange) ?? null,
    change: changeRows.find(row => row.exchange === exchange) ?? null,
    liquidity: liquidityRows.find(row => row.exchange === exchange) ?? null,
    official: officialRows[exchange] ?? null,
  };
}

export function targetOfficialDomesticSharePct(
  analysis: MarketShareQualityAnalysis,
  exchange: DomesticExchange
): number | null {
  const summary = analysis.official_domestic_24h;
  const row = summary?.exchanges?.[exchange];
  const total = summary?.total_volume_krw;
  const share = row?.share_pct;
  if (
    !summary ||
    summary.source !== "OFFICIAL_DOMESTIC_24H_TICKERS" ||
    summary.status !== "LIVE" ||
    summary.ready_exchange_count !== summary.expected_exchange_count ||
    row?.status !== "LIVE" ||
    typeof total !== "number" ||
    !Number.isFinite(total) ||
    total <= 0 ||
    typeof share !== "number" ||
    !Number.isFinite(share) ||
    share < 0 ||
    share > 100
  ) {
    return null;
  }
  return share;
}

function finite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function nonnegative(value: unknown): number | null {
  return finite(value) && value >= 0 ? value : null;
}

function share(value: unknown): number | null {
  return finite(value) && value >= 0 && value <= 100 ? value : null;
}

/** Select server-owned values with the evidence boundary of each card. */
export function selectTargetExchangeMetrics(
  analysis: MarketShareQualityAnalysis,
  exchange: DomesticExchange
) {
  const selected = selectTargetExchangePerspective(analysis, exchange);
  const context = analysis.context;
  const officialWindow = context.data_source?.official_24h_ticker_used === true;
  const currentStatus = context.selected_window_status;
  const supplemented =
    currentStatus === "PARTIAL" &&
    context.data_source?.official_minute_supplement_used === true;
  const currentUsable =
    ["CONFIRMED", "PROVISIONAL"].includes(currentStatus) || supplemented;
  // A window observed only in part still has real per-exchange rows. They are
  // shown, and named by their basis so they are not read as the window's own.
  const observedRange = currentStatus === "PARTIAL" && !supplemented;
  const collector = analysis.collectors?.[exchange];
  const streamReady =
    context.market_scope === "KRW"
      ? (collector?.krw_trade_stream_ready ?? collector?.trade_stream_ready)
      : collector?.trade_stream_ready;
  // The intelligence projection omits collectors but retains the finalized
  // context and exchange rows. Absence is not an explicit readiness failure.
  const exchangeReady = officialWindow || streamReady !== false;
  const comparison = analysis.change_attribution;
  const comparisonUsable =
    context.comparison_enabled &&
    ["CONFIRMED", "PROVISIONAL"].includes(comparison.status) &&
    !(comparison.excluded_exchanges ?? []).includes(exchange);
  const liquidityUsable =
    finite(selected.liquidity?.live_asset_count) &&
    selected.liquidity.live_asset_count > 0;
  const officialSource =
    analysis.official_domestic_24h?.source === "OFFICIAL_DOMESTIC_24H_TICKERS";
  const officialVolume = officialSource
    ? nonnegative(selected.official?.volume_krw)
    : null;
  const windowShown = (currentUsable || observedRange) && exchangeReady;
  const windowSharePct = windowShown ? share(selected.realtime?.share_pct) : null;
  return {
    officialWindow,
    windowSharePct,
    windowVolumeKrw: windowShown
      ? nonnegative(selected.realtime?.volume_krw)
      : null,
    windowShareBasis:
      observedRange && windowSharePct !== null ? OBSERVED_RANGE_BASIS_LABEL : "",
    changePP:
      comparisonUsable && finite(selected.change?.share_change_pp)
        ? selected.change.share_change_pp
        : null,
    volumeChangeKrw:
      comparisonUsable && finite(selected.change?.volume_change_krw)
        ? selected.change.volume_change_krw
        : null,
    liquiditySharePct: liquidityUsable
      ? share(selected.liquidity?.liquidity_share_pct)
      : null,
    spreadBps: liquidityUsable
      ? nonnegative(selected.liquidity?.median_spread_bps)
      : null,
    officialSharePct: targetOfficialDomesticSharePct(analysis, exchange),
    officialVolumeKrw: officialVolume,
    officialVolumeRetained:
      officialVolume !== null && selected.official?.status !== "LIVE",
  };
}
