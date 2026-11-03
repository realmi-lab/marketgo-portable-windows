/** Format an observed trade rate without turning a small nonzero rate into zero. */
export function formatLiveTradeRate(
  value: number | null | undefined,
  unit = "회/초"
): string {
  if (
    value === null ||
    value === undefined ||
    !Number.isFinite(value) ||
    value < 0
  ) {
    return "—";
  }
  if (value > 0 && value < 0.1) return `0.1${unit} 미만`;
  return `${value.toLocaleString("ko-KR", {
    maximumFractionDigits: value < 10 ? 1 : 0,
  })}${unit}`;
}
