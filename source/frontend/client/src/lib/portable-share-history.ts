import { DOMESTIC_EXCHANGES } from "./market-share-config.ts";
import { appendLiveHistoryGap, appendLiveHistoryPoint, type LiveChartHistoryPoint } from "./live-chart.ts";
import type { MarketShareOfficialDomestic24h } from "./market-share-types.ts";

type OfficialShare = Pick<MarketShareOfficialDomestic24h, "as_of" | "status" | "exchanges">;

// Tick/analysis delays do not prove that the independent official ticker feed
// disconnected. Retain only its real source timestamps and never fill missing values.
export function appendPortableOfficialShare(
  history: LiveChartHistoryPoint[],
  official: OfficialShare | undefined,
  connection: "live" | "stale" | "offline" | "connecting",
  paused: boolean
): LiveChartHistoryPoint[] {
  if (paused) return history;
  if (connection === "offline" || connection === "connecting")
    return appendLiveHistoryGap(history);
  if (!official || !Number.isFinite(official.as_of) || official.as_of <= 0)
    return history;
  const point: LiveChartHistoryPoint = { at: official.as_of * 1000 };
  if (official.status !== "LIVE") point.partial = 1;
  for (const exchange of DOMESTIC_EXCHANGES) {
    const row = official.exchanges[exchange];
    point[`share_${exchange}`] = row?.status === "LIVE" ? row.share_pct ?? null : null;
  }
  const previous = history.at(-1);
  if (previous?.at === point.at && !previous.gap &&
      previous.partial === point.partial &&
      DOMESTIC_EXCHANGES.every(exchange => previous[`share_${exchange}`] === point[`share_${exchange}`]))
    return history;
  return appendLiveHistoryPoint(history, point);
}
