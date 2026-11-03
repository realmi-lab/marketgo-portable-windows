type MarketTimeDetail = "date-time" | "time" | "time-seconds";

// Live tables format many timestamps per render; build each formatter once.
const marketTimeFormats = new Map<MarketTimeDetail, Intl.DateTimeFormat>();

function marketTimeFormat(detail: MarketTimeDetail): Intl.DateTimeFormat {
  let format = marketTimeFormats.get(detail);
  if (!format) {
    format = new Intl.DateTimeFormat("ko-KR", {
      timeZone: "Asia/Seoul",
      ...(detail === "date-time"
        ? ({ month: "2-digit", day: "2-digit" } as const)
        : {}),
      hour: "2-digit",
      minute: "2-digit",
      ...(detail !== "time" ? ({ second: "2-digit" } as const) : {}),
      hour12: false,
    });
    marketTimeFormats.set(detail, format);
  }
  return format;
}

/** Format explicit Unix milliseconds in the dashboard's Korean time zone. */
export function formatMarketTimeMS(
  value: number | null | undefined,
  detail: MarketTimeDetail = "date-time"
): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0)
    return "—";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "—";
  return marketTimeFormat(detail).format(date);
}

const kstDate = (ms: number) =>
  new Date(ms + 9 * 3_600_000).toISOString().slice(0, 10);

/**
 * A time within the last day, with seconds: with its date as well when that
 * is not today in Korea, so yesterday's 17:00 is not read as today's.
 */
export function formatRecentMarketTimeMS(
  value: number | null | undefined,
  nowMS = Date.now()
): string {
  const today =
    typeof value === "number" &&
    value > 0 &&
    value < 8.64e15 &&
    kstDate(value) === kstDate(nowMS);
  return formatMarketTimeMS(value, today ? "time-seconds" : "date-time");
}
