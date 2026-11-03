/**
 * What the selected-window (선택 구간) views may show for one analysis.
 *
 * The server withholds a share whenever its denominator is not complete: the
 * window was only partly observed, or some trades could not be converted to
 * KRW. It still sends the amounts it did observe. This module decides how
 * those observed amounts and the missing share are described, using only
 * values the server supplied. The browser never divides the two amounts to
 * produce a share, never fills an unobserved span, and never turns a missing
 * value into zero.
 */

import { formatDuration } from "./formatters.ts";
import type { MarketShareAnalysis } from "./market-share-types.ts";

/**
 * Names the basis of a share computed only from what the WebSocket stream
 * observed inside a window that was not fully covered. Product text, shown
 * wherever such a share is shown so it is never read as the window's share.
 */
export const OBSERVED_RANGE_BASIS_LABEL = "관측범위기준(웹소켓)";
const OBSERVED_RANGE_BASIS_NOTE = `이 점유율은 ${OBSERVED_RANGE_BASIS_LABEL}이며 구간 전체의 점유율과 다를 수 있습니다.`;

export type ShareBasis = "WINDOW" | "OBSERVED_RANGE";

export type SelectedWindowStatus =
  | "CONFIRMED"
  | "PROVISIONAL"
  | "PARTIAL"
  | "UNAVAILABLE"
  | (string & {});

export interface SelectedWindowInput {
  status: SelectedWindowStatus;
  coverageSec: number;
  requiredSec: number;
  /** Trades whose KRW conversion the server could not resolve. */
  unresolvedFxCount: number;
  /** Closed official minutes replaced the missing live span. */
  officialMinuteSupplementUsed: boolean;
  domesticVolumeKrw: number;
  bithumbVolumeKrw: number;
  /** The share exactly as the server published it. */
  sharePct: number | null;
}

export interface SelectedWindowPresentation {
  /** Observed amounts are real values and may be displayed. */
  showsAmounts: boolean;
  /** The amounts cover the whole requested window. */
  amountsAreComplete: boolean;
  /** Received and missing span for an incomplete window; empty when complete. */
  amountsQualifier: string;
  /** The server-published share may be displayed. */
  showsShare: boolean;
  sharePct: number | null;
  /**
   * What the shown share covers: the requested window, or only the observed
   * part of it. null when no share is shown.
   */
  shareBasis: ShareBasis | null;
  /** Names the basis of an observed-range share; empty otherwise. */
  shareBasisLabel: string;
  /** One sentence saying what an observed-range share is; empty otherwise. */
  shareBasisNote: string;
  /** Why the share is absent; empty when it is shown. */
  shareReason: string;
}

function finite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

/**
 * Seconds of the selected window that were not observed.
 *
 * The server does not send context.data_source.gap_seconds. It sends the
 * required and the observed seconds, so the span is their difference. Returns
 * null when either is not a finite number, so a missing span is shown as
 * unknown rather than as zero or as "no gap recorded".
 */
export function selectedWindowGapSeconds(
  requiredSec: number | null | undefined,
  coverageSec: number | null | undefined
): number | null {
  if (!finite(requiredSec) || !finite(coverageSec)) return null;
  return Math.max(0, Math.floor(requiredSec) - Math.floor(coverageSec));
}

/** "일부 관측 · 관측 7분 / 필요 10분 · 누락 3분" for a partly observed window. */
export function observedSpanNote(observedSec: number, requiredSec: number): string {
  const missingSec = Math.max(0, requiredSec - observedSec);
  return `일부 관측 · 관측 ${formatDuration(observedSec)} / 필요 ${formatDuration(requiredSec)} · 누락 ${formatDuration(missingSec)}`;
}

const NO_BASIS = { shareBasis: null, shareBasisLabel: "", shareBasisNote: "" } as const;

