/**
 * ChartKit — Recharts 공통 스타일/툴팁.
 *
 * 이전엔 Dashboard/Balances/Latency 가 각각 contentStyle/axis 폰트 색을
 * oklch 리터럴로 중복 정의했다. 여기서 테마 토큰 기반 통합.
 */
import type { CSSProperties } from "react";

/** Recharts tooltip 컨테이너 스타일 — 다크/라이트 자동 (CSS 변수 사용) */
export const chartTooltipStyle: CSSProperties = {
  backgroundColor: "var(--popover)",
  border: "1px solid var(--border)",
  borderRadius: "8px",
  fontFamily: "var(--font-mono)",
  fontSize: "16px",
  color: "var(--popover-foreground)",
  boxShadow: "var(--glass-shadow)",
};

export const chartTooltipLabelStyle: CSSProperties = {
  color: "var(--muted-foreground)",
};

/** X/Y축 tick 폰트 색 — 테마 적응형 */
export const chartAxisTickStyle = {
  fontFamily: "var(--font-mono)",
  fontSize: 16,
  fill: "var(--axis-fill)",
} as const;

/** CartesianGrid stroke 색 */
export const chartGridStroke = "var(--grid-stroke)";

/** 차트 hover cursor fill */
export const chartCursorFill = "var(--grid-stroke)";

/**
 * KRW compact 포매터를 Recharts tickFormatter 용으로 감싼 함수 반환.
 * 값이 없을 때 빈 문자열을 반환해 축이 깨지지 않게 한다.
 */
export function tickFormatterKrw(formatKrwCompact: (v: number) => string) {
  return (v: number) => {
    if (!Number.isFinite(v) || v === 0) return "";
    return formatKrwCompact(Number(v));
  };
}
