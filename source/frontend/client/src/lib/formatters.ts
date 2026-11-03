/**
 * formatters.ts — 통합 포매터.
 *
 * 이전엔 KRW/USD/Time 포매터가 여러 파일에 갈라져 있었다:
 *   - abi2-adapters.ts: formatKRW, formatUSD, formatHedgeUsd, formatCoinQty...
 *   - balance-utils.ts: formatKrwCompact, formatKrwFull, formatBalanceAsset
 *   - 6개 페이지 로컬: formatTime, formatTimeMs
 * 모두 여기로 모은다. 기존 이름은 재-export 로 유지해 import 가 깨지지 않게 한다.
 */

/* ── KRW ── */

// toLocaleString(locale, options) builds a new formatter per call; tables
// format every coin row on each update, so keep one per fraction width.
const fixedFractionFormats = new Map<number, Intl.NumberFormat>();

function fixedFractionFormat(digits: number): Intl.NumberFormat {
  let format = fixedFractionFormats.get(digits);
  if (!format) {
    format = new Intl.NumberFormat("ko-KR", {
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
    });
    fixedFractionFormats.set(digits, format);
  }
  return format;
}

/** A nonzero amount below one won remains different from an observed zero. */
function compactWonMagnitude(value: number): string {
  if (value > 0 && value < 1) return "₩1 미만";
  const units = [
    { scale: 1, digits: 0, suffix: "" },
    { scale: 1e4, digits: 1, suffix: "만" },
    { scale: 1e8, digits: 2, suffix: "억" },
    { scale: 1e12, digits: 2, suffix: "조" },
  ];
  let index = units.findLastIndex(unit => value >= unit.scale);
  if (index < 0) index = 0;
  // Rounding may cross a unit boundary (9,999.6 won, for example).
  // Select the final unit before formatting instead of emitting 10,000만.
  while (index < units.length - 1) {
    const unit = units[index];
    if (
      Number((value / unit.scale).toFixed(unit.digits)) * unit.scale <
      units[index + 1].scale
    ) {
      break;
    }
    index++;
  }
  const unit = units[index];
  const amount = value / unit.scale;
  // Thousands separators in every unit: ₩7,598.79억 like ₩3,469억, not ₩7598.79억.
  const text =
    unit.scale === 1
      ? Math.round(amount).toLocaleString("ko-KR")
      : fixedFractionFormat(unit.digits).format(amount);
  return `₩${text}${unit.suffix}`;
}

/**
 * Decimals for a percentage or percentage-point value: two, and three only
 * when two would print a non-zero value as 0.00 (the display rule set on
 * 2026-09-29). A value that is still zero at three decimals is left to the
 * caller's "미만" wording.
 */
export function adaptivePercentDigits(value: number): 2 | 3 {
  return value !== 0 &&
    Number.isFinite(value) &&
    Number(Math.abs(value).toFixed(2)) === 0
    ? 3
    : 2;
}

/** The default two decimals follow the rule above; other counts are kept. */
function percentPrecision(value: number, digits: number): number {
  if (!Number.isFinite(digits)) return adaptivePercentDigits(value);
  const precision = Math.min(6, Math.max(0, Math.trunc(digits)));
  return precision === 2 ? adaptivePercentDigits(value) : precision;
}

/** 원화 compact (조/억/만 단위, 양수에는 증가 부호를 붙이지 않음). */
export function formatKRW(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return `${value < 0 ? "-" : ""}${compactWonMagnitude(Math.abs(value))}`;
}

/** 원화 compact + 부호 (손익 표시용) — balance-utils.formatKrwCompact 동일 */
export function formatKrwCompact(value: number): string {
  if (!Number.isFinite(value)) return "—";
  const sign = value < 0 ? "-" : value > 0 ? "+" : "";
  return `${sign}${compactWonMagnitude(Math.abs(value))}`;
}

/**
 * A percentage level keeps the exact 0/100 boundaries distinct from rounding.
 * A share above 100% is inconsistent data, so it is not shown as a value.
 */
