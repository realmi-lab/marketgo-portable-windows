/** Server-supplied post/pre volume ratio; a missing baseline is not zero. */
export function formatNoticeVolumeMultiplier(
  value: number | null | undefined
): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0)
    return "기준 없음";
  if (value > 0 && Number(value.toFixed(2)) === 0) return "0.01배 미만";
  return `${value.toFixed(2)}배`;
}
