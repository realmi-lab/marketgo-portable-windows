import type { MarketShareOfficialDomestic24h } from "./market-share-types.ts";
import { KORBIT_DISPLAY_NAME } from "./exchanges.ts";

export interface IndexDayRow {
  exchange: string;
  market: string;
  base: string;
  quote: string;
  hours: number;
  amount_hours: number;
  base_volume: number | null;
  quote_volume: number | null;
  volume_krw: number | null;
  observed_volume_krw?: number | null;
  observed_amount_hours?: number;
  status: string;
  sources: string[];
}
export interface IndexDayResponse {
  storage_mode?: "ROLLING_24H_LATEST" | "REALTIME_WINDOW" | "TICKER_24H" | "HOURLY_ARCHIVE";
  window_sec?: number;
  as_of_second?: number;
  window_complete?: boolean;
  coverage_sec?: number;
  failed_markets?: number;
  domestic?: { totals: Record<string, DomesticIndexCell>; coins: Array<{base: string; cells: Record<string, DomesticIndexCell>}> };
  contract_version: "INDEX_DAY_HOURLY_V1" | "REALTIME_INDEX_V1" | "INDEX_TICKER_V1";
  day: string;
  start: number;
  end: number;
  status: string;
  membership: string;
  usdt_close_krw: number | null;
  rows: IndexDayRow[];
  exchanges: Array<{
    exchange: string;
    volume_krw: number | null;
    confirmed_volume_krw?: number | null;
    share_pct: number | null;
    observed_volume_krw?: number | null;
    observed_share_pct?: number | null;
    observed_markets?: number;
    complete_markets: number;
    market_count: number;
    status: string;
  }>;
}
export interface DomesticIndexCell {
  total: number | null; krw: number | null; btc: number | null; usdt: number | null;
  share: number | null; partial: boolean; unavailable: boolean;
}
export const INDEX_EXCHANGES = [
  ["bithumb", "빗썸"],
  ["upbit", "업비트"],
  ["coinone", "코인원"],
  ["korbit", KORBIT_DISPLAY_NAME],
  ["gopax", "고팍스"],
] as const;
export type IndexSort =
  "exchange" | "base" | "quote" | "base_volume" | "volume_krw";
// Missing turnover is not an observed zero.
export function indexDisplayAmount(value: number | null | undefined, unavailable: boolean) {
  return value ?? null;
}

/**
 * The server distinguishes an observed zero from missing amounts with null.
 * A partial period can contain verified zero turnover; retain that value.
 * Legacy unsupported placeholders are removed by normalizeDomesticGrid.
 */
export function indexCellValue(
  cell: DomesticIndexCell | undefined,
  field: "total" | "krw" | "btc" | "usdt" | "share"
): number | null {
  return cell?.[field] ?? null;
}
export const indexCellAmount = (cell: DomesticIndexCell | undefined) => indexCellValue(cell, "total");
export const indexCellShare = (cell: DomesticIndexCell | undefined) => indexCellValue(cell, "share");

// Older servers mark unsupported turnover with both unavailable=true and zero.
// Preserve observed amounts and aggregate denominators; hide only those placeholders.
export function normalizeIndexAvailability(payload: IndexDayResponse): IndexDayResponse {
  return payload.domestic ? { ...payload, domestic: normalizeDomesticGrid(payload.domestic) } : payload;
}

export function normalizeDomesticGrid(grid: NonNullable<IndexDayResponse["domestic"]>): NonNullable<IndexDayResponse["domestic"]> {
  const normalize = (cells: Record<string, DomesticIndexCell>) =>
    Object.fromEntries(Object.entries(cells).map(([key, cell]) => [key,
      (key === "korbit" || key === "gopax") && cell.unavailable && cell.total === 0
        ? { ...cell, total: null, krw: null, btc: null, usdt: null, share: null }
        : cell,
    ]));
  return {
    totals: normalize(grid.totals),
    coins: grid.coins.map(coin => ({ ...coin, cells: normalize(coin.cells) })),
  };
}
// The activated official hourly adapters return no quote turnover for these venues.
export function indexTurnoverUnsupported(exchange: string) {
  return exchange === "korbit" || exchange === "gopax";
}
export function indexDaySource(day: string, now = Date.now()): "hourly_rolling" | undefined {
  return day === currentKSTDay(now) ? "hourly_rolling" : undefined;
}