export function selectedWindowPresentation(
  input: SelectedWindowInput
): SelectedWindowPresentation {
  const unresolved = finite(input.unresolvedFxCount)
    ? Math.max(0, Math.floor(input.unresolvedFxCount))
    : 0;
  const amountsFinite =
    finite(input.domesticVolumeKrw) && finite(input.bithumbVolumeKrw);
  const observedSec = finite(input.coverageSec) ? Math.max(0, input.coverageSec) : 0;
  const requiredSec = finite(input.requiredSec) ? Math.max(0, input.requiredSec) : 0;

  // Only statuses the server is known to produce open anything. This is an
  // allowlist on purpose: a status this bundle does not know (a newer server,
  // a stale tab) says nothing about how much of the window was observed, so it
  // shows no amount and is not described as a partial observation.
  const known =
    input.status === "CONFIRMED" ||
    input.status === "PROVISIONAL" ||
    input.status === "PARTIAL";
  if (!known && input.status !== "UNAVAILABLE") {
    return {
      showsAmounts: false,
      amountsAreComplete: false,
      amountsQualifier: "",
      showsShare: false,
      sharePct: null,
      ...NO_BASIS,
      shareReason: "이 구간의 자료 상태를 확인할 수 없어 표시하지 않습니다.",
    };
  }
  if (!known || !amountsFinite) {
    return {
      showsAmounts: false,
      amountsAreComplete: false,
      amountsQualifier: "",
      showsShare: false,
      sharePct: null,
      ...NO_BASIS,
      // UNAVAILABLE means the window has no observations. A frame that simply
      // carries no amounts (no summary section) says nothing about that.
      shareReason:
        input.status === "UNAVAILABLE"
          ? "선택한 구간의 관측값이 없어 표시할 수 없습니다."
          : "이 화면의 자료에는 선택 구간 금액이 포함되어 있지 않습니다.",
    };
  }

  const complete = input.status === "CONFIRMED" || input.status === "PROVISIONAL";
  const supplemented =
    input.status === "PARTIAL" && input.officialMinuteSupplementUsed === true;
  const partial = input.status === "PARTIAL" && !supplemented;

  // A partial window with nothing observed has no amount to describe.
  if (partial && observedSec <= 0) {
    return {
      showsAmounts: false,
      amountsAreComplete: false,
      amountsQualifier: "",
      showsShare: false,
      sharePct: null,
      ...NO_BASIS,
      shareReason: "선택한 구간의 관측값이 없어 표시할 수 없습니다.",
    };
  }

  const amountsAreComplete = complete || supplemented;
  const amountsQualifier = amountsAreComplete
    ? ""
    : observedSpanNote(observedSec, requiredSec);

  const published = finite(input.sharePct) ? input.sharePct : null;
  if (published !== null && amountsAreComplete) {
    return {
      showsAmounts: true,
      amountsAreComplete,
      amountsQualifier,
      showsShare: true,
      sharePct: published,
      shareBasis: "WINDOW",
      shareBasisLabel: "",
      shareBasisNote: "",
      shareReason: "",
    };
  }
  // Product decision: the share the server publishes for a partly observed
  // window is shown, named by its basis. It is never derived here, and a
  // conversion that did not finish still withholds it, because the
  // denominator is incomplete even inside the observed range.
  if (published !== null && partial && unresolved === 0) {
    return {
      showsAmounts: true,
      amountsAreComplete,
      amountsQualifier,
      showsShare: true,
      sharePct: published,
      shareBasis: "OBSERVED_RANGE",
      shareBasisLabel: OBSERVED_RANGE_BASIS_LABEL,
      shareBasisNote: OBSERVED_RANGE_BASIS_NOTE,
      shareReason: "",
    };
  }

  let shareReason: string;
  if (unresolved > 0) {
    shareReason = `원화 환산이 끝나지 않은 체결 ${unresolved.toLocaleString("ko-KR")}건이 있어 점유율을 계산하지 않았습니다.`;
  } else if (partial) {
    shareReason = "선택한 구간을 일부만 관측해 서버가 점유율을 내지 않았습니다.";
  } else {
    shareReason = "이 구간의 점유율은 아직 확정되지 않았습니다.";
  }

  return {
    showsAmounts: true,
    amountsAreComplete,
    amountsQualifier,
    showsShare: false,
    sharePct: null,
    ...NO_BASIS,
    shareReason,
  };
}

