import {
  formatKrwMagnitude,
  formatPercentagePointMagnitude,
  formatSignedPercentagePoints,
} from "./formatters.ts";

function finite(value: number | null | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

/** Subtracting two rates produces percentage points, unlike a price ratio. */
export function formatRateDifferenceBps(
  value: number | null | undefined
): string {
  if (finite(value) && value !== 0 && Number((value / 100).toFixed(3)) === 0) {
    return `0.001%p 미만 ${value > 0 ? "높음" : "낮음"}`;
  }
  return finite(value) ? formatSignedPercentagePoints(value / 100) : "—";
}

/** Changes over time use increase/decrease, unlike a between-exchange price level. */
export function formatRelativeChangePercent(
  value: number | null | undefined,
  digits = 1
): string {
  if (!finite(value)) return "—";
  const precision = Number.isFinite(digits)
    ? Math.min(6, Math.max(0, Math.trunc(digits)))
    : 1;
  if (value === 0) return `${(0).toFixed(precision)}%`;
  if (Number(Math.abs(value).toFixed(precision)) === 0) {
    return `${(10 ** -precision).toFixed(precision)}% 미만 ${value > 0 ? "증가" : "감소"}`;
  }
  return `${value > 0 ? "+" : "-"}${Math.abs(value).toFixed(precision)}%`;
}

/** Slippage is a cost magnitude; a small observed cost is not a zero cost. */
export function formatSlippageRate(value: number | null | undefined): string {
  if (!finite(value)) return "—";
  if (value === 0) return "0.000%";
  const magnitude = Math.abs(value) / 100;
  const percent =
    Number(magnitude.toFixed(3)) === 0
      ? "0.001% 미만"
      : `${magnitude.toFixed(3)}%`;
  return value < 0 ? `${percent} 유리` : percent;
}

export function formatSlippageImpactKrw(
  notional: number,
  value: number | null | undefined
): string {
  if (!finite(notional) || notional <= 0 || !finite(value)) return "계산 대기";
  const impact = (notional * Math.abs(value)) / 10_000;
  if (!Number.isFinite(impact)) return "계산 대기";
  const magnitude =
    value !== 0 && impact < 1 ? "1원 미만" : formatKrwMagnitude(impact);
  return value < 0 ? `${magnitude} 유리` : magnitude;
}

export function formatShareChangeWords(
  value: number | null | undefined,
  digits = 2
): string {
  if (!finite(value)) return "변화 확인 대기";
  if (value === 0) return "변화 없음";
  return `${formatPercentagePointMagnitude(value, digits)} ${value > 0 ? "상승" : "하락"}`;
}

/** Missing comparison evidence must not look like an observed zero on a map. */
export function captureMapMarker(
  contribution: number | null | undefined,
  captureGap: number | null | undefined,
  maximumContribution: number
) {
  const contributionPP = finite(contribution) ? contribution : null;
  const captureGapPP = finite(captureGap) ? captureGap : null;
  const scale =
    finite(maximumContribution) && maximumContribution > 0
      ? maximumContribution
      : 1;
  return {
    contributionPP,
    radius:
      contributionPP === null
        ? 3.5
        : 3.5 + (Math.abs(contributionPP) / scale) * 8,
    tone:
      captureGapPP === null
        ? "unknown"
        : captureGapPP > 0
          ? "above"
          : captureGapPP < 0
            ? "below"
            : "equal",
  } as const;
}

export function formatObservationSeconds(
  observed: number | null | undefined,
  required: number | null | undefined
): string {
  const count = (value: number | null | undefined) =>
    finite(value) && Number.isSafeInteger(value) && value >= 0
      ? value.toLocaleString("ko-KR")
      : "—";
  return `${count(observed)}/${count(required)}초`;
}
