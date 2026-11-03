export interface OfficialContributionInsightDriver {
  contribution_pp: number;
  bithumb_volume_change_krw: number;
  competitor_volume_change_krw: number;
}

export interface OfficialContributionInsight {
  available: boolean;
  positiveCount: number | null;
  negativeCount: number | null;
  positiveGrossPP: number | null;
  negativeGrossPP: number | null;
  positiveTopFiveSharePct: number | null;
  negativeTopFiveSharePct: number | null;
  cancellationPct: number | null;
}

function topFiveShare(values: number[]): number | null {
  const total = values.reduce((sum, value) => sum + value, 0);
  if (total <= 0) return null;
  const topFive = [...values]
    .sort((left, right) => right - left)
    .slice(0, 5)
    .reduce((sum, value) => sum + value, 0);
  return (topFive / total) * 100;
}

export function buildOfficialContributionInsight(
  drivers: readonly OfficialContributionInsightDriver[],
  netShareDeltaPP: number | null | undefined,
  endpointsReady = true
): OfficialContributionInsight {
  // Exact endpoint totals can exist without persisted coin-level evidence.
  // A nonempty, valid all-zero driver set is available; an absent set is not.
  const available =
    endpointsReady &&
    Number.isFinite(netShareDeltaPP) &&
    drivers.length > 0 &&
    drivers.every(driver => Number.isFinite(driver.contribution_pp));
  if (!available) {
    return {
      available: false,
      positiveCount: null,
      negativeCount: null,
      positiveGrossPP: null,
      negativeGrossPP: null,
      positiveTopFiveSharePct: null,
      negativeTopFiveSharePct: null,
      cancellationPct: null,
    };
  }
  const positive = drivers
    .map(driver => driver.contribution_pp)
    .filter(value => Number.isFinite(value) && value > 0);
  const negative = drivers
    .map(driver => driver.contribution_pp)
    .filter(value => Number.isFinite(value) && value < 0)
    .map(Math.abs);
  const positiveGrossPP = positive.reduce((sum, value) => sum + value, 0);
  const negativeGrossPP = negative.reduce((sum, value) => sum + value, 0);
  const grossPP = positiveGrossPP + negativeGrossPP;
  const cancellationPct =
    grossPP > 0 && Number.isFinite(netShareDeltaPP)
      ? Math.max(
          0,
          Math.min(
            100,
            (1 - Math.abs(netShareDeltaPP as number) / grossPP) * 100
          )
        )
      : null;
  return {
    available: true,
    positiveCount: positive.length,
    negativeCount: negative.length,
    positiveGrossPP,
    negativeGrossPP,
    positiveTopFiveSharePct: topFiveShare(positive),
    negativeTopFiveSharePct: topFiveShare(negative),
    cancellationPct,
  };
}
