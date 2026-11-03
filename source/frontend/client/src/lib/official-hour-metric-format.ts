type OptionalMetric = number | null | undefined;

function finiteMetric(value: OptionalMetric): value is number {
  return value !== null && value !== undefined && Number.isFinite(value);
}

function fixed(value: number, digits: number): string {
  return value.toLocaleString("ko-KR", {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });
}

/** Selected hours use the same Korean time zone as the official-hour chart. */
export function formatOfficialHourTimestamp(value: OptionalMetric): string {
  if (!finiteMetric(value) || value <= 0) return "시각 정보 없음";
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "시각 정보 없음";
  return date.toLocaleString("ko-KR", {
    timeZone: "Asia/Seoul",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

/**
 * Formats raw server values once; no share or contribution calculation here.
 * Two decimals, three only when two would show 0.00 (adaptivePercentDigits).
 */
export function formatOfficialHourShare(value: OptionalMetric): string {
  return formatSharePercent(value);
}

export function formatOfficialHourContribution(value: OptionalMetric): string {
  if (!finiteMetric(value)) return "—";
  if (value === 0) return "변화 없음";
  const sign = value < 0 ? "-" : "+";
  const digits = adaptivePercentDigits(value);
  if (Number(Math.abs(value).toFixed(digits)) === 0) {
    return `0.001%p 미만 ${value > 0 ? "상승" : "하락"}`;
  }
  return `${sign}${fixed(Math.abs(value), digits)}%p`;
}

export function formatOfficialHourMoneyChange(value: OptionalMetric): string {
  if (!finiteMetric(value)) return "—";
  if (value === 0) return "0.00억";
  const sign = value < 0 ? "-" : "+";
  const hundredMillions = Math.abs(value) / 100_000_000;
  if (Number(hundredMillions.toFixed(2)) === 0) {
    return `0.01억 미만 ${value > 0 ? "증가" : "감소"}`;
  }
  return `${sign}${fixed(hundredMillions, 2)}억`;
}
import { adaptivePercentDigits, formatSharePercent } from "./formatters.ts";
