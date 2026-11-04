import type { IndexDayResponse, IndexDayRow } from "./index-day.ts";
import type {
  ExchangeNotice,
  MarketShareAsset,
} from "./market-share-types.ts";

export function isNoticePortal(hostname: string, pathname: string): boolean {
  return (
    hostname.toLowerCase() === "notice.realmi.net" ||
    /^\/notices(?:\/|$)/.test(pathname)
  );
}

// Select existing market rows; quantities, conversions and totals remain server-owned.
export function noticeAssetMarkets(
  data: IndexDayResponse | null,
  asset: string
) {
  return (data?.rows ?? []).filter(row => row.base === asset);
}

// A collecting row can carry a placeholder zero before any candle arrives.
// Preserve confirmed zeroes, but never present that placeholder as a trade amount.
export function noticeHourlyAmount(row: IndexDayRow): number | null {
  const value = row.volume_krw ?? row.observed_volume_krw;
  if (value == null || !Number.isFinite(value)) return null;
  if (
    value === 0 &&
    row.amount_hours === 0 &&
    row.status !== "COMPLETE" &&
    row.status !== "ROLLING_OK"
  )
    return null;
  return value;
}

export function noticeHourlySummary(
  data: IndexDayResponse | null,
  asset: string
) {
  const rows = noticeAssetMarkets(data, asset);
  // The endpoint's rows include every coin even when its grid is filtered.
  // Read the named coin cell, never an unfiltered market total or another coin's evidence.
  const cell = data?.domestic?.coins.find(coin => coin.base === asset)?.cells
    .domestic;
  const hasAmount = rows.some(row => noticeHourlyAmount(row) != null);
  const collecting = rows.some(row => row.status === "ROLLING_COLLECTING");
  const stale = rows.some(row => row.status === "ROLLING_STALE");
  const partial =
    !rows.length ||
    !cell ||
    cell.partial ||
    cell.unavailable ||
    rows.some(row => row.amount_hours < 24 || noticeHourlyAmount(row) == null);
  return {
    rows,
    amount:
      hasAmount && cell?.total != null && Number.isFinite(cell.total)
        ? cell.total
        : null,
    partial,
    label: stale
      ? "갱신 지연"
      : collecting
        ? "시간봉 수집 중"
        : !hasAmount
          ? "자료 없음"
          : partial
            ? "일부 시간봉"
            : "24시간 확인",
    refreshMS: collecting ? 15_000 : 60_000,
  };
}

export function noticeLiveVolume(
  row:
    | { domestic_trade_count: number; domestic_base_quantity: number | null; domestic_volume_krw: number | null }
    | undefined,
  complete: boolean
) {
  // An asset can exist in the registry without any observed trades in this window.
  const confirmed = Boolean(row && (complete || row.domestic_trade_count > 0));
  return {
    quantity: confirmed ? row!.domestic_base_quantity : null,
    amount: confirmed ? row!.domestic_volume_krw : null,
  };
}

export function noticeQuantity(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "—";
  if (value > 0 && value < 0.0001) return "0.0001 미만";
  return new Intl.NumberFormat("ko-KR", { maximumFractionDigits: 4 }).format(
    value
  );
}

/** The whole official body; the 1,500-character excerpt cuts sentences off. */
export function noticeBodyText(
  notice: Pick<ExchangeNotice, "original_body_text" | "body_excerpt">
): string {
  return notice.original_body_text?.trim() || notice.body_excerpt?.trim() || "";
}

export interface PortalNoticeFilter {
  exchange: string;
  category: string;
  asset: string;
  query: string;
}

// Archive items are replaced, not mutated, when the version changes, so the
// lowercased title + coins + whole body is built once per notice, not per keystroke.
const searchTextByNotice = new WeakMap<ExchangeNotice, string>();

function noticeSearchText(notice: ExchangeNotice): string {
  let text = searchTextByNotice.get(notice);
  if (text === undefined) {
    text = [notice.title, notice.assets.join(" "), noticeBodyText(notice)]
      .join(" ")
      .toLowerCase();
    searchTextByNotice.set(notice, text);
  }
  return text;
}

/** Filters the whole 30-day list, not one page; "all" disables a field. */
export function filterPortalNotices<T extends ExchangeNotice>(
  notices: readonly T[],
  filter: PortalNoticeFilter
): T[] {
  const query = filter.query.trim().toLowerCase();
  return notices.filter(
    notice =>
      (filter.exchange === "all" || notice.exchange === filter.exchange) &&
      (filter.category === "all" || notice.category === filter.category) &&
      (filter.asset === "all" || notice.assets.includes(filter.asset)) &&
      (!query || noticeSearchText(notice).includes(query))
  );
}

/** Option counts for one field, so each choice says how many notices it keeps. */
export function countPortalNotices(
  notices: readonly ExchangeNotice[],
  field: "exchange" | "category"
): Array<[string, number]> {
  const counts = new Map<string, number>();
  for (const notice of notices)
    counts.set(notice[field], (counts.get(notice[field]) ?? 0) + 1);
  return [...counts].sort((left, right) => right[1] - left[1]);
}
