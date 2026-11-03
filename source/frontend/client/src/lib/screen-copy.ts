/*
 * Words and sentences the dashboard screens build from server values. Every
 * number is the server's; these helpers only name, format and order them.
 */
import { exchangeLabel } from "./exchanges.ts";
import {
  formatPercentagePointMagnitude,
  formatSharePercent,
  formatSignedPercentagePoints,
} from "./formatters.ts";
import { withParticle } from "./korean-particle.ts";
import { ANALYSIS_WINDOWS } from "./market-share-config.ts";
import { marketShareWindowLabel } from "./market-share-labels.ts";
import type {
  MarketShareAnalysis,
  MarketShareRealtimeFlowStatus,
} from "./market-share-types.ts";
import {
  currentShareSentence,
  selectedWindowFromAnalysis,
} from "./selected-window-presentation.ts";

const KST_OFFSET_MS = 9 * 3_600_000;

/** The KST calendar day of a Unix-millisecond time, e.g. "2026-10-26". */
export function kstDay(ms: number): string {
  return new Date(ms + KST_OFFSET_MS).toISOString().slice(0, 10);
}

/** "09시": the KST hour an official hourly bucket starts at. */
export function kstHour(ms: number): string {
  return `${String(new Date(ms + KST_OFFSET_MS).getUTCHours()).padStart(2, "0")}시`;
}

/** "10/26 09시": an official hour in KST with its day. */
export function kstHourLabel(ms: number): string {
  const kst = new Date(ms + KST_OFFSET_MS);
  return `${kst.getUTCMonth() + 1}/${kst.getUTCDate()} ${kstHour(ms)}`;
}

