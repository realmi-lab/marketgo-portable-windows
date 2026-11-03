/*
 * Grouping of the calculator's observation-gap records for the data-status
 * screen. Durations are the server's; this only counts the records per place
 * and picks the longest, so a reader sees where the gaps came from. It adds
 * no durations: the records are the newest ones the server keeps, not a
 * complete account of missing time.
 */

export interface GapRecord {
  start_second: number;
  end_second: number | null;
  duration_sec: number;
  affected_exchanges: readonly string[];
  /** Some markets only; empty means every market of the exchanges. */
  affected_markets?: readonly string[];
  reason_codes: readonly string[];
  observed_volume_at_risk_pct: number | null;
}

/** Where a gap happened: one exchange each, or "all" for a gap of every exchange. */
export function gapPlaces(gap: GapRecord): string[] {
  return gap.affected_exchanges.length ? [...gap.affected_exchanges] : ["all"];
}

export interface GapPlaceSummary {
  place: string;
  count: number;
  /** The longest of the place's records, as the server measured it. */
  longestSec: number;
}

export function gapSummaryByPlace(
  gaps: readonly GapRecord[],
  sinceSecond: number
): GapPlaceSummary[] {
  const byPlace = new Map<string, GapPlaceSummary>();
  for (const gap of gaps) {
    if ((gap.end_second ?? Number.MAX_SAFE_INTEGER) < sinceSecond) continue;
    for (const place of gapPlaces(gap)) {
      const entry = byPlace.get(place) ?? { place, count: 0, longestSec: 0 };
      entry.count += 1;
      entry.longestSec = Math.max(entry.longestSec, gap.duration_sec);
      byPlace.set(place, entry);
    }
  }
  return [...byPlace.values()].sort(
    (left, right) =>
      right.count - left.count || right.longestSec - left.longestSec
  );
}

export type GapKind = "connection" | "collector" | "journal" | "other";

/** The family of a gap's first reason, for colouring the gap map. */
export function gapKind(reasonCodes: readonly string[]): GapKind {
  const codes = reasonCodes.join(" ");
  if (/WEBSOCKET_|SUBSCRIPTION_ERROR|TRADE_STREAM_NOT_READY/.test(codes))
    return "connection";
  if (/COLLECTOR_OBSERVATION_INCOMPLETE/.test(codes)) return "collector";
  if (/COLLECTOR_HEALTH_JOURNAL_GAP|PROCESS_DOWNTIME/.test(codes))
    return "journal";
  return "other";
}

/**
 * "0.02%" of domestic trading at risk, or why it is unknown. A gap over some
 * markets carries those markets' share, and says so. The server weighs a gap
 * only while it is open, and not when another gap of the same exchanges
 * covers the minutes it weighs against, so a closed gap without a weight
 * stays unknown rather than waiting to be weighed.
 */
export function gapRiskLabel(
  gap: Pick<
    GapRecord,
    | "affected_exchanges"
    | "affected_markets"
    | "observed_volume_at_risk_pct"
    | "end_second"
  >
): string {
  if (typeof gap.observed_volume_at_risk_pct === "number") {
    const value = gap.observed_volume_at_risk_pct;
    const share =
      value > 0 && value < 0.01 ? "0.01% 미만" : `${value.toFixed(2)}%`;
    const markets = gap.affected_markets?.length ?? 0;
    return markets > 0 ? `${share} · 마켓 ${markets}개` : share;
  }
  if (!gap.affected_exchanges.length) return "전체";
  return gap.end_second === null ? "계산 전" : "알 수 없음";
}
