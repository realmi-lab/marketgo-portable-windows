export const MARKET_SHARE_DASHBOARD_MODES = [
  "movement",
  "summary",
  "window",
] as const;

export type MarketShareDashboardMode =
  (typeof MARKET_SHARE_DASHBOARD_MODES)[number];

export function marketShareViewForDashboardMode(
  mode: MarketShareDashboardMode
): "summary" | "overview" | null {
  if (mode === "summary") return "summary";
  if (mode === "window") return "overview";
  return null;
}

export function initialMarketShareDashboardMode(
  hash: string
): MarketShareDashboardMode {
  return hash === "#overview" ? "window" : "movement";
}