/** "09:41": a KST minute. */
export function kstMinute(ms: number): string {
  const kst = new Date(ms + KST_OFFSET_MS);
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${pad(kst.getUTCHours())}:${pad(kst.getUTCMinutes())}`;
}

const HOUR_MS = 3_600_000;

/** A share chart's axis time: "09시" for hours, "09:41" for shorter buckets. */
export function bucketTick(ms: number, bucketMS: number): string {
  return bucketMS >= HOUR_MS ? kstHour(ms) : kstMinute(ms);
}

/** A bucket with its day: "10/26 09시", "10/26 09:40–09:50". */
export function bucketLabel(ms: number, bucketMS: number): string {
  if (bucketMS >= HOUR_MS) return kstHourLabel(ms);
  const kst = new Date(ms + KST_OFFSET_MS);
  return `${kst.getUTCMonth() + 1}/${kst.getUTCDate()} ${kstMinute(ms)}–${kstMinute(ms + bucketMS)}`;
}

export function windowLabel(seconds: number): string {
  return (
    ANALYSIS_WINDOWS.find(window => window.seconds === seconds)?.label ??
    marketShareWindowLabel(seconds)
  );
}

/** "고팍스·코빗": names joined the way the screens list them. */
export function joinNames(names: readonly string[]): string {
  return names.filter(Boolean).join("·");
}

export function exchangeNames(
  exchanges: readonly string[] | undefined
): string {
  return joinNames((exchanges ?? []).map(exchangeLabel));
}

export type Tone = "ok" | "warn" | "alert" | "muted";

/**
 * Why a read failed, in plain words. Requests report English details; the
 * screen says what happened and keeps the HTTP status for whoever reports it.
 */
export function readFailureReason(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  if (/[가-힣]/.test(message)) return message; // already written for people
  if (error instanceof Error && error.name === "TimeoutError") {
    return "응답이 늦어 기다리지 않았습니다.";
  }
  const status = /\((\d{3})\)/.exec(message)?.[1];
  if (status === "404") return "서버에 아직 이 자료가 없습니다(404).";
  if (status) return `서버가 이 자료를 내지 못했습니다(${status}).`;
  if (/invalid|incomplete/i.test(message))
    return "서버 응답 형식이 맞지 않습니다.";
  return "서버에 연결하지 못했습니다.";
}

/** A data source's state in words; a code the screens do not know says so. */
export function sourceStatusLabel(status: string | undefined): string {
  switch (status) {
    case "LIVE":
      return "정상";
    case "STALE":
      return "갱신 지연";
    case "PARTIAL":
      return "일부만 수신";
    case "WARMING":
      return "준비 중";
    case "GAP":
      return "빠진 시간 있음";
    case "UNAVAILABLE":
      return "받지 못함";
    default:
      return "상태 확인 필요";
  }
}

/**
 * How the official 24 hours were received: "5/5곳 수신", with the state when
 * it is not live (a summary the collector stopped updating keeps its counts
 * but turns STALE). ok only when it is live from every exchange.
 */
export function official24hReceipt(
  official:
    | {
        status: string;
        ready_exchange_count: number;
        expected_exchange_count: number;
      }
    | undefined
): { label: string; ok: boolean } {
  if (!official) return { label: "자료 없음", ok: false };
  const live = official.status === "LIVE";
  const count = `${official.ready_exchange_count}/${official.expected_exchange_count}곳 수신`;
  return {
    label: live ? count : `${sourceStatusLabel(official.status)} · ${count}`,
    ok:
      live &&
      official.ready_exchange_count === official.expected_exchange_count,
  };
}

/** The calculator's trade ledger state in words. */
export function ledgerStatusLabel(status: string | undefined): string {
  switch (status) {
    case "BALANCED":
      return "맞음";
    case "COLLECTOR_LAG":
      return "수집 서버보다 늦음";
    case "MISMATCH":
      return "건수가 맞지 않음";
    default:
      return "상태 확인 필요";
  }
}

/** The one-second flow's state, as the live strip names it. */
export function flowStatus(
  status: MarketShareRealtimeFlowStatus | string | undefined
): {
  label: string;
  tone: Tone;
} {
  switch (status) {
    case "NORMAL":
      return { label: "평소", tone: "ok" };
    case "WATCH":
      return { label: "관찰", tone: "warn" };
    case "ALERT":
      return { label: "경고", tone: "alert" };
    case "DEGRADED":
      return { label: "자료 부족", tone: "warn" };
    default:
      return { label: "준비 중", tone: "muted" };
  }
}

/** Why the server left a change comparison out. */
export function unavailableReasonLabel(reason: string | undefined): string {
  switch (reason) {
    case "COMPARISON_WINDOW_INCOMPLETE":
      return "지금이나 직전 구간에 빠진 시간이 있어 비교하지 않았습니다";
    case "CURRENT_UNRESOLVED_FX":
    case "PREVIOUS_UNRESOLVED_FX":
      return "원화로 바꾸지 못한 체결이 있어 비교하지 않았습니다";
    case "NO_CURRENT_DOMESTIC_VOLUME":
      return "지금 구간에 국내 거래가 없습니다";
    case "NO_PREVIOUS_DOMESTIC_VOLUME":
      return "직전 구간에 국내 거래가 없습니다";
    case "CROSS_WINDOW_MARKET_IDENTITY_CONFLICT":
      return "두 구간의 코인 이름이 맞지 않아 비교하지 않았습니다";
    case "CURRENT_DOMESTIC_INPUT_INVALID":
    case "PREVIOUS_DOMESTIC_INPUT_INVALID":
      return "받은 자료가 올바르지 않아 비교하지 않았습니다";
    default:
      return "비교할 수 없는 상태입니다";
  }
}

export interface ComparisonStatus {
  status?: string;
  provisional_reason?: string;
  unavailable_reason?: string;
  excluded_exchanges?: readonly string[];
}

/**
 * A comparison with the previous window may be shown only when the server
 * confirmed it, or made it without some exchanges. Otherwise its numbers may
 * be leftovers of an incomplete comparison.
 */
export function comparisonUsable(
  change: ComparisonStatus | null | undefined
): boolean {
  return change?.status === "CONFIRMED" || change?.status === "PROVISIONAL";
}

/**
 * "고팍스 제외" for a comparison made without some exchanges, "관측범위기준"
 * for one made over the seconds observed; "" otherwise.
 */
export function comparisonScope(
  change: ComparisonStatus | null | undefined
): string {
  if (change?.status !== "PROVISIONAL") return "";
  return change.provisional_reason === "OBSERVED_RANGE"
    ? "관측범위기준"
    : `${exchangeNames(change.excluded_exchanges)} 제외`;
}

/**
 * What a reader needs to know before trusting a comparison: nothing when it
 * is complete, which exchanges it leaves out when provisional, and why it is
 * missing otherwise.
 */
export function comparisonNote(
  change: ComparisonStatus | null | undefined
): string {
  if (!change) return "변화 자료를 아직 받지 못했습니다";
  if (change.status === "CONFIRMED") return "";
  if (change.status === "PROVISIONAL") {
    const names = exchangeNames(change.excluded_exchanges);
    if (change.provisional_reason === "OBSERVED_RANGE") {
      return "빠진 시간은 빼고 관측한 시간끼리 비교했습니다(관측범위기준)";
    }
    if (change.provisional_reason === "MINOR_OBSERVATION_GAP") {
      return `${withParticle(names, "을/를")} 빼고 비교했습니다. 끊김이 있었고 거래 비중이 0.1% 이하인 곳입니다`;
    }
    return `지금 연결이 끊긴 ${withParticle(names, "을/를")} 빼고 비교했습니다`;
  }
  return unavailableReasonLabel(change.unavailable_reason);
}

/**
 * comparisonNote for one window: 24 hours is never compared with the 24 hours
 * before it, whatever reason the empty comparison carries.
 */
export function windowComparisonNote(
  windowSec: number,
  change: ComparisonStatus | null | undefined
): string {
  return windowSec === 86_400
    ? "24시간은 직전 24시간과 비교하지 않습니다"
    : comparisonNote(change);
}

/** "올랐습니다" / "내렸습니다" / "그대로입니다" for a signed change. */
export function movedVerb(value: number): string {
  if (value > 0) return "올랐습니다";
  if (value < 0) return "내렸습니다";
  return "그대로입니다";
}

export interface ShareBridge extends ComparisonStatus {
  share_change_pp: number | null;
  bithumb_direct_effect_pp: number | null;
  competitor_effect_pp: number | null;
  competitor_volume_change_krw?: number | null;
}

/**
 * "10분 변화 +6.55%p는 빗썸 거래금액 효과 +4.51%p와 경쟁 거래소 거래 감소
 * 효과 +2.04%p로 나뉩니다." The competitor effect is named by what
 * competitors did, and only when the server says what they did.
 */
export function shareBridgeSentence(
  window: string,
  bridge: ShareBridge
): string {
  const {
    share_change_pp: change,
    bithumb_direct_effect_pp: direct,
    competitor_effect_pp: competitor,
  } = bridge;
  if (
    !comparisonUsable(bridge) ||
    change === null ||
    direct === null ||
    competitor === null
  )
    return "";
  const moved = bridge.competitor_volume_change_krw;
  const competitorPart =
    typeof moved === "number" && moved !== 0
      ? `경쟁 거래소 거래 ${moved < 0 ? "감소" : "증가"} 효과`
      : "경쟁 거래소 효과";
  const scope = comparisonScope(bridge);
  return `${window} 변화${scope ? `(${scope})` : ""} ${formatSignedPercentagePoints(change)}는 빗썸 거래금액 효과 ${formatSignedPercentagePoints(direct)}와 ${competitorPart} ${formatSignedPercentagePoints(competitor)}로 나뉩니다.`;
}

/**
 * The midpoint split read in words: coin mix is where trading moved between
 * coins; capture is Bithumb's part within the same coins.
 */
export function mixCaptureSentence(
  mix: number | null | undefined,
  capture: number | null | undefined
): string {
  if (typeof mix !== "number" || typeof capture !== "number") return "";
  const mixPart =
    mix === 0
      ? "코인 사이 거래 이동은 점유율을 바꾸지 않았고"
      : `빗썸이 강한 코인${mix > 0 ? "으로 거래가 몰려" : "에서 거래가 빠져"} ${formatPercentagePointMagnitude(mix)} ${mix > 0 ? "올렸고" : "내렸고"}`;
  const capturePart =
    capture === 0
      ? "같은 코인 안 빗썸 비중은 그대로였습니다."
      : `같은 코인 안에서 빗썸이 가져간 비중은 ${formatPercentagePointMagnitude(capture)}만큼 ${capture > 0 ? "늘었습니다" : "줄었습니다"}.`;
  return `${mixPart} ${capturePart}`;
}

export interface PhaseEvent {
  direction: string;
  started_at_ms: number;
  baseline_share_pct: number;
  current_share_pct: number;
  change_pp: number;
}

/** "새벽 2시 18.24%에서 20시 21.98%로 3.74%p 올랐습니다" style phase line. */
export function phaseSentence(
  event: PhaseEvent | null | undefined,
  hourLabel: (ms: number) => string
): string {
  if (!event) return "";
  return `정시 3곳 점유율은 ${hourLabel(event.started_at_ms)} ${formatSharePercent(event.baseline_share_pct)}에서 지금 ${formatSharePercent(event.current_share_pct)}로 ${formatPercentagePointMagnitude(event.change_pp)} ${event.direction === "DOWN" ? "내렸습니다" : "올랐습니다"}.`;
}

export interface WindowShare {
  /** The window the server computed, e.g. "10분". */
  window: string;
  /** "실시간 5곳", or "공식 24시간" when the 24-hour window is the official ticker. */
  source: string;
  official: boolean;
  /** The server's share, or null when it may not be shown. */
  share: number | null;
  /** "관측범위기준(웹소켓)" for the share of a partly observed window. */
  basis: string;
  /** The observed span of an incomplete window, or why the share is missing. */
  note: string;
}

/** The selected window's share as every screen shows it. */
export function windowShare(
  analysis: Partial<MarketShareAnalysis> | null | undefined,
  fallbackWindowSec: number
): WindowShare {
  const presentation = selectedWindowFromAnalysis(analysis ?? {});
  const official =
    analysis?.context?.data_source?.official_24h_ticker_used === true;
  return {
    window: windowLabel(analysis?.window_sec ?? fallbackWindowSec),
    source: official ? "공식 24시간" : "실시간 5곳",
    official,
    share: presentation.showsShare ? presentation.sharePct : null,
    basis: presentation.shareBasisLabel,
    note: presentation.showsShare
      ? presentation.amountsQualifier
      : presentation.shareReason,
  };
}

/**
 * "최근 10분(실시간 5곳) 빗썸 점유율은 27.02%입니다. 직전 10분보다 6.55%p
 * 높았고, PYBOBO가 +5.12%p를 만들었습니다." The share is the window's own; a
 * comparison made without some exchanges says so, since its numbers are
 * those exchanges' alone.
 */
export function windowLeadSentence(
  share: WindowShare,
  change:
    (ComparisonStatus & { share_change_pp: number | null }) | null | undefined,
  topAsset: { asset: string; contribution_pp: number } | null | undefined,
  /** The caller must show the partial status and its explanation beside the sentence. */
  { basisInHelp = false }: { basisInHelp?: boolean } = {}
): string {
  const head = currentShareSentence({
    windowLabel: `${share.window}(${share.source})`,
    sharePercent: share.share === null ? null : formatSharePercent(share.share),
    basisLabel: basisInHelp ? "" : share.basis,
  });
  if (!comparisonUsable(change) || change?.share_change_pp == null) return head;
  const delta = change.share_change_pp;
  const scope =
    change.status === "PROVISIONAL"
      ? `${withParticle(exchangeNames(change.excluded_exchanges), "을/를")} 뺀 거래소끼리 비교하면 `
      : "";
  const moved = delta > 0 ? "높았" : delta < 0 ? "낮았" : "같았";
  const compared = `${scope}직전 ${share.window}보다 ${formatPercentagePointMagnitude(delta)} ${moved}`;
  const asset =
    topAsset && topAsset.contribution_pp !== 0
      ? `${withParticle(topAsset.asset, "이/가")} ${formatSignedPercentagePoints(topAsset.contribution_pp)}를 만들었습니다.`
      : "";
  return asset
    ? `${head} ${compared}고, ${asset}`
    : `${head} ${compared}습니다.`;
}

/** "1시간 변화 +0.39%p는 업비트 거래금액이 441.68억 줄어든 효과(+0.33%p)가 가장 컸습니다." */
export function hourDriverSentence(
  shareDelta: number | null | undefined,
  driver:
    | { exchange: string; volume_change_krw: number; contribution_pp: number }
    | null
    | undefined,
  formatMoney: (value: number) => string
): string {
  if (typeof shareDelta !== "number" || !driver) return "";
  const moved = driver.volume_change_krw < 0 ? "줄어든" : "늘어난";
  return `1시간 변화 ${formatSignedPercentagePoints(shareDelta)}는 ${exchangeLabel(driver.exchange)} 거래금액이 ${formatMoney(Math.abs(driver.volume_change_krw))} ${moved} 효과(${formatSignedPercentagePoints(driver.contribution_pp)})가 가장 컸습니다.`;
}

/** Compares two server shares in words without subtracting them. */
export function depthSentence(
  tradeShare: number | null | undefined,
  depthShare: number | null | undefined,
  countShare: number | null | undefined
): string {
  if (typeof tradeShare !== "number" || typeof depthShare !== "number")
    return "";
  const relation =
    depthShare > tradeShare
      ? "높습니다"
      : depthShare < tradeShare
        ? "낮습니다"
        : "같습니다";
  const count =
    typeof countShare === "number"
      ? ` 체결 건수 점유율은 ${formatSharePercent(countShare)}입니다.`
      : "";
  return `거래금액 점유율 ${formatSharePercent(tradeShare)}보다 호가 금액 점유율 ${formatSharePercent(depthShare)}가 ${relation}.${count}`;
}

const DRIVERS: Record<string, { short: string; sentence: string }> = {
  BITHUMB_VOLUME_LED: {
    short: "빗썸 거래금액",
    sentence: "빗썸 거래금액이 변화를 이끌었습니다.",
  },
  COMPETITOR_VOLUME_LED: {
    short: "경쟁 거래소 거래",
    sentence: "경쟁 거래소 거래가 변화를 이끌었습니다.",
  },
  BALANCED: { short: "두 효과가 비슷함", sentence: "두 효과가 비슷했습니다." },
  NO_CHANGE: { short: "변화 없음", sentence: "변화가 없었습니다." },
};

/**
 * What led a window's change, by the server's primary driver code: a short
 * form for a table column and a sentence for prose.
 */
export function primaryDriver(driver: string | undefined): {
  short: string;
  sentence: string;
} {
  return (
    (driver ? DRIVERS[driver] : undefined) ?? {
      short: "비교 불가",
      sentence: "무엇이 변화를 이끌었는지 비교할 수 없습니다.",
    }
  );
}

export interface WindowCoinLists {
  window_sec: number;
  rising_assets: ReadonlyArray<{ asset: string; contribution_pp: number }>;
  falling_assets: ReadonlyArray<{ asset: string; contribution_pp: number }>;
}

export interface RecurringCoin {
  asset: string;
  windows: Array<{ window_sec: number; contribution_pp: number }>;
}

/**
 * Coins that lead more than one window, most windows first. It only groups
 * the server's per-window lists; no contribution is combined.
 */
export function recurringCoins(
  windows: readonly WindowCoinLists[]
): RecurringCoin[] {
  const byAsset = new Map<string, RecurringCoin>();
  for (const window of windows) {
    for (const row of [...window.rising_assets, ...window.falling_assets]) {
      const entry = byAsset.get(row.asset) ?? { asset: row.asset, windows: [] };
      entry.windows.push({
        window_sec: window.window_sec,
        contribution_pp: row.contribution_pp,
      });
      byAsset.set(row.asset, entry);
    }
  }
  return [...byAsset.values()]
    .filter(entry => entry.windows.length > 1)
    .sort(
      (left, right) =>
        right.windows.length - left.windows.length ||
        left.asset.localeCompare(right.asset)
    );
}

/** The window whose share fell the most, if any fell. */
export function largestFall<T extends { share_change_pp: number | null }>(
  windows: readonly T[]
): T | null {
  let lowest: T | null = null;
  for (const window of windows) {
    if (window.share_change_pp === null || window.share_change_pp >= 0)
      continue;
    if (!lowest || window.share_change_pp < (lowest.share_change_pp ?? 0))
      lowest = window;
  }
  return lowest;
}
