import type { LiveChartHistoryPoint } from "./live-chart";

type AxisUnit = { divisor: number; unit: string };

function axisUnit(maximum: number, suffix: string): AxisUnit {
  if (maximum >= 1e12) return { divisor: 1e12, unit: `조${suffix}` };
  if (maximum >= 1e8) return { divisor: 1e8, unit: `억${suffix}` };
  if (maximum >= 1e4) return { divisor: 1e4, unit: `만${suffix}` };
  return { divisor: 1, unit: suffix };
}

/** Presentation units only: series, domains and tooltip values stay unscaled. */
export function buildLiveChartMobileAxes(
  history: readonly LiveChartHistoryPoint[]
): { volume: AxisUnit; trades: AxisUnit } {
  let volumeMaximum = 0;
  let tradeMaximum = 0;
  for (const point of history) {
    for (const [key, value] of Object.entries(point)) {
      if (typeof value !== "number" || !Number.isFinite(value)) continue;
      if (key.startsWith("rate_")) {
        volumeMaximum = Math.max(volumeMaximum, Math.abs(value));
      } else if (key.startsWith("trades_")) {
        tradeMaximum = Math.max(tradeMaximum, Math.abs(value));
      }
    }
  }
  return {
    volume: axisUnit(volumeMaximum, "원/초"),
    trades: axisUnit(tradeMaximum, "회/초"),
  };
}

export function formatLiveChartAxisTick(
  value: number,
  divisor: number
): string {
  if (!Number.isFinite(value)) return "—";
  const scaled = value / divisor;
  if (scaled === 0) return "0";
  // Preserve a nonzero sub-unit tick instead of rounding it to zero. Auto-width
  // axes measure the resulting text, including the rare scientific notation.
  if (Math.abs(scaled) < 0.01) return scaled.toPrecision(2);
  return scaled.toLocaleString("ko-KR", {
    useGrouping: false,
    maximumFractionDigits: 2,
  });
}