/**
 * The same decision, taken from an analysis frame.
 *
 * Not every frame carries every section. The market intelligence page receives
 * view=full, which has a context but no summary, and a frame can also arrive
 * without data_source. A missing section is not an observation, so it can
 * neither throw nor be read as zero: the amounts are absent and the window
 * state is reported as unconfirmed.
 */
export function selectedWindowFromAnalysis(
  analysis: Partial<Pick<MarketShareAnalysis, "context" | "summary">>
): SelectedWindowPresentation {
  const context = analysis?.context;
  const summary = analysis?.summary;
  const presentation = selectedWindowPresentation({
    status: context?.selected_window_status ?? "",
    coverageSec: context?.selected_window_coverage_sec ?? Number.NaN,
    requiredSec: context?.selected_window_required_sec ?? Number.NaN,
    unresolvedFxCount: summary?.domestic_unresolved_fx_count ?? 0,
    // Compact frames can omit data_source; absence is not a supplement.
    officialMinuteSupplementUsed:
      context?.data_source?.official_minute_supplement_used === true,
    domesticVolumeKrw: summary?.domestic_volume_krw ?? Number.NaN,
    bithumbVolumeKrw: summary?.bithumb_volume_krw ?? Number.NaN,
    sharePct: summary?.bithumb_share_pct ?? null,
  });
  // No frame yet is a window still loading, not one in doubt.
  return context
    ? presentation
    : { ...presentation, shareReason: "이 구간 자료를 받는 중입니다." };
}

/**
 * The sentence that says what happened to the span that was not observed.
 * A span the server did not supply is left out of the sentence instead of
 * being printed as a dash, and a fully observed window has nothing to say.
 */
export function unobservedSpanSentence(input: {
  gapSec: number | null;
  supplemented: boolean;
}): string {
  if (input.gapSec !== null && input.gapSec <= 0) return "";
  const span = input.gapSec === null ? "" : `${formatDuration(input.gapSec)} `;
  if (input.supplemented) {
    return input.gapSec === null
      ? " 실시간으로 확인하지 못한 구간은 공식 분 단위 자료로 확인된 거래금액을 합계에 보완했습니다."
      : ` 실시간으로 확인하지 못한 ${span}동안 공식 분 단위 자료로 확인된 거래금액은 합계에 보완했습니다.`;
  }
  return input.gapSec === null
    ? " 확인하지 못한 구간은 합계에 0원으로 넣지 않았습니다."
    : ` 확인하지 못한 ${span}구간은 합계에 0원으로 넣지 않았습니다.`;
}

/**
 * The lead sentence of the overview. A share that is not shown is not the
 * subject of "입니다": the sentence says the share is not shown instead of
 * finishing a statement about a dash.
 */
export function currentShareSentence(input: {
  windowLabel: string;
  sharePercent: string | null;
  /** Names the basis of a share that covers only the observed range. */
  basisLabel?: string;
}): string {
  if (input.sharePercent === null) {
    return `최근 ${input.windowLabel} 빗썸 점유율은 표시하지 않습니다.`;
  }
  const sentence = `최근 ${input.windowLabel} 빗썸 점유율은 ${input.sharePercent}입니다.`;
  return input.basisLabel
    ? `${sentence} 이 점유율은 ${input.basisLabel}이며 구간 전체의 점유율과 다를 수 있습니다.`
    : sentence;
}

/**
 * The aggregation version shown in the report footer. The server sends it as
 * context.aggregation_version; a version that is missing or not a positive
 * integer yields no label at all rather than a dangling separator.
 */
export function analysisVersionLabel(version: unknown): string {
  return typeof version === "number" && Number.isInteger(version) && version > 0
    ? `계산 번호 ${version.toLocaleString("ko-KR")}`
    : "";
}