export function formatSharePercent(
  value: number | null | undefined,
  digits = 2
): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "—";
  if (value < 0 || value > 100) return "확인 필요";
  const precision = percentPrecision(value, digits);
  const magnitude = Math.abs(value);
  const rounded = Number(magnitude.toFixed(precision));
  if (value !== 0 && rounded === 0) {
    return `${value < 0 ? "-" : ""}${(10 ** -precision).toFixed(precision)}% 미만`;
  }
  if (value > 0 && value < 100 && rounded === 100) return "100% 미만";
  return `${value < 0 ? "-" : ""}${magnitude.toFixed(precision)}%`;
}

/** 문장의 '늘었습니다/줄었습니다'와 함께 쓰는 부호 없는 변화 크기. */
export function formatKrwMagnitude(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return formatKrwCompact(Math.abs(value)).replace(/^\+/, "");
}

/** 문장의 '상승/하락'과 함께 쓰는 부호 없는 퍼센트포인트 변화 크기. */
export function formatPercentagePointMagnitude(
  value: number,
  digits = 2
): string {
  if (!Number.isFinite(value)) return "—";
  const precision = Math.min(3, percentPrecision(value, digits));
  const absolute = Math.abs(value);
  if (absolute === 0) return "변화 없음";
  if (value !== 0 && Number(absolute.toFixed(precision)) === 0) {
    return `${(10 ** -precision).toFixed(precision)}%p 미만`;
  }
  return `${absolute.toFixed(precision)}%p`;
}

/** Tiny changes keep their direction instead of becoming a signed or unsigned zero. */
export function formatSignedPercentagePoints(
  value: number,
  digits = 2
): string {
  if (!Number.isFinite(value)) return "—";
  const precision = percentPrecision(value, digits);
  const rounded = Number(value.toFixed(precision));
  if (value === 0) return "변화 없음";
  if (rounded === 0) {
    return `${(10 ** -precision).toFixed(precision)}%p 미만 ${value > 0 ? "상승" : "하락"}`;
  }
  const sign = rounded > 0 ? "+" : rounded < 0 ? "-" : "";
  return `${sign}${Math.abs(rounded).toFixed(precision)}%p`;
}

/** Relative price comparisons use percent; tiny differences remain distinct from 0. */
export function formatSignedPercent(
  value: number | null | undefined,
  digits = 2
): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "—";
  const precision = percentPrecision(value, digits);
  const sign = value < 0 ? "-" : value > 0 ? "+" : "";
  const magnitude = Math.abs(value);
  if (value !== 0 && Number(magnitude.toFixed(precision)) === 0) {
    return `${(10 ** -precision).toFixed(precision)}% 미만 ${value > 0 ? "높음" : "낮음"}`;
  }
  return `${sign}${magnitude.toFixed(precision)}%`;
}

/** 원화 가격 — 1원 미만의 유효한 소수 가격을 0원으로 반올림하지 않는다. */
const krwPriceFormat = new Intl.NumberFormat("ko-KR", {
  maximumSignificantDigits: 12,
});

export function formatKrwPrice(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return `${value < 0 ? "-" : ""}₩${krwPriceFormat.format(Math.abs(value))}`;
}

/** 원화 전체 자리 */
export function formatKrwFull(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return `₩${Math.round(value).toLocaleString("ko-KR")}`;
}

/** 부호가 붙은 원화 전체 (P&L 행) */
export function formatSignedKrw(value: number): string {
  if (!Number.isFinite(value)) return "—";
  return `${value < 0 ? "-" : "+"}₩${Math.round(Math.abs(value)).toLocaleString("ko-KR")}`;
}

/** USDT/KRW 환율 표시 */
export function formatUsdtKrwRate(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "n/a";
  return `₩${Math.round(value).toLocaleString("ko-KR")}`;
}

/* ── USD ── */

