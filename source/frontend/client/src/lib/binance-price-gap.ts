export interface PriceGapLeg {
  direction: "Binance → 빗썸" | "빗썸 → Binance";
  gross: number;
  net: number | null;
}

export interface PriceGapCandidate {
  gross: PriceGapLeg;
  net: PriceGapLeg | null;
}

/** This is a cross-venue price difference, not a price rise/fall over time. */
export function formatRelativePricePercent(
  value: number | null | undefined
): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "—";
  if (value === 0) return "0.00%";
  const magnitude = Math.abs(value);
  if (Number(magnitude.toFixed(2)) === 0) {
    return `0.01% 미만 ${value > 0 ? "높음" : "낮음"}`;
  }
  return `${value > 0 ? "+" : "-"}${magnitude.toFixed(2)}%`;
}

export function parsePercentRate(value: string): number | null {
  const normalized = value.trim();
  if (!/^(?:\d+(?:\.\d*)?|\.\d+)$/.test(normalized)) return null;
  const parsed = Number(normalized);
  return Number.isFinite(parsed) && parsed >= 0 && parsed <= 100
    ? parsed / 100
    : null;
}

export function buildPriceGapCandidate(
  bithumbMid: number | null,
  binanceBid: number | null,
  binanceAsk: number | null,
  bithumbFee: number | null,
  binanceFee: number | null
): PriceGapCandidate | null {
  if (
    bithumbMid === null ||
    binanceBid === null ||
    binanceAsk === null ||
    !Number.isFinite(bithumbMid) ||
    !Number.isFinite(binanceBid) ||
    !Number.isFinite(binanceAsk) ||
    bithumbMid <= 0 ||
    binanceBid <= 0 ||
    binanceAsk <= 0 ||
    binanceBid > binanceAsk
  ) {
    return null;
  }
  const feesAvailable =
    bithumbFee !== null &&
    binanceFee !== null &&
    Number.isFinite(bithumbFee) &&
    Number.isFinite(binanceFee) &&
    bithumbFee >= 0 &&
    bithumbFee <= 1 &&
    binanceFee >= 0 &&
    binanceFee <= 1;
  const candidates: PriceGapLeg[] = [
    {
      direction: "Binance → 빗썸",
      gross: bithumbMid / binanceAsk - 1,
      net: feesAvailable
        ? (bithumbMid * (1 - bithumbFee)) / (binanceAsk * (1 + binanceFee)) - 1
        : null,
    },
    {
      direction: "빗썸 → Binance",
      gross: binanceBid / bithumbMid - 1,
      net: feesAvailable
        ? (binanceBid * (1 - binanceFee)) / (bithumbMid * (1 + bithumbFee)) - 1
        : null,
    },
  ];
  return {
    gross: candidates.reduce((best, item) =>
      item.gross > best.gross ? item : best
    ),
    net: feesAvailable
      ? candidates.reduce((best, item) => (item.net! > best.net! ? item : best))
      : null,
  };
}
