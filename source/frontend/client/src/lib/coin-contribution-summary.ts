import type { MarketShareChangeAsset } from "./market-share-types";

export interface CoinContributionSummary {
  positive: MarketShareChangeAsset[];
  negative: MarketShareChangeAsset[];
  bithumbVolumeGrowth: MarketShareChangeAsset[];
  competitorVolumeGrowth: MarketShareChangeAsset[];
}

function topBy(
  rows: MarketShareChangeAsset[],
  value: (row: MarketShareChangeAsset) => number,
  direction: "ascending" | "descending",
  limit: number
): MarketShareChangeAsset[] {
  return rows
    .filter(row => Number.isFinite(value(row)))
    .sort((left, right) =>
      direction === "descending"
        ? value(right) - value(left)
        : value(left) - value(right)
    )
    .slice(0, limit);
}

export function buildCoinContributionSummary(
  assets: MarketShareChangeAsset[],
  limit = 5
): CoinContributionSummary {
  const safeLimit = Math.max(0, Math.floor(limit));
  const positive = assets.filter(row => row.contribution_pp > 0);
  const negative = assets.filter(row => row.contribution_pp < 0);
  const bithumbGrowth = assets.filter(row => row.bithumb_volume_change_krw > 0);
  const competitorGrowth = assets.filter(
    row => row.competitor_volume_change_krw > 0
  );

  return {
    positive: topBy(
      positive,
      row => row.contribution_pp,
      "descending",
      safeLimit
    ),
    negative: topBy(
      negative,
      row => row.contribution_pp,
      "ascending",
      safeLimit
    ),
    bithumbVolumeGrowth: topBy(
      bithumbGrowth,
      row => row.bithumb_volume_change_krw,
      "descending",
      safeLimit
    ),
    competitorVolumeGrowth: topBy(
      competitorGrowth,
      row => row.competitor_volume_change_krw,
      "descending",
      safeLimit
    ),
  };
}