export function formatUSD(value: number): string {
  return `$${value.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

/** 헤지용 USD — 값 크기에 따라 소수 자릿수 조절 */
export function formatHedgeUsd(value: number): string {
  if (!Number.isFinite(value)) return "—";
  const abs = Math.abs(value);
  let maxFrac = 2;
  if (abs >= 10_000) maxFrac = 0;
  else if (abs >= 100) maxFrac = 2;
  else if (abs >= 1) maxFrac = 3;
  else maxFrac = 4;
  const formatted = abs.toLocaleString("en-US", {
    minimumFractionDigits: 0,
    maximumFractionDigits: maxFrac,
  });
  return value < 0 ? `-$${formatted}` : `$${formatted}`;
}

/* ── 코인 수량 ── */

/** 코인 수량 표시 자릿수 (최대 6) */
export function coinQtyDecimals(_coin: string): number {
  return 6;
}

export function formatCoinQty(qty: number, coin: string): string {
  if (!Number.isFinite(qty) || qty <= 0) return "—";
  return qty.toFixed(coinQtyDecimals(coin));
}

export function formatCoinQtyWithCoin(qty: number, coin: string): string {
  const q = formatCoinQty(qty, coin);
  if (q === "—") return `— ${coin}`;
  return `${q} ${coin}`;
}

export function formatHedgeQty(qty: number, coin: string): string {
  return formatCoinQty(qty, coin);
}

export function formatHedgeQtyWithCoin(qty: number, coin: string): string {
  return formatCoinQtyWithCoin(qty, coin);
}

/** 자산별 잔고 표시 */
export function formatBalanceAsset(asset: string, amount: number): string {
  if (!Number.isFinite(amount)) return "—";
  const key = asset.toUpperCase();
  if (key === "KRW") return `₩${Math.round(amount).toLocaleString("ko-KR")}`;
  if (key === "USDT" || key === "USDC")
    return (
      amount.toLocaleString("en-US", { maximumFractionDigits: 2 }) + ` ${key}`
    );
  return `${amount.toFixed(6)} ${key}`;
}

/* ── 디버그용 ── */

export function formatFullDecimal(value: number, maxDecimals = 16): string {
  if (!Number.isFinite(value)) return "—";
  if (value === 0) return "0";
  const s = value.toFixed(maxDecimals);
  return s.includes(".") ? s.replace(/\.?0+$/, "") : s;
}

/* ── 시간 ── */

/** 초 단위 timestamp → HH:MM:SS.mmm */
export function formatTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return "—";
  return formatTimeMs(seconds * 1000);
}

/** 밀리초 → HH:MM:SS.mmm */
export function formatTimeMs(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "—";
  const totalMs = Math.floor(ms);
  const h = Math.floor(totalMs / 3_600_000);
  const m = Math.floor((totalMs % 3_600_000) / 60_000);
  const s = Math.floor((totalMs % 60_000) / 1000);
  const frac = totalMs % 1000;
  const pad = (n: number, l = 2) => String(n).padStart(l, "0");
  return `${pad(h)}:${pad(m)}:${pad(s)}.${pad(frac, 3)}`;
}

/**
 * Exact Korean duration: 12시간 38분 18초. Every part is kept, so an observed
 * and a missing span printed side by side still add up to the whole window.
 */
export function formatDuration(seconds: number | null | undefined): string {
  if (typeof seconds !== "number" || !Number.isFinite(seconds) || seconds < 0)
    return "—";
  const total = Math.floor(seconds);
  const parts = [
    [Math.floor(total / 3_600), "시간"],
    [Math.floor((total % 3_600) / 60), "분"],
    [total % 60, "초"],
  ] as const;
  const text = parts
    .filter(([amount]) => amount > 0)
    .map(([amount, unit]) => `${amount.toLocaleString("ko-KR")}${unit}`)
    .join(" ");
  return text || "0초";
}

/** 초 → "Xh Ym" / "Ym" / "Xs" */
export function formatSeconds(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return "0s";
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m`;
  return `${Math.round(sec)}s`;
}

/** timestamp(ms) 기준 경과 "Ns" / "Nm" */
export function formatOrderAge(tsMs: number): string {
  if (!tsMs) return "—";
  const diff = Date.now() - tsMs;
  if (diff < 60_000) return `${Math.max(0, Math.round(diff / 1000))}s`;
  return `${Math.round(diff / 60_000)}m`;
}

/** funding rate → 소수점 5자리 % */
export function formatFundingRate(rate: number): string {
  if (!Number.isFinite(rate)) return "—";
  return `${rate.toFixed(5)}%`;
}

/** 백분율 (0.0012 → "0.12%") */
export function formatRatio(value: number): string {
  if (!Number.isFinite(value)) return "0.0%";
  return `${(value * 100).toFixed(1)}%`;
}
