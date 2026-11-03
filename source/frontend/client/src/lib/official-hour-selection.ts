import type { MarketShareOfficialAlignedContribution } from "./market-share-types";

/** A saved warm-up endpoint must not hide a newer complete comparison on reentry. */
export function restoreOfficialHourSelection(
  selectedEndMS: number,
  availableHourMS: ReadonlySet<number>,
  latestEndMS: number,
  intervalSec: number
): number {
  const comparable = new Set(
    [...availableHourMS].filter(end =>
      availableHourMS.has(end - intervalSec * 1_000)
    )
  );
  return resolveOfficialHourSelection(
    selectedEndMS,
    comparable.size ? comparable : availableHourMS,
    latestEndMS
  );
}

/** Persisted selections may outlive the visible chart window. */
export function resolveOfficialHourSelection(
  selectedEndMS: number,
  availableHourMS: ReadonlySet<number>,
  latestEndMS: number,
  previousLatestEndMS?: number
): number {
  if (
    selectedEndMS === previousLatestEndMS &&
    latestEndMS > selectedEndMS &&
    availableHourMS.has(latestEndMS)
  )
    return latestEndMS;
  if (availableHourMS.has(selectedEndMS)) return selectedEndMS;
  if (availableHourMS.has(latestEndMS)) return latestEndMS;
  return Math.max(0, ...availableHourMS);
}

/** Display comparison values only when both server endpoints match the selected hour. */
export function selectedOfficialHourState(
  contribution: MarketShareOfficialAlignedContribution,
  selectedEndMS: number
) {
  const matchesSelection =
    Number.isFinite(selectedEndMS) &&
    selectedEndMS > 0 &&
    contribution.current?.bucket_ms === selectedEndMS;
  const intervalSec = contribution.window_sec;
  const comparisonReady =
    matchesSelection &&
    contribution.status === "LIVE" &&
    contribution.endpoint_comparison_available === true &&
    Number.isFinite(intervalSec) &&
    intervalSec > 0 &&
    contribution.previous?.bucket_ms === selectedEndMS - intervalSec * 1_000;
  return {
    matchesSelection,
    comparisonReady,
    shareDeltaPP:
      comparisonReady && Number.isFinite(contribution.share_delta_pp)
        ? contribution.share_delta_pp
        : null,
  };
}