export function currentKSTDay(now = Date.now()): string {
  return new Date(now + 9 * 3600_000).toISOString().slice(0, 10);
}
export function lastClosedKSTDay(now = Date.now()): string {
  return new Date(now + 9 * 3600_000 - 86400_000).toISOString().slice(0, 10);
}
function finiteNonnegative(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0;
}
function tickerTimestampMS(value: number): number {
  return value < 10_000_000_000 ? value * 1_000 : value;
}
export function currentTickerIndexExchanges(
  summary: MarketShareOfficialDomestic24h | null | undefined,
  day: string
): IndexDayResponse["exchanges"] {
  const currentSource = Boolean(
    summary?.source === "OFFICIAL_DOMESTIC_24H_TICKERS" &&
    finiteNonnegative(summary.as_of) &&
    summary.as_of > 0 &&
    currentKSTDay(tickerTimestampMS(summary.as_of)) === day
  );
  const totalVolumeKRW = summary?.total_volume_krw;
  const tickerRowsReady = INDEX_EXCHANGES.every(([exchange]) => {
    const row = summary?.exchanges?.[exchange];
    return Boolean(
      (row?.status === "LIVE" || row?.status === "STALE") &&
      finiteNonnegative(row.volume_krw) &&
      finiteNonnegative(row.share_pct) &&
      row.share_pct <= 100 &&
      finiteNonnegative(row.as_of) &&
      row.as_of > 0 &&
      currentKSTDay(tickerTimestampMS(row.as_of)) === day
    );
  });
  const denominatorReady = Boolean(
    currentSource &&
    (summary?.status === "LIVE" || summary?.status === "STALE") &&
    summary.expected_exchange_count === INDEX_EXCHANGES.length &&
    summary.ready_exchange_count === summary.expected_exchange_count &&
    tickerRowsReady &&
    finiteNonnegative(totalVolumeKRW) &&
    totalVolumeKRW > 0
  );
  return INDEX_EXCHANGES.map(([exchange]) => {
    const row = summary?.exchanges?.[exchange];
    const volumeReady = Boolean(
      currentSource &&
      (row?.status === "LIVE" || row?.status === "STALE") &&
      finiteNonnegative(row.volume_krw)
    );
    const shareReady = Boolean(
      denominatorReady &&
      volumeReady &&
      finiteNonnegative(row?.share_pct) &&
      row!.share_pct! <= 100
    );
    return {
      exchange,
      volume_krw: volumeReady ? row!.volume_krw : null,
      confirmed_volume_krw: null,
      share_pct: shareReady ? row!.share_pct! : null,
      complete_markets: volumeReady ? row!.market_count : 0,
      market_count: row?.market_count ?? 0,
      status: shareReady
        ? row?.status === "STALE"
          ? "STALE_TICKER"
          : "CURRENT_TICKER"
        : "INCOMPLETE",
    };
  });
}
export function sortIndexRows(
  rows: IndexDayRow[],
  key: IndexSort,
  descending: boolean
): IndexDayRow[] {
  return [...rows].sort((a, b) => {
    const left = a[key],
      right = b[key];
    const missing = (v: unknown) =>
      v == null || (typeof v === "number" && !Number.isFinite(v));
    if (missing(left) || missing(right))
      return Number(missing(left)) - Number(missing(right));
    const diff =
      typeof left === "number" && typeof right === "number"
        ? left - right
        : String(left).localeCompare(String(right));
    return (
      (descending ? -diff : diff) ||
      `${a.exchange}:${a.market}`.localeCompare(`${b.exchange}:${b.market}`)
    );
  });
}
export function indexRowStatus(status: string): string {
  return (
    (
      {
        COMPLETE: "24시간 확인",
        COMPLETE_ESTIMATED: "24시간 확인 · 시가 환산 추정",
        TURNOVER_UNAVAILABLE: "거래금액 미제공",
        MISSING: "시간별 자료 없음",
        INCOMPLETE_HOURS: "시간 자료 부족",
        BASE_VOLUME_ONLY: "수량만 확인",
        FX_MISSING: "같은 시간 환산 시가 없음",
        CONFLICT: "원본 대조 필요",
        INVALID_AMOUNT: "금액 검증 필요",
      } as Record<string, string>
    )[status] ?? "자료 확인 필요"
  );
}
