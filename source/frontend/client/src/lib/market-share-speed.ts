import type { MarketShareMomentum } from "./market-share-types";
import { MARKET_SHARE_WINDOW_SEC } from "./market-share-config.ts";

export const CURRENT_TURNOVER_SPEED_WINDOW_SEC =
  MARKET_SHARE_WINDOW_SEC.TEN_SECONDS;

export function hasObservedMomentumRate(row: MarketShareMomentum): boolean {
  const status = row.coverage_status ?? row.status;
  return (
    status !== "UNAVAILABLE" &&
    row.coverage_sec > 0 &&
    row.rate_denominator_sec > 0 &&
    Number.isFinite(row.volume_per_sec_krw) &&
    Number.isFinite(row.market_volume_per_sec_krw)
  );
}

export function currentTurnoverSpeed(
  momentum: MarketShareMomentum[]
): MarketShareMomentum | null {
  const current = momentum.find(
    row => row.duration_sec === CURRENT_TURNOVER_SPEED_WINDOW_SEC
  );

  if (!current || !hasObservedMomentumRate(current)) {
    return null;
  }

  return current;
}
