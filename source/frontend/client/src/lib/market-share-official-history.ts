import type {
  MarketShareOfficialAlignedContribution,
  MarketShareOfficialAlignedHistoryResponse,
} from "./market-share-types";

const KST_OFFSET_MS = 9 * 60 * 60 * 1_000;
export const OFFICIAL_ALIGNED_HOUR_MS = 60 * 60 * 1_000;
export const OFFICIAL_ALIGNED_HISTORY_WINDOW_MS = 24 * OFFICIAL_ALIGNED_HOUR_MS;
export const INCOMPLETE_OFFICIAL_CONTRIBUTION_RETRY_MS = 10_000;

/** A 24-hour inclusive range contains 25 hourly points, not 25 elapsed hours. */
export function officialHistorySampleCoverage(
  history:
    | Pick<
        MarketShareOfficialAlignedHistoryResponse,
        "observed_point_count" | "expected_point_count" | "missing_point_count"
      >
    | null
    | undefined
): string {
  if (!history) return "조회 불가";
  const observed = history.observed_point_count;
  const expected = history.expected_point_count;
  const missing = history.missing_point_count;
  if (
    !Number.isSafeInteger(observed) ||
    observed < 0 ||
    !Number.isSafeInteger(expected) ||
    expected <= 0 ||
    !Number.isSafeInteger(missing) ||
    missing < 0 ||
    observed > expected ||
    observed + missing !== expected
  )
    return "정시 자료 수 확인 중";
  const available = `정시 자료 ${observed.toLocaleString("ko-KR")}/${expected.toLocaleString("ko-KR")}개 확보`;
  return missing > 0
    ? `${available} · ${missing.toLocaleString("ko-KR")}개 없음`
    : available;
}

export function officialAlignedContributionIsComplete(
  value: MarketShareOfficialAlignedContribution,
  selectedEndMS: number
): boolean {
  const intervalMS = Math.max(1, value.window_sec || 3_600) * 1_000;
  return Boolean(
    value.status === "LIVE" &&
    value.endpoint_comparison_available &&
    value.missing_sample_count === 0 &&
    value.previous &&
    value.current?.bucket_ms === selectedEndMS &&
    value.previous.bucket_ms === selectedEndMS - intervalMS &&
    Array.isArray(value.asset_drivers) &&
    Array.isArray(value.exchange_drivers)
  );
}

export function buildOfficialAlignedHistoryPath(
  fromMS: number,
  toMS: number
): string {
  const query = new URLSearchParams({
    from_ms: String(Math.trunc(fromMS)),
    to_ms: String(Math.trunc(toMS)),
  });
  return `/api/market-share/official-aligned-history?${query}`;
}

export function buildOfficialAlignedContributionPath(toMS: number): string {
  const query = new URLSearchParams({ to_ms: String(Math.trunc(toMS)) });
  return `/api/market-share/official-aligned-contribution?${query}`;
}

export function officialHistoryKSTInputValue(valueMS: number): string {
  if (!Number.isFinite(valueMS) || valueMS <= 0) return "";
  return new Date(valueMS + KST_OFFSET_MS).toISOString().slice(0, 16);
}

export function parseOfficialHistoryKSTInput(value: string): number | null {
  const normalized = value.trim();
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:00$/.test(normalized)) return null;
  const parsed = Date.parse(`${normalized}:00+09:00`);
  if (
    !Number.isFinite(parsed) ||
    parsed <= 0 ||
    parsed % OFFICIAL_ALIGNED_HOUR_MS !== 0
  ) {
    return null;
  }
  return parsed;
}

export function latestSafeOfficialHourMS(nowMS: number): number {
  const safeMS = nowMS - 2 * 60 * 1_000;
  return (
    Math.floor(safeMS / OFFICIAL_ALIGNED_HOUR_MS) * OFFICIAL_ALIGNED_HOUR_MS
  );
}
