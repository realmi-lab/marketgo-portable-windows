export const DOMESTIC_EXCHANGES = [
  "bithumb",
  "upbit",
  "coinone",
  "korbit",
  "gopax",
] as const;

export type DomesticExchange = (typeof DOMESTIC_EXCHANGES)[number];

export const MARKET_SHARE_WINDOW_SEC = {
  REALTIME: 0,
  TEN_SECONDS: 10,
  THIRTY_SECONDS: 30,
  ONE_MINUTE: 60,
  TEN_MINUTES: 600,
  THIRTY_MINUTES: 1800,
  ONE_HOUR: 3600,
  ONE_DAY: 86400,
} as const;

export const MARKET_SHARE_WINDOWS = [
  { seconds: MARKET_SHARE_WINDOW_SEC.TEN_SECONDS, label: "10초" },
  { seconds: MARKET_SHARE_WINDOW_SEC.THIRTY_SECONDS, label: "30초" },
  { seconds: MARKET_SHARE_WINDOW_SEC.ONE_MINUTE, label: "1분" },
  { seconds: MARKET_SHARE_WINDOW_SEC.TEN_MINUTES, label: "10분" },
  { seconds: MARKET_SHARE_WINDOW_SEC.ONE_HOUR, label: "1시간" },
  { seconds: MARKET_SHARE_WINDOW_SEC.ONE_DAY, label: "24시간" },
] as const;

/** Every rolling window the calculator analyses (aggregate.DefaultWindows). */
export const ANALYSIS_WINDOWS = [
  { seconds: 10, label: "10초" },
  { seconds: 30, label: "30초" },
  { seconds: 60, label: "1분" },
  { seconds: 300, label: "5분" },
  { seconds: 600, label: "10분" },
  { seconds: 1_800, label: "30분" },
  { seconds: 3_600, label: "1시간" },
  { seconds: 7_200, label: "2시간" },
  { seconds: 14_400, label: "4시간" },
  { seconds: 21_600, label: "6시간" },
  { seconds: 43_200, label: "12시간" },
  { seconds: 86_400, label: "24시간" },
] as const;

export const MARKET_SHARE_NETWORK_WINDOWS = [
  { seconds: 60, label: "1분" },
  { seconds: 300, label: "5분" },
  { seconds: 600, label: "10분" },
  { seconds: 900, label: "15분" },
  { seconds: 1_800, label: "30분" },
  { seconds: 3_600, label: "1시간" },
  { seconds: 7_200, label: "2시간" },
  { seconds: 10_800, label: "3시간" },
  { seconds: 21_600, label: "6시간" },
  { seconds: 43_200, label: "12시간" },
  { seconds: 86_400, label: "24시간" },
] as const;

export type MarketShareNetworkWindowSec =
  (typeof MARKET_SHARE_NETWORK_WINDOWS)[number]["seconds"];

export function marketShareNetworkWindowLabel(
  seconds: MarketShareNetworkWindowSec
): string {
  return (
    MARKET_SHARE_NETWORK_WINDOWS.find(option => option.seconds === seconds)
      ?.label ?? `${seconds}초`
  );
}

export const MARKET_SHARE_NOTICE_WINDOWS = [
  { seconds: MARKET_SHARE_WINDOW_SEC.REALTIME, label: "실시간" },
  ...MARKET_SHARE_WINDOWS,
] as const;

export const ORDERBOOK_DEPTH_LEVELS = ["1", "3", "5", "10"] as const;
