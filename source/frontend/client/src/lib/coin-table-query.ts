import type {
  MarketShareAsset,
  MarketShareChangeAsset,
} from "./market-share-types";

/** A coin row as the table reads it; the drivers screen's frames carry no order-book detail. */
export type CoinTableAsset = Omit<
  MarketShareAsset,
  "microstructure" | "asset_identity"
>;

export type CoinTableView = "impact" | "capture";
export type CoinContributionDirection =
  "all" | "positive" | "negative" | "flat";
export type CoinListingFilter =
  | "all"
  | "bithumb-only"
  | "bithumb-upbit"
  | "bithumb-other"
  | "bithumb-unlisted";
export type CoinNoticeFilter = "all" | "present" | "absent";
export type CoinTradeFilter = "traded" | "all";
export type CoinTableSortDirection = "asc" | "desc";
export type CoinTableSortKey =
  | "asset"
  | "contribution"
  | "capture"
  | "bithumb-change"
  | "domestic-change"
  | "competitor"
  | "listing"
  | "notice"
  | "domestic-volume"
  | "capture-gap"
  | "bithumb-listed";

export interface CoinTableFilters {
  search: string;
  direction: CoinContributionDirection;
  listing: CoinListingFilter;
  notice: CoinNoticeFilter;
  trade: CoinTradeFilter;
}

export interface CoinTableSort {
  key: CoinTableSortKey | null;
  direction: CoinTableSortDirection;
}

interface CoinTableQuery {
  assets: CoinTableAsset[];
  changes: ReadonlyMap<string, MarketShareChangeAsset>;
  noticeAssets: ReadonlySet<string>;
  filters: CoinTableFilters;
  sort: CoinTableSort;
  view: CoinTableView;
}

export interface CoinTableRows {
  rows: CoinTableAsset[];
  /** Rows every other filter kept that the trade filter left out. */
  hiddenUntraded: number;
}

type SortValue = number | string | null | undefined;

/**
 * Whether the coin has values in this view: domestic trades in the selected
 * window, or for the change view also in the window it is compared with.
 * Other rows are empty in every number column.
 */
export function coinTradedInView(
  asset: CoinTableAsset,
  view: CoinTableView
): boolean {
  const tradedNow =
    (asset.domestic_volume_krw ?? 0) > 0 || asset.domestic_trade_count > 0;
  return (
    tradedNow ||
    (view === "impact" && asset.share_change_contribution_pp !== null)
  );
}

/** The ranking before a column is chosen; lower comes first, missing values go last. */
function defaultRank(asset: CoinTableAsset, view: CoinTableView): SortValue {
  if (view === "capture") return asset.capture_gap_vs_venue_pp;
  const contribution = asset.share_change_contribution_pp;
  return contribution === null ? null : -Math.abs(contribution);
}

function matchesDirection(
  value: number | null,
  direction: CoinContributionDirection
): boolean {
  if (direction === "all") return true;
  if (direction === "positive") return value !== null && value > 0;
  if (direction === "negative") return value !== null && value < 0;
  return value === 0;
}

function listingKey(asset: CoinTableAsset): Exclude<CoinListingFilter, "all"> {
  if (asset.exclusive_on_bithumb) return "bithumb-only";
  if (asset.shared_bithumb_upbit) return "bithumb-upbit";
  if (asset.listed_on_bithumb) return "bithumb-other";
  return "bithumb-unlisted";
}

function listingSortValue(asset: CoinTableAsset): string {
  if (asset.exclusive_on_bithumb) return "빗썸 단독";
  if (asset.shared_bithumb_upbit) return "빗썸·업비트 동시상장";
  if (asset.listed_on_bithumb) return "빗썸·기타 동시상장";
  return "빗썸 미상장";
}

function sortValue(
  asset: CoinTableAsset,
  change: MarketShareChangeAsset | undefined,
  hasNotice: boolean,
  key: CoinTableSortKey
): SortValue {
  switch (key) {
    case "asset":
      return asset.asset;
    case "contribution":
      return asset.share_change_contribution_pp;
    case "capture":
      return asset.bithumb_capture_pct;
    case "bithumb-change":
      return change?.bithumb_volume_change_krw;
    case "domestic-change":
      return change?.domestic_volume_change_krw;
    case "competitor":
      return asset.competitor_leader || null;
    case "listing":
      return listingSortValue(asset);
    case "notice":
      return hasNotice ? 1 : 0;
    case "domestic-volume":
      return asset.domestic_volume_krw;
    case "capture-gap":
      return asset.capture_gap_vs_venue_pp;
    case "bithumb-listed":
      return asset.listed_on_bithumb ? 1 : 0;
  }
}

function isMissing(value: SortValue): boolean {
  return (
    value === null ||
    value === undefined ||
    (typeof value === "number" && !Number.isFinite(value))
  );
}

function compareValues(left: SortValue, right: SortValue): number {
  if (typeof left === "number" && typeof right === "number") {
    return left - right;
  }
  return String(left).localeCompare(String(right), "ko");
}

export function queryCoinTableRows({
  assets,
  changes,
  noticeAssets,
  filters,
  sort,
  view,
}: CoinTableQuery): CoinTableRows {
  const needle = filters.search.trim().toUpperCase();
  let hiddenUntraded = 0;
  const filtered = assets.filter(asset => {
    if (needle && !asset.asset.includes(needle)) return false;
    if (
      !matchesDirection(asset.share_change_contribution_pp, filters.direction)
    )
      return false;
    if (filters.listing !== "all" && listingKey(asset) !== filters.listing)
      return false;
    const hasNotice = noticeAssets.has(asset.asset);
    if (filters.notice === "present" && !hasNotice) return false;
    if (filters.notice === "absent" && hasNotice) return false;
    if (filters.trade === "traded" && !coinTradedInView(asset, view)) {
      hiddenUntraded += 1;
      return false;
    }
    return true;
  });

  const rows = [...filtered].sort((left, right) => {
    const leftValue =
      sort.key === null
        ? defaultRank(left, view)
        : sortValue(
            left,
            changes.get(left.asset),
            noticeAssets.has(left.asset),
            sort.key
          );
    const rightValue =
      sort.key === null
        ? defaultRank(right, view)
        : sortValue(
            right,
            changes.get(right.asset),
            noticeAssets.has(right.asset),
            sort.key
          );
    const leftMissing = isMissing(leftValue);
    const rightMissing = isMissing(rightValue);
    if (leftMissing || rightMissing) {
      if (leftMissing && rightMissing)
        return left.asset.localeCompare(right.asset, "ko");
      return leftMissing ? 1 : -1;
    }
    const result = compareValues(leftValue, rightValue);
    if (result === 0) return left.asset.localeCompare(right.asset, "ko");
    return sort.key === null || sort.direction === "asc" ? result : -result;
  });
  return { rows, hiddenUntraded };
}
