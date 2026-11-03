import type { IndexDayResponse } from "./index-day.ts";

// Poll rapidly only while the worker is publishing partial current-hour progress.
// Completed or historical snapshots retain the normal one-minute cadence.
export function indexRefreshInterval(data: IndexDayResponse | null | undefined): number {
  if (data?.storage_mode === "TICKER_24H") return 10_000;
  return data?.storage_mode === "ROLLING_24H_LATEST" &&
    data.rows.some(row => row.status === "ROLLING_COLLECTING") ? 15_000 : 60_000;
}
