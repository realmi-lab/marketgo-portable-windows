export type MarketShareSurface = "summary" | "detail";

export function shouldShowExchangeRegistry(
  surface: MarketShareSurface
): boolean {
  return surface === "detail";
}
