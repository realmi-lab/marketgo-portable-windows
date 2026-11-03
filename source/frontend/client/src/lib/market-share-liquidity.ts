export function observedLiquidityValue(
  liveAssetCount: number | null | undefined,
  value: number | null | undefined
): number | null {
  if (
    typeof liveAssetCount !== "number" ||
    !Number.isFinite(liveAssetCount) ||
    liveAssetCount <= 0
  )
    return null;
  if (typeof value !== "number" || !Number.isFinite(value)) return null;
  return value;
}
