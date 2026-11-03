import type { MarketShareOfficialTicker1mPoint } from "./market-share-types";

export interface OfficialHourlyChartPoint {
  bucket_ms: number;
  observed_at_ms: number;
  share: number | null;
  available: boolean;
}

export const OFFICIAL_HOURLY_CHART_POINT_COUNT = 30;

/** Query durable history even when the current process has only one sample. */
export function officialHourlyHistoryFromMS(latestEndMS: number): number {
  return Math.max(
    0,
    latestEndMS - (OFFICIAL_HOURLY_CHART_POINT_COUNT - 1) * 3_600_000
  );
}

/** Inclusive hourly endpoints describe one fewer elapsed interval than samples. */
export function officialHourlySeriesCoverageLabel(
  points: readonly OfficialHourlyChartPoint[]
): string {
  if (!points.length) return "정시 자료 없음";
  const available = points.filter(point => point.available).length;
  const hours = (points.at(-1)!.bucket_ms - points[0].bucket_ms) / 3_600_000;
  return `정시 자료 ${available}/${points.length}개${hours > 0 ? ` · ${hours.toLocaleString("ko-KR")}시간 구간` : ""}`;
}

export function mergeOfficialHourlyPoints(
  persisted: readonly MarketShareOfficialTicker1mPoint[],
  live: readonly MarketShareOfficialTicker1mPoint[]
): MarketShareOfficialTicker1mPoint[] {
  const byBucket = new Map<number, MarketShareOfficialTicker1mPoint>();
  for (const point of [...persisted, ...live]) {
    if (!Number.isFinite(point.bucket_ms) || point.bucket_ms <= 0) continue;
    byBucket.set(point.bucket_ms, point);
  }
  return [...byBucket.values()].sort(
    (left, right) => left.bucket_ms - right.bucket_ms
  );
}

export function buildOfficialHourlyChartSeries(
  source: readonly MarketShareOfficialTicker1mPoint[],
  intervalSec: number,
  limit: number
): OfficialHourlyChartPoint[] {
  const intervalMS = Math.max(1, Math.floor(intervalSec)) * 1_000;
  const safeLimit = Math.max(1, Math.floor(limit));
  const points = [...source]
    .filter(
      point =>
        Number.isFinite(point.bucket_ms) &&
        point.bucket_ms > 0 &&
        Number.isFinite(point.bithumb_share_pct)
    )
    .sort((left, right) => left.bucket_ms - right.bucket_ms);
  const byBucket = new Map<number, MarketShareOfficialTicker1mPoint>();
  for (const point of points) byBucket.set(point.bucket_ms, point);
  const lastBucket = points.at(-1)?.bucket_ms;
  if (!lastBucket) return [];
  const firstAvailableBucket = points[0].bucket_ms;
  const firstBucket = Math.max(
    firstAvailableBucket,
    lastBucket - (safeLimit - 1) * intervalMS
  );
  const result: OfficialHourlyChartPoint[] = [];
  for (
    let bucketMS = firstBucket;
    bucketMS <= lastBucket && result.length < safeLimit;
    bucketMS += intervalMS
  ) {
    const point = byBucket.get(bucketMS);
    result.push({
      bucket_ms: bucketMS,
      observed_at_ms: point?.observed_at_ms ?? bucketMS,
      share: point?.bithumb_share_pct ?? null,
      available: Boolean(point),
    });
  }
  return result;
}
