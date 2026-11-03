import {
  Fragment,
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type CSSProperties,
  type Dispatch,
  type SetStateAction,
} from "react";
import {
  Activity,
  AlertTriangle,
  ArrowDown,
  ArrowDownRight,
  ArrowUp,
  ArrowUpRight,
  ArrowUpDown,
  BarChart3,
  CheckCircle2,
  ChevronLeft,
  ChevronDown,
  ChevronRight,
  CircleDot,
  Coins,
  FileText,
  Gauge,
  History,
  Layers3,
  Radio,
  RadioTower,
  Search,
  X,
} from "lucide-react";
import { KpiCard } from "@/components/common/KpiCard";
import { DeferredRender } from "@/components/common/DeferredRender";
import { InlineHelp } from "@/components/common/InlineHelp";
import { MobileHelp } from "@/components/common/MobileHelp";
import { Panel } from "@/components/common/Panel";
import { PageHeader } from "@/components/common/PageHeader";
import { TargetExchangePerspectivePanel } from "@/components/TargetExchangePerspectivePanel";
import { ExchangeBrandLabel } from "@/components/ExchangeBrandLabel";
import { MarketRegistryChanges } from "@/components/MarketRegistryChanges";
import { NumericIntegrityStatus } from "@/components/NumericIntegrityStatus";
import {
  useMarketShare,
  type MarketShareConnection,
  type MarketShareTickStore,
} from "@/hooks/useMarketShare";
import { useAnimatedMetrics } from "@/hooks/useAnimatedMetrics";
import { useLiveOverviewHistory } from "@/hooks/useLiveOverviewHistory";
import { exchangeLabel } from "@/lib/exchanges";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import { buildCoinContributionSummary } from "@/lib/coin-contribution-summary";
import {
  queryCoinTableRows,
  type CoinContributionDirection,
  type CoinTableAsset,
  type CoinListingFilter,
  type CoinNoticeFilter,
  type CoinTableSort,
  type CoinTableSortKey,
  type CoinTradeFilter,
} from "@/lib/coin-table-query";
import {
  formatDuration,
  formatKrwCompact,
  formatKrwMagnitude,
  formatKrwPrice,
  formatSharePercent,
  formatSignedPercent,
  formatPercentagePointMagnitude,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import {
  DOMESTIC_EXCHANGES,
  MARKET_SHARE_WINDOWS,
  MARKET_SHARE_WINDOW_SEC,
  ORDERBOOK_DEPTH_LEVELS,
} from "@/lib/market-share-config";
import { uniqueNoticeSchedules } from "@/lib/notice-time";
import {
  listingHeadlineLabel,
  marketShareDurationLabel,
  marketShareGapLabel,
  marketShareWindowLabel,
  microstructureStatusLabel,
  noticeCategoryLabel,
  noticeScheduleLabel,
  priceValidationStatusLabel,
} from "@/lib/market-share-labels";
import { liveTickExchangeObserved } from "@/lib/live-chart";
import {
  exchangeNoticeAssetLabels,
  isRelevantDomesticNotice,
  isNoticeRelevantToSelectedWindow,
  noticeAnchorTimeMs,
  noticeTimestampLabel,
} from "@/lib/market-share-reaction";
import { assetImpactInterpretation } from "@/lib/asset-impact-copy";
import { observedLiquidityValue } from "@/lib/market-share-liquidity";
import {
  observationGapRecordsEmptyLabel,
  qualityFilterLabel,
  tradeAccountingStatusPresentation,
  tradeAccountingInputPresentation,
  formatQualitySignedCount,
} from "@/lib/market-share-quality-presentation";
import {
  formatShareChangeWords,
  formatRelativeChangePercent,
} from "@/lib/market-numeric-presentation";
import {
  aggregationEngineLabel,
  currentQueueLabel,
  formatCollectorElapsed,
  liveCollectionFallbackLabel,
  observationGapReasonLabels,
  observationGapStatusLabel,
  orderbookAgeBreakdown,
} from "@/lib/market-share-status-presentation";
import { formatObservedWindowCoverage } from "@/lib/observed-window-coverage";
import {
  analysisVersionLabel,
  currentShareSentence,
  selectedWindowFromAnalysis,
  selectedWindowGapSeconds,
  unobservedSpanSentence,
  type SelectedWindowPresentation,
} from "@/lib/selected-window-presentation";
import { minuteRecoveryPresentation } from "@/lib/minute-recovery-presentation";
import {
  MOMENTUM_SOURCE_HELP,
  momentumSourcePresentation,
} from "@/lib/momentum-source-copy";
import { clampPage } from "@/lib/market-share-controls";
import { getOfficialAlignedHistory } from "@/lib/api-client";
import {
  latestSafeOfficialHourMS,
  OFFICIAL_ALIGNED_HISTORY_WINDOW_MS,
  officialHistoryKSTInputValue,
  officialHistorySampleCoverage,
  parseOfficialHistoryKSTInput,
} from "@/lib/market-share-official-history";
import {
  currentTurnoverSpeed,
  hasObservedMomentumRate,
} from "@/lib/market-share-speed";
import {
  shouldShowExchangeRegistry,
  type MarketShareSurface,
} from "@/lib/market-share-view-layout";
import { isDomesticExchange } from "@/lib/target-exchange";
import { withParticle } from "@/lib/korean-particle";
import { noticeSummaryDisplayText } from "@/lib/notice-analysis-status";
import type {
  MarketScope,
  MarketShareAnalysis,
  MarketShareAsset,
  MarketShareChangeAsset,
  MarketShareDriversAnalysis,
  MarketShareQualityAnalysis,
  ExchangeNotice,
  MarketShareListingStructureHeadline,
  MarketShareMomentum,
  MarketShareOfficialAlignedHistoryResponse,
  MarketShareOfficialDomestic24h,
  MarketShareOfficialTicker1mPoint,
  MarketShareReadiness,
  MarketShareVolumeRelationship,
} from "@/lib/market-share-types";

export type MarketShareView =
  "summary" | "overview" | "drivers" | "quality";

const LazyLiveExchangeChart = lazy(
  () => import("@/components/LiveExchangeChart")
);

const NOTICE_SCHEDULE_TIME_FORMAT = new Intl.DateTimeFormat("ko-KR", {
  timeZone: "Asia/Seoul",
  year: "numeric",
  month: "numeric",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});

function noticeScheduleTime(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "시각 정보 없음";
  return NOTICE_SCHEDULE_TIME_FORMAT.format(new Date(value));
}

/** A share outside 0–100% is inconsistent data, not a before/after value. */
function noticeStudySharesConsistent(study: {
  bithumb_pre_share_pct?: number | null;
  bithumb_post_share_pct?: number | null;
}): boolean {
  return [study.bithumb_pre_share_pct, study.bithumb_post_share_pct].every(
    value =>
      typeof value === "number" &&
      Number.isFinite(value) &&
      value >= 0 &&
      value <= 100
  );
}

function noticeStudyStatusLabel(value: string): string {
  if (value === "MATURE") return "반응 확인 완료";
  if (value === "LIVE_ESTIMATE") return "실시간 추정";
  if (value === "INCOMPLETE_CONTIGUOUS_COVERAGE") return "연속 관측 부족";
  return "반응 상태 확인 필요";
}

const QUALITY_STATUS_LABELS: Record<string, string> = {
  HEALTHY: "관측 상태 양호",
  DEGRADED: "저하",
  PARTIAL: "부분 수신",
  PROVISIONAL_FX: "환산값 확인 중",
};

const ORDERBOOK_ACTIVITY_LABELS: Record<string, string> = {
  QUIET: "거래 적음",
  STALE: "호가 지연",
  GAP: "순서 복구",
  UNINITIALIZED: "아직 준비 안 됨",
};

const ORDERBOOK_DIAGNOSIS_LABELS: Record<string, string> = {
  LOW_ACTIVITY_CANDIDATE: "최근 거래 적음",
  SEQUENCE_RECOVERY: "순서 복구 중",
  WS_DISCONNECTED: "실시간 연결 끊김",
  SUBSCRIPTION_ERROR: "실시간 자료 연결 오류",
  SNAPSHOT_OR_SUBSCRIPTION_MISSING: "초기 호가 자료 없음",
  ORDERBOOK_SUBSCRIPTION_MISSING: "실시간 호가 수신 전",
  WS_STREAM_DELAY: "실시간 호가 지연",
  ORDERBOOK_DELAY_WHILE_TRADING: "거래 중 호가 지연",
  ORDERBOOK_STREAM_DELAY: "실시간 호가 지연",
  WS_DELAY_OR_INACTIVE_CHUNK: "실시간 자료 지연·거래 적음",
};

function qualityStatusLabel(value: string): string {
  return QUALITY_STATUS_LABELS[value] ?? "상태 확인 필요";
}

function orderbookActivityLabel(value: string): string {
  return ORDERBOOK_ACTIVITY_LABELS[value] ?? "상태 확인 필요";
}

function orderbookDiagnosisLabel(value: string): string {
  return ORDERBOOK_DIAGNOSIS_LABELS[value] ?? "세부 상태 확인 필요";
}

const VIEW_TITLES: Record<MarketShareView, string> = {
  summary: "시장점유율 요약",
  overview: "빗썸 현물 점유율",
  drivers: "코인별 점유율 기여",
  quality: "데이터 품질",
};

const EVIDENCE_TONES = {
  MEASURED: "border-profit/40 bg-profit/10 text-profit",
  DIAGNOSTIC: "border-gold/40 bg-gold/10 text-gold",
  CONTEXTUAL: "border-info/40 bg-info/10 text-info",
};

const EVIDENCE_LABELS = {
  MEASURED: "확정 계산",
  DIAGNOSTIC: "진단 관측",
  CONTEXTUAL: "시간적 맥락",
};

const POINT_FINDING_KINDS = new Set([
  "GLOBAL_INTEREST_GAP",
  "MOMENTUM",
  "NOTICE_REACTION",
  "SHARE_LOSS_DRIVER",
  "SHARE_GAIN_DRIVER",
  "UPBIT_LISTING_PRESSURE",
]);

type SharePulsePoint = {
  at: number;
  value: number;
};

type SharePulseCoordinate = SharePulsePoint & {
  x: number;
  y: number;
};

const SHARE_PULSE_WINDOW_MS = 60_000;

function smoothPulsePath(points: SharePulseCoordinate[]): string {
  if (points.length < 2) return "";
  if (points.length === 2) {
    return `M ${points[0].x} ${points[0].y} L ${points[1].x} ${points[1].y}`;
  }

  let path = `M ${points[0].x} ${points[0].y}`;
  for (let index = 1; index < points.length - 1; index += 1) {
    const point = points[index];
    const next = points[index + 1];
    path += ` Q ${point.x} ${point.y} ${(point.x + next.x) / 2} ${(point.y + next.y) / 2}`;
  }
  const last = points.at(-1)!;
  return `${path} Q ${last.x} ${last.y} ${last.x} ${last.y}`;
}

function sharePulseGeometry(history: SharePulsePoint[]): {
  segments: SharePulseCoordinate[][];
  latest: SharePulseCoordinate | null;
} {
  if (!history.length) return { segments: [], latest: null };

  const latestAt = history.at(-1)!.at;
  const windowStart = latestAt - SHARE_PULSE_WINDOW_MS;
  const visible = history.filter(point => point.at >= windowStart);
  const values = visible.map(point => point.value);
  const minimum = Math.min(...values);
  const maximum = Math.max(...values);
  const center = (minimum + maximum) / 2;
  const visibleRange = Math.max(maximum - minimum, 0.05);
  const lower = center - visibleRange * 0.7;
  const upper = center + visibleRange * 0.7;
  const coordinates = visible.map(point => ({
    ...point,
    x:
      3 +
      Math.max(
        0,
        Math.min(1, (point.at - windowStart) / SHARE_PULSE_WINDOW_MS)
      ) *
        94,
    y:
      82 -
      Math.max(0, Math.min(1, (point.value - lower) / (upper - lower))) * 64,
  }));
  const segments: SharePulseCoordinate[][] = [];
  for (const point of coordinates) {
    const current = segments.at(-1);
    if (!current) {
      segments.push([point]);
    } else {
      current.push(point);
    }
  }
  return { segments, latest: coordinates.at(-1) ?? null };
}

function money(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return formatKrwCompact(value).replace(/^\+/, "");
}

function orderBookPrice(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return formatKrwPrice(value);
}

function moneyChange(value: number | null | undefined): string {
  return typeof value === "number" && Number.isFinite(value)
    ? formatKrwCompact(value)
    : "—";
}

function percent(value: number | null | undefined, digits = 1): string {
  return formatSharePercent(value, digits);
}

function shareDenominatorAvailable(analysis: MarketShareAnalysis): boolean {
  return (
    selectedWindowValuesAvailable(analysis) &&
    analysis.summary.domestic_volume_krw > 0
  );
}

function selectedWindowValuesAvailable(analysis: MarketShareAnalysis): boolean {
  if (
    ["CONFIRMED", "PROVISIONAL"].includes(
      analysis.context.selected_window_status
    )
  ) {
    return true;
  }
  return (
    analysis.context.selected_window_status === "PARTIAL" &&
    analysis.context.data_source.official_minute_supplement_used === true
  );
}

/**
 * How the selected window's observed amounts and withheld share are described.
 * selectedWindowValuesAvailable still answers "is the whole window usable for
 * comparison"; this answers "which observed values may be shown, and why is the
 * share missing", from server values only.
 */
/** Why an exchange shows no amount: its stream is not received, or nothing to show yet. */
function exchangeAmountPlaceholder(observed: boolean): string {
  return observed ? "거래금액 확인 중" : "체결 수신 확인 대기";
}

/** Unobserved seconds of the selected window, from the fields the server sends. */
function selectedWindowGap(
  analysis: Pick<MarketShareAnalysis, "context">
): number | null {
  return selectedWindowGapSeconds(
    analysis.context.selected_window_required_sec,
    analysis.context.selected_window_coverage_sec
  );
}

function selectedWindowView(
  analysis: MarketShareAnalysis
): SelectedWindowPresentation {
  return selectedWindowFromAnalysis(analysis);
}

function officialSnapshotIsComplete(
  snapshot: MarketShareOfficialDomestic24h | null | undefined
): boolean {
  return Boolean(
    snapshot?.status === "LIVE" &&
    snapshot.ready_exchange_count === snapshot.expected_exchange_count &&
    typeof snapshot.total_volume_krw === "number" &&
    Number.isFinite(snapshot.total_volume_krw) &&
    typeof snapshot.bithumb_share_pct === "number" &&
    Number.isFinite(snapshot.bithumb_share_pct)
  );
}

function points(value: number | null | undefined, digits = 2): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return formatSignedPercentagePoints(value, digits);
}

function signedPercent(value: number | null | undefined, digits = 1): string {
  return formatRelativeChangePercent(value, digits);
}

function bps(value: number | null | undefined): string {
  return value === null || value === undefined || !Number.isFinite(value)
    ? "—"
    : formatSignedPercent(value / 100, 3);
}

function age(value: number | null | undefined): string {
  if (
    value === null ||
    value === undefined ||
    !Number.isFinite(value) ||
    value < 0
  )
    return "—";
  if (value > 0 && value < 0.001) return "1ms 미만";
  return value < 1 ? `${Math.round(value * 1000)}ms` : `${value.toFixed(1)}초`;
}

function maximumAge(
  ...values: Array<number | null | undefined>
): number | null {
  const observed = values.filter(
    (value): value is number => value !== null && value !== undefined
  );
  return observed.length ? Math.max(...observed) : null;
}

function findingMetric(
  finding: MarketShareAnalysis["findings"][number]
): string {
  if (finding.value_bps !== undefined) {
    const value = bps(finding.value_bps);
    return finding.value_krw === undefined
      ? value
      : `${value} · ${money(finding.value_krw)}`;
  }
  if (finding.value_pct === undefined) {
    return finding.value_krw === undefined ? "" : money(finding.value_krw);
  }
  const value = POINT_FINDING_KINDS.has(finding.kind)
    ? points(finding.value_pct)
    : percent(finding.value_pct, 2);
  return finding.value_krw === undefined
    ? value
    : `${value} · ${money(finding.value_krw)}`;
}

function readableFindingCopy(
  finding: MarketShareAnalysis["findings"][number]
): { title: string; detail: string } {
  if (finding.kind === "LISTING_GAP") {
    return {
      title: `${finding.asset ?? "이 코인"}: 빗썸에는 없고 다른 거래소에서 거래됨`,
      detail: `현재 구간에서 빗썸 외 거래소의 ${finding.asset ?? "이 코인"} 거래금액은 ${money(finding.value_krw)}입니다. 상장 필요성이나 거래 원인을 뜻하지는 않습니다.`,
    };
  }
  if (finding.kind === "EXCLUSIVE_LISTING_STRENGTH") {
    return {
      title: `${finding.asset ?? "이 코인"}: 국내에서 빗썸에만 상장`,
      detail: `빗썸에서 발생한 현재 구간 거래금액은 ${money(finding.value_krw)}입니다. 단독 상장이 거래 증가의 원인이라는 뜻은 아닙니다.`,
    };
  }
  if (finding.kind === "CAPTURE_GAP") {
    return {
      title: `${finding.asset ?? "이 코인"} 거래가 다른 거래소에 더 몰림`,
      detail: `국내 ${finding.asset ?? "이 코인"} 거래금액 중 빗썸 비중은 ${percent(finding.value_pct, 1)}이고, 빗썸 외 거래소 거래금액은 ${money(finding.value_krw)}입니다.`,
    };
  }
  return { title: finding.title, detail: finding.detail };
}

function readableBasis(basis?: string[]): string[] | undefined {
  const internalCaptureTerm = ["포착", "률"].join("");
  return basis?.map(item =>
    item
      .replaceAll(internalCaptureTerm, "거래금액 중 빗썸 비중")
      .replaceAll("경쟁 거래금액", "빗썸 외 거래소 거래금액")
      .replaceAll("국내 경쟁 거래금액", "빗썸 외 국내 거래소 거래금액")
  );
}

function readableCheckCopy(
  check: MarketShareAnalysis["expert_brief"]["checks"][number]
): { title: string; detail: string } {
  const value = check.value;
  const magnitude = Math.abs(value ?? 0);
  switch (check.code) {
    case "LIQUIDITY_CONVERSION_GAP":
      return {
        title: "주문장에 쌓인 금액과 실제 거래의 차이",
        detail: `빗썸의 10단계 호가 잔량 비중이 실제 거래금액 점유율보다 ${formatPercentagePointMagnitude(magnitude)} ${value !== null && value >= 0 ? "높습니다" : "낮습니다"}. 주문장 잔량이 실제 거래로 이어졌다는 뜻은 아닙니다.`,
      };
    case "TRADE_SIZE_MIX":
      return {
        title: `빗썸의 건당 거래금액이 다른 거래소보다 ${value !== null && value >= 0 ? "작은 편" : "큰 편"}`,
        detail: `빗썸의 체결 건수 비중이 거래금액 점유율보다 ${formatPercentagePointMagnitude(magnitude)} ${value !== null && value >= 0 ? "높습니다. 건당 거래금액은 다른 거래소보다 작은 편입니다" : "낮습니다. 건당 거래금액은 다른 거래소보다 큰 편입니다"}.`,
      };
    case "AGGRESSOR_FLOW_GAP":
      return {
        title: `빗썸의 매수 주도 체결 비중이 더 ${value !== null && value >= 0 ? "높음" : "낮음"}`,
        detail: `빗썸의 매수 주도 체결 비중이 다른 거래소 합계보다 ${formatPercentagePointMagnitude(magnitude)} ${value !== null && value >= 0 ? "높습니다" : "낮습니다"}. 고객의 순매수 금액을 뜻하지는 않습니다.`,
      };
    case "EXECUTION_COST_GAP":
      return {
        title: `빗썸의 호가 간격이 경쟁 최저보다 ${value !== null && value >= 0 ? "큼" : "작음"}`,
        detail: `빗썸의 대표 매수·매도 가격 차이가 경쟁 거래소의 가장 낮은 값보다 ${diagnosticMetric(magnitude, check.unit)} ${value !== null && value >= 0 ? "큽니다" : "작습니다"}. 현재 확인된 10단계 호가만 비교했습니다.`,
      };
    default:
      return { title: check.title, detail: check.detail };
  }
}

function EvidenceBasis({ basis }: { basis?: string[] }) {
  if (!basis?.length) return null;
  return (
    <details className="summary-evidence-basis">
      <summary>계산 기준 보기</summary>
      <ul>
        {basis.map(item => (
          <li key={item}>{item}</li>
        ))}
      </ul>
    </details>
  );
}

function duration(seconds: number | null | undefined): string {
  return marketShareDurationLabel(seconds);
}

function analysisCoverageQualifier(analysis: MarketShareAnalysis): string {
  const status = analysis.context.selected_window_status;
  if (status === "CONFIRMED") return "전체 관측";
  if (status === "PARTIAL") {
    return (
      `일부만 확인 · 관측 ${duration(analysis.context.selected_window_coverage_sec)} / ` +
      `필요 ${duration(analysis.context.selected_window_required_sec)}`
    );
  }
  if (status === "PROVISIONAL") return "확인 중";
  return "계산 불가";
}

function comparisonCoverageQualifier(analysis: MarketShareAnalysis): string {
  const selectedStatus = analysis.context.selected_window_status;
  if (selectedStatus === "UNAVAILABLE") return "계산 불가";
  if (!analysis.context.comparison_enabled) return "현재값";
  const status = analysis.change_attribution.status;
  if (status === "CONFIRMED") return "비교 관측 충족";
  if (status === "PROVISIONAL") return "확인 중";
  if (selectedStatus !== "CONFIRMED") return "현재 구간 확인 중";
  return "직전 구간 자료 부족";
}

function comparisonValuesAvailable(analysis: MarketShareAnalysis): boolean {
  return ["CONFIRMED", "PROVISIONAL"].includes(
    analysis.change_attribution.status
  );
}

function comparisonScopeLabel(analysis: MarketShareAnalysis): string {
  if (analysis.change_attribution.status !== "PROVISIONAL") {
    return "국내 5개 거래소";
  }
  const excluded = analysis.change_attribution.excluded_exchanges ?? [];
  if (!excluded.length) return "현재 확인된 거래소만 비교";
  return `${excluded.map(exchangeLabel).join("·")} 제외 비교`;
}

function exchangeTradeStreamReady(
  analysis: MarketShareAnalysis,
  exchange: string
): boolean {
  const collector = analysis.collectors[exchange];
  return analysis.context.market_scope === "KRW"
    ? Boolean(collector?.krw_trade_stream_ready)
    : Boolean(collector?.trade_stream_ready);
}

function comparisonUnavailableMessage(analysis: MarketShareAnalysis): string {
  if (analysis.context.selected_window_status === "UNAVAILABLE") {
    return `현재 ${windowLabel(analysis.context.window_sec)} 관측값이 없어 표시할 수 없습니다.`;
  }
  if (!analysis.context.comparison_enabled) {
    return `현재 ${windowLabel(analysis.context.window_sec)} 누적값만 보여주며 직전 ${windowLabel(analysis.context.window_sec)} 비교를 하지 않습니다.`;
  }
  if (analysis.context.selected_window_status !== "CONFIRMED") {
    return "현재 구간 관측이 충분하지 않아 변화 기여를 계산하지 않습니다.";
  }
  const disconnected = DOMESTIC_EXCHANGES.filter(exchange => {
    const collector = analysis.collectors[exchange];
    return analysis.context.market_scope === "KRW"
      ? !collector?.krw_trade_stream_ready
      : !collector?.trade_stream_ready;
  });
  if (disconnected.length) {
    return `${disconnected.map(exchangeLabel).join("·")}의 실시간 체결 연결이 확인되지 않아 직전 구간과 비교하지 않습니다. 지금 확인된 거래소의 현재값만 보여줍니다.`;
  }
  return "직전 동일 구간 자료가 충분하지 않아 변화 기여를 계산하지 않습니다.";
}

function comparisonReferenceLabel(
  analysis: MarketShareAnalysis,
  value: number | null | undefined
): string {
  if (analysis.context.selected_window_status === "UNAVAILABLE") {
    return "비교 불가";
  }
  if (!analysis.context.comparison_enabled) {
    return `직전 ${windowLabel(analysis.context.window_sec)} 비교 안 함`;
  }
  if (
    !comparisonValuesAvailable(analysis) ||
    value === null ||
    value === undefined
  ) {
    return analysis.context.selected_window_status === "CONFIRMED"
      ? "직전 동일 구간 자료 부족"
      : "현재 구간 확인 중";
  }
  return `직전 동일 구간 ${percent(value, 2)}`;
}

function hasSummaryComparison(analysis: MarketShareAnalysis): boolean {
  return (
    selectedWindowValuesAvailable(analysis) &&
    comparisonValuesAvailable(analysis)
  );
}

function windowLabel(seconds: number): string {
  return marketShareWindowLabel(seconds);
}

function windowSpanLabel(seconds: number): string {
  const label = windowLabel(seconds);
  return /[초분]$/.test(label) ? `${label}간` : label;
}

function domesticListingLabel(exchanges: string[]): string {
  const listed = new Set(exchanges);
  const bithumb = listed.has("bithumb");
  const upbit = listed.has("upbit");
  if (bithumb && upbit) return "빗썸·업비트 동시상장";
  if (listed.size === 1 && bithumb) return "빗썸 단독";
  if (listed.size === 1 && upbit) return "업비트 단독";
  if (bithumb) return "빗썸 상장·업비트 미상장";
  if (upbit) return "업비트 상장·빗썸 미상장";
  if (listed.size) return `${exchanges.map(exchangeLabel).join("·")} 상장`;
  return "국내 미상장";
}

function listingLabel(
  asset: Pick<
    MarketShareAsset,
    "exclusive_on_bithumb" | "shared_bithumb_upbit" | "listed_on_bithumb"
  >
): string {
  if (asset.exclusive_on_bithumb) return "빗썸 단독";
  if (asset.shared_bithumb_upbit) return "빗썸·업비트 동시상장";
  if (asset.listed_on_bithumb) return "빗썸·기타 동시상장";
  return "빗썸 미상장";
}

function changeTone(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return "text-muted-foreground";
  }
  if (value > 0) return "text-profit";
  if (value < 0) return "text-loss";
  return "text-muted-foreground";
}

function volumeTransitionSentence(
  subject: string,
  previous: number | null | undefined,
  current: number | null | undefined,
  change: number | null | undefined
): string {
  if (change === null || change === undefined || !Number.isFinite(change)) {
    return `${subject} ${money(previous)}에서 ${withParticle(money(current), "으로/로")} 증감이 확인되지 않았습니다.`;
  }
  if (change > 0)
    return `${subject} ${money(previous)}에서 ${withParticle(money(current), "으로/로")} ${formatKrwMagnitude(change)} 늘었습니다.`;
  if (change < 0)
    return `${subject} ${money(previous)}에서 ${withParticle(money(current), "으로/로")} ${formatKrwMagnitude(change)} 줄었습니다.`;
  return `${subject} ${money(previous)}에서 ${withParticle(money(current), "으로/로")} 변하지 않았습니다.`;
}

function listingStructureVolumeSummary(
  row: MarketShareListingStructureHeadline
): string {
  return (
    `해당 구조의 국내 거래금액은 ${money(row.previous_domestic_volume_krw)} → ${money(row.current_domestic_volume_krw)} (${formatKrwCompact(row.domestic_volume_change_krw)})이고, ` +
    `빗썸은 ${money(row.previous_bithumb_volume_krw)} → ${money(row.current_bithumb_volume_krw)} (${formatKrwCompact(row.bithumb_volume_change_krw)})입니다.`
  );
}

function shareChangeSentence(
  windowSec: number,
  shareChange: number | null | undefined
): string {
  const comparison = `직전 동일 ${windowLabel(windowSec)} 구간`;
  if (
    shareChange === null ||
    shareChange === undefined ||
    !Number.isFinite(shareChange)
  ) {
    return `${comparison} 대비 변화를 계산 중입니다.`;
  }
  if (shareChange > 0) {
    return `${comparison} 대비 ${formatPercentagePointMagnitude(shareChange)} 상승했습니다.`;
  }
  if (shareChange < 0) {
    return `${comparison} 대비 ${formatPercentagePointMagnitude(shareChange)} 하락했습니다.`;
  }
  return `${comparison}과 같습니다.`;
}

function shareVolumeRelationshipSentence(
  relationship: MarketShareVolumeRelationship | undefined
): string | null {
  if (relationship === "SHARE_UP_BOTH_VOLUMES_DOWN") {
    return "빗썸과 국내 전체 거래금액이 모두 줄었지만, 국내 전체 감소 폭이 더 커 점유율은 상승했습니다.";
  }
  if (relationship === "SHARE_DOWN_BOTH_VOLUMES_UP") {
    return "빗썸과 국내 전체 거래금액이 모두 늘었지만, 국내 전체 증가 폭이 더 커 점유율은 하락했습니다.";
  }
  return null;
}

function summaryActionLabel(
  asset: MarketShareAsset | undefined,
  change: MarketShareChangeAsset
): string {
  if (asset?.exclusive_on_bithumb) {
    return change.bithumb_volume_change_krw >= 0
      ? "단독상장 거래금액 증가"
      : "단독상장 거래금액 감소";
  }
  if (!asset?.listed_on_bithumb) {
    return change.domestic_volume_change_krw >= 0
      ? "빗썸 미상장 거래금액 증가"
      : "빗썸 미상장 거래금액 감소";
  }
  if (change.bithumb_capture_change_pp === null)
    return "코인 내 빗썸 비중 자료 부족";
  const marketLabel =
    change.domestic_volume_change_krw > 0
      ? "시장 거래금액 증가"
      : change.domestic_volume_change_krw < 0
        ? "시장 거래금액 감소"
        : "시장 거래금액 변화 없음";
  const captureLabel =
    change.bithumb_capture_change_pp > 0
      ? "코인 내 빗썸 비중 증가"
      : change.bithumb_capture_change_pp < 0
        ? "코인 내 빗썸 비중 감소"
        : "코인 내 빗썸 비중 변화 없음";
  return `${marketLabel} · ${captureLabel}`;
}

const DIAGNOSTIC_STATUS_LABELS: Record<string, string> = {
  STRENGTH: "상대 우위",
  BROAD: "변화 분산",
  CONCENTRATED: "집중 변화",
  CAPACITY: "호가 여력",
  SMALLER_TICKET_MIX: "소액 체결 비중",
  LARGER_TICKET_MIX: "대형 체결 비중",
  BUY_FLOW_HEAVIER: "매수 체결 우위",
  SELL_FLOW_HEAVIER: "매도 체결 우위",
  RISK: "상대 열위",
  ALIGNED: "차이 작음",
  NEUTRAL: "중립",
  DIVERSIFIED: "단독상장 분산",
  OPPORTUNITY: "전환 기회",
  WATCH: "추적 필요",
  UNAVAILABLE: "자료 없음",
};

function diagnosticStatusLabel(status: string): string {
  return DIAGNOSTIC_STATUS_LABELS[status] ?? "관측 근거";
}

function reportTimestamp(valueSeconds: number): string {
  return formatMarketTimeMS(valueSeconds * 1000);
}

function diagnosticMetric(value: number | null, unit: string): string {
  if (value === null || !Number.isFinite(value)) return "—";
  if (unit === "%p") return points(value, 2);
  if (unit === "%") {
    // Spreads below 1% read in the same three decimals as the spread cards.
    const digits = value !== 0 && Math.abs(value) < 1 ? 3 : 1;
    return percent(value, digits);
  }
  if (unit === "bp") return bps(value);
  return `${value.toLocaleString("ko-KR", { maximumFractionDigits: 2 })}${unit}`;
}

function ExchangeIdentity({ exchange }: { exchange: string }) {
  return (
    <span className="market-table-identity">
      <span
        className="market-table-exchange-mark"
        style={{ backgroundColor: `var(--exchange-${exchange})` }}
      />
      <ExchangeBrandLabel exchange={exchange} />
    </span>
  );
}

function coverageLabel(status: string, gapSeconds?: number | null) {
  if (status === "CONFIRMED") return "전체 관측";
  if (status === "PROVISIONAL") return "아직 확인 중인 자료";
  if (status === "PARTIAL")
    return gapSeconds ? `누락 ${duration(gapSeconds)}` : "일부 누락";
  return "자료 확인 대기";
}

function momentumCoverageLabel(row: MarketShareMomentum) {
  const status = row.status || row.coverage_status || "UNAVAILABLE";
  const excluded = row.data_source?.excluded_exchanges ?? [];
  const exclusionLabel = excluded.length
    ? ` · ${excluded.map(exchangeLabel).join("·")} 제외`
    : "";
  if (row.data_source?.official_24h_ticker_used) {
    return `공식 최근 24시간 자료 · 전체 구간${exclusionLabel}`;
  }
  if (row.data_source?.official_minute_supplement_used) {
    if (row.data_source.official_minute_replacement_used) {
      return `공식 1분 자료로 대체 · 체결 상세 미복구${exclusionLabel}`;
    }
    const originalGap = row.gap_seconds ?? 0;
    const interval = row.data_source.official_candle_supplement_interval_sec;
    const candleLabel = interval === 600 ? "공식 10분 자료" : "공식 1분 자료";
    return `${candleLabel}로 보완${originalGap ? ` · 체결 상세 확인 필요 ${duration(originalGap)}` : ""}${exclusionLabel}`;
  }
  return `${coverageLabel(status, row.gap_seconds ?? 0)}${exclusionLabel}`;
}

const READINESS_PHASE_LABELS: Record<MarketShareReadiness["phase"], string> = {
  STOPPED: "수집 중지",
  REGISTRY: "마켓 목록 준비",
  DEGRADED: "수집 품질 확인 필요",
  AGGREGATING: "거래 집계 시작",
  CONNECTING: "실시간 자료 연결 중",
  OBSERVING: "실시간 체결 확인 중",
  READY: "실시간 수집 준비 완료",
};

function readinessPhaseLabel(phase: MarketShareReadiness["phase"]): string {
  return READINESS_PHASE_LABELS[phase] ?? "수집 상태 확인 중";
}

function ExchangeRegistryReadiness({
  registry,
}: {
  registry: MarketShareAnalysis["registry"] | null;
}) {
  const quotePriority = ["KRW", "BTC", "USDT", "USDC"];
  // The five cards are drawn before the registry arrives, with their own
  // "준비 중" values; a one-line note that grew into five cards pushed the
  // rest of the loading view down.
  return (
    <div
      className="exchange-registry-grid grid gap-px border-t border-border bg-border sm:grid-cols-2 xl:grid-cols-6 2xl:grid-cols-5"
      aria-label="거래소별 기준통화 마켓과 고유 심볼"
    >
      {DOMESTIC_EXCHANGES.map(exchange => {
        const venue = registry?.exchanges[exchange];
        const quotes = Object.entries(venue?.quotes ?? {}).sort(
          ([left], [right]) => {
            const leftIndex = quotePriority.indexOf(left);
            const rightIndex = quotePriority.indexOf(right);
            return (
              (leftIndex < 0 ? 99 : leftIndex) -
                (rightIndex < 0 ? 99 : rightIndex) || left.localeCompare(right)
            );
          }
        );
        return (
          <article
            key={exchange}
            className="exchange-registry-card flex min-w-0 flex-col bg-card p-4 xl:col-span-2 2xl:col-span-1"
          >
            <div className="flex items-center justify-between gap-2">
              <strong className="text-sm">
                <ExchangeBrandLabel exchange={exchange} />
              </strong>
              <span className="text-base text-muted-foreground">
                {venue
                  ? `${venue.market_count.toLocaleString("ko-KR")}개 마켓`
                  : "거래쌍 목록 준비 중"}
              </span>
            </div>
            <div className="mt-3 flex flex-wrap gap-1.5 pb-3 text-base">
              {quotes.length ? (
                quotes.map(([quote, count]) => (
                  <span
                    key={quote}
                    className="border border-border bg-inset px-2 py-1"
                  >
                    <b>{quote}</b> 마켓 {count.toLocaleString("ko-KR")}개
                  </span>
                ))
              ) : (
                <span className="text-muted-foreground">
                  기준통화 집계 대기
                </span>
              )}
            </div>
            <p className="mt-auto border-t border-border pt-2 text-base text-muted-foreground">
              중복 제외 코인{" "}
              <strong className="text-foreground">
                {venue?.asset_count.toLocaleString("ko-KR") ?? "—"}개
              </strong>
            </p>
          </article>
        );
      })}
    </div>
  );
}

function StartupReadiness({
  readiness,
  registry,
  error,
  errorSeverity,
}: {
  readiness: MarketShareReadiness | null;
  registry: MarketShareAnalysis["registry"] | null;
  error: string;
  errorSeverity: "error" | "warning" | null;
}) {
  const connectedPct = readiness?.connected_market_pct ?? 0;
  const progress = Math.max(0, Math.min(100, connectedPct));
  return (
    <div
      className="glass-card startup-readiness"
      role="status"
      aria-live="polite"
    >
      <div className="startup-readiness-heading">
        <div>
          <span className="startup-readiness-kicker">실시간 데이터 준비</span>
          <strong>
            {readiness
              ? readinessPhaseLabel(readiness.phase)
              : "실시간 수집 준비 중"}
          </strong>
        </div>
        {readiness && (
          <span className="summary-status">
            {readiness.elapsed_sec.toFixed(1)}초
          </span>
        )}
      </div>
      <div className="startup-readiness-track" aria-hidden="true">
        <span style={{ width: `${progress}%` }} />
      </div>
      <div className="startup-readiness-meta">
        <span>
          {readiness
            ? `${readiness.ready_exchange_count}/${readiness.enabled_exchange_count} 거래소 준비 · 거래소별 전체 현물 구독 상태`
            : "거래소별 전체 마켓 구독을 확인하는 중입니다."}
        </span>
        {readiness?.connected_market_pct !== null &&
          readiness?.connected_market_pct !== undefined && (
            <strong>{readiness.connected_market_pct.toFixed(1)}%</strong>
          )}
      </div>
      {/* The status line sits above the registry lists, which arrive later
          and would otherwise push it down while the page loads. */}
      {error ? (
        <p
          className={`startup-readiness-note ${errorSeverity === "error" ? "text-loss" : "text-gold"}`}
        >
          {error}
        </p>
      ) : readiness?.blocking_exchanges.length ? (
        <p className="startup-readiness-note">
          확인 중: {readiness.blocking_exchanges.map(exchangeLabel).join(" · ")}
        </p>
      ) : (
        <p className="startup-readiness-note">
          분석 결과가 도착하면 화면을 유지한 채 자동으로 표시합니다.
        </p>
      )}
      <ExchangeRegistryReadiness registry={registry} />
      <MarketRegistryChanges />
    </div>
  );
}

function WindowCoverageNotice({
  analysis,
}: {
  analysis: MarketShareQualityAnalysis;
}) {
  const context = analysis.context;
  if (context.window_sec !== 86_400) return null;
  const status = coverageLabel(
    context.selected_window_status,
    selectedWindowGap(analysis)
  );
  return (
    <div className="window-coverage-notice" role="status">
      <Gauge className="h-4 w-4 shrink-0 text-gold" />
      <div className="min-w-0">
        <strong>24시간 데이터 깊이</strong>
        <span>
          최근 {duration(context.selected_window_coverage_sec)} 관측 · 필요{" "}
          {duration(context.selected_window_required_sec)} · {status}
        </span>
      </div>
      <p>
        24시간을 선택하면 최근 24시간 체결만 봅니다. 직전 24시간과 비교하는
        48시간 구간은 사용하지 않습니다.
      </p>
    </div>
  );
}

function ContextHeader({
  windowSec,
  marketScope,
  onWindowChange,
  onScopeChange,
  view,
}: {
  windowSec: number;
  marketScope: MarketScope;
  onWindowChange: (value: number) => void;
  onScopeChange: (value: MarketScope) => void;
  view: MarketShareView;
}) {
  return (
    <PageHeader
      title={VIEW_TITLES[view]}
      icon={
        view === "overview" || view === "summary" ? (
          <BarChart3 className="h-4 w-4 text-primary" />
        ) : undefined
      }
      actions={
        (
          <div className="context-toolbar flex w-full flex-wrap items-center gap-2 2xl:justify-end">
            <div className="segmented-control flex border border-border bg-inset p-1">
              {(
                [
                  ["KRW", "KRW 현물"],
                  ["ALL_DOMESTIC_CONVERTED", "전체 현물·원화로 환산"],
                ] as const
              ).map(([scope, label]) => (
                <button
                  key={scope}
                  type="button"
                  aria-pressed={marketScope === scope}
                  onClick={() => onScopeChange(scope)}
                  className={`segmented-control-item px-3 py-1.5 text-xs ${marketScope === scope ? "segmented-control-item-active bg-background text-foreground" : "text-muted-foreground hover:text-foreground"}`}
                >
                  {label}
                </button>
              ))}
            </div>
            <div className="segmented-control flex border border-border bg-inset p-1">
              {MARKET_SHARE_WINDOWS.map(row => (
                <button
                  key={row.seconds}
                  type="button"
                  aria-pressed={windowSec === row.seconds}
                  onClick={() => onWindowChange(row.seconds)}
                  className={`segmented-control-item px-3 py-1.5 text-xs ${windowSec === row.seconds ? "segmented-control-item-active bg-background text-foreground" : "text-muted-foreground hover:text-foreground"}`}
                >
                  {row.label}
                </button>
              ))}
            </div>
          </div>
        )
      }
    />
  );
}

type BridgeTab = "listing" | "exchange" | "coin" | "quote";
type BridgeRow = {
  key: string;
  label: string;
  value: number;
  detail?: string;
  available?: boolean;
};

const BRIDGE_TAB_LABELS: Record<BridgeTab, string> = {
  listing: "상장 구조",
  exchange: "거래소",
  coin: "코인",
  quote: "기준통화",
};

function ShareBridge({ analysis }: { analysis: MarketShareAnalysis }) {
  const [tab, setTab] = useState<BridgeTab>("listing");
  const attribution = analysis.change_attribution;
  const rows = useMemo<BridgeRow[]>(() => {
    if (tab === "listing") {
      return analysis.expert_brief.listing_segments.map(row => ({
        key: row.segment,
        label: row.label,
        value: row.contribution_pp,
      }));
    }
    if (tab === "exchange") {
      const excluded = new Set(attribution.excluded_exchanges ?? []);
      return attribution.exchanges
        .filter(row => !excluded.has(row.exchange))
        .map(row => ({
          key: row.exchange,
          label: exchangeLabel(row.exchange),
          value: row.contribution_pp,
        }));
    }
    if (tab === "quote") {
      return analysis.expert_brief.quote_comparison.map(row => {
        const available =
          row.current_volume_krw > 0 || row.previous_volume_krw > 0;
        return {
          key: row.quote,
          label: row.quote,
          value: row.contribution_pp,
          available,
          detail: available
            ? `현재 ${money(row.current_volume_krw)} · 직전 ${money(row.previous_volume_krw)} · 코인 거래금액 중 빗썸 비중 ${percent(row.current_bithumb_capture_pct, 1)}`
            : "현재·직전 비교 구간 거래 없음",
        };
      });
    }
    return analysis.expert_brief.major_assets.map(row => ({
      key: row.asset,
      label: row.asset,
      value: row.contribution_pp,
    }));
  }, [analysis.expert_brief, attribution.exchanges, tab]);
  const max = Math.max(0.000001, ...rows.map(row => Math.abs(row.value)));
  const complete = comparisonValuesAvailable(analysis);
  // This branch reads only analysis.summary, so observed amounts and the
  // published share can be shown without a comparison.
  const windowView = selectedWindowView(analysis);
  const currentAvailable = windowView.showsAmounts;
  const shareAvailable = windowView.showsShare;
  return (
    <Panel
      title="점유율 변화 나눠 보기"
      description={`${comparisonScopeLabel(analysis)} 기준으로 같은 점유율 변화를 상장 구조·거래소·기준통화·코인별로 나눠 보여줍니다. 계산 결과이며 원인을 확정하지 않습니다.`}
      className="min-h-[430px]"
      icon={<BarChart3 className="h-4 w-4" />}
      actions={
        <div className="segmented-control flex border border-border bg-inset p-1">
          {(
            [
              ["listing", "상장 구조"],
              ["exchange", "거래소"],
              ["quote", "기준통화"],
              ["coin", "코인"],
            ] as const
          ).map(([key, label]) => (
            <button
              key={key}
              type="button"
              aria-pressed={tab === key}
              onClick={() => setTab(key)}
              className={`segmented-control-item px-3 py-1.5 text-base ${tab === key ? "segmented-control-item-active bg-background text-foreground" : "text-muted-foreground"}`}
            >
              {label}
            </button>
          ))}
        </div>
      }
    >
      {!complete ? (
        <div className="p-4">
          <div className="flex items-center justify-between border-b border-border pb-3 text-xs">
            <span className="text-muted-foreground">현재 선택 구간 점유율</span>
            <span className="font-semibold">
              {shareAvailable ? percent(windowView.sharePct, 2) : "—"}
              {windowView.shareBasisLabel && (
                <small className="ml-2 font-normal text-muted-foreground">
                  {windowView.shareBasisLabel}
                </small>
              )}
            </span>
          </div>
          <div className="flex min-h-64 items-center justify-center p-5 text-center text-xs text-muted-foreground">
            {analysis.context.comparison_enabled
              ? `${BRIDGE_TAB_LABELS[tab]}별 비교를 위한 ${comparisonUnavailableMessage(analysis)}`
              : comparisonUnavailableMessage(analysis)}
          </div>
          <div className="mt-2 flex items-center justify-between border-t border-border pt-3 text-xs">
            <span className="text-muted-foreground">
              현재 관측 구간 국내 거래금액
            </span>
            <span className="font-semibold">
              {currentAvailable
                ? money(analysis.summary.domestic_volume_krw)
                : "—"}
            </span>
          </div>
        </div>
      ) : (
        <div className="p-4">
          <div className="flex items-center justify-between border-b border-border pb-3 text-xs">
            <span className="text-muted-foreground">직전 점유율</span>
            <span className="font-semibold">
              {percent(attribution.previous_share_pct, 2)}
            </span>
          </div>
          <div className="max-h-64 divide-y divide-border/50 overflow-y-auto">
            {rows.map(row => (
              <div
                key={row.key}
                className="grid grid-cols-[minmax(120px,1fr)_2fr_84px] items-center gap-3 py-2.5 text-xs"
              >
                <div className="min-w-0">
                  <span className="block truncate">{row.label}</span>
                  {row.detail && (
                    <span className="summary-bridge-detail">{row.detail}</span>
                  )}
                </div>
                <div className="relative h-2 bg-secondary">
                  {row.available === false ? (
                    <span className="absolute inset-0 text-center text-base leading-6 text-muted-foreground">
                      자료 없음
                    </span>
                  ) : (
                    <div
                      className={`smooth-bar absolute top-0 h-full ${row.value >= 0 ? "left-1/2 bg-profit" : "right-1/2 bg-loss"}`}
                      style={{
                        width: `${Math.min(50, (Math.abs(row.value) / max) * 50)}%`,
                      }}
                    />
                  )}
                </div>
                <span
                  className={`text-right ${row.available === false ? "text-muted-foreground" : changeTone(row.value)}`}
                >
                  {row.available === false ? "—" : points(row.value)}
                </span>
              </div>
            ))}
            {!rows.length && (
              <p className="p-4 text-center text-xs text-muted-foreground">
                기준통화별 비교 데이터 없음
              </p>
            )}
          </div>
          <div className="mt-2 flex items-center justify-between border-t border-border pt-3 text-xs">
            <span className="text-muted-foreground">현재 점유율</span>
            <span className="font-semibold">
              {percent(attribution.current_share_pct, 2)}
            </span>
          </div>
        </div>
      )}
    </Panel>
  );
}

function LiveOverviewCharts({
  analysis,
  tickStore,
  connection,
  marketScope,
  requestedWindowSec,
}: {
  analysis: MarketShareAnalysis;
  tickStore: MarketShareTickStore;
  connection: MarketShareConnection;
  marketScope: MarketScope;
  requestedWindowSec: number;
}) {
  const tick = useSyncExternalStore(
    tickStore.subscribe,
    tickStore.getSnapshot,
    tickStore.getSnapshot
  );
  const history = useLiveOverviewHistory({
    tick,
    connection,
    marketScope,
    requestedWindowSec,
  });

  return (
    <>
      {(["share", "rate"] as const).map(metric => {
        const fallback = (
          <div className="glass-card min-h-[390px]" aria-hidden="true" />
        );
        return (
          <DeferredRender key={metric} fallback={fallback}>
            <Suspense fallback={fallback}>
              <LazyLiveExchangeChart
                history={history}
                analysis={analysis}
                metric={metric}
              />
            </Suspense>
          </DeferredRender>
        );
      })}
    </>
  );
}

function ShareChangeExplanation({
  analysis,
}: {
  analysis: MarketShareAnalysis;
}) {
  const attribution = analysis.change_attribution;
  const complete = comparisonValuesAvailable(analysis);
  const shareChange = attribution.share_change_pp;
  const bithumbVolumeChange = attribution.bithumb_volume_change_krw;
  const competitorVolumeChange = attribution.competitor_volume_change_krw;
  const falling = typeof shareChange === "number" && shareChange < 0;
  const directionAssets =
    shareChange == null
      ? []
      : falling
        ? analysis.expert_brief.negative_assets
        : analysis.expert_brief.positive_assets;
  const directionCoverage = falling
    ? analysis.expert_brief.negative_asset_coverage_pct
    : analysis.expert_brief.positive_asset_coverage_pct;
  const listingSegments = analysis.expert_brief.listing_segments;
  const directEffect = attribution.bithumb_direct_effect_pp;
  const marketEffect =
    attribution.competitor_effect_pp ??
    attribution.domestic_market_size_effect_pp;
  const primaryAxis =
    attribution.primary_driver === "BITHUMB_VOLUME_LED"
      ? "빗썸 자체 거래금액 변화"
      : attribution.primary_driver === "COMPETITOR_VOLUME_LED"
        ? "경쟁 거래소 거래금액 변화"
        : "양쪽 거래금액 변화";
  const evidenceLayers = [
    analysis.evidence_layers.measured,
    analysis.evidence_layers.diagnostic,
    analysis.evidence_layers.contextual,
  ].filter(layer => layer.items.length > 0);
  const evidenceCount = evidenceLayers.reduce(
    (total, layer) => total + layer.items.length,
    0
  );
  const DirectionIcon =
    shareChange == null || shareChange === 0
      ? ArrowUpDown
      : falling
        ? ArrowDownRight
        : ArrowUpRight;
  const directionLabel = falling ? "하락" : "상승";

  return (
    <Panel
      title={
        <span className="inline-help-heading">
          점유율 변화 계산 근거
          <InlineHelp text="점유율 변화를 빗썸과 경쟁 거래소의 거래금액 변화로 나눠 계산합니다. 호가·상장·공지는 원인으로 확정하지 않고 별도 참고 항목으로 표시합니다." />
        </span>
      }
      icon={<Activity className="h-4 w-4" />}
    >
      {!complete ? (
        <div className="flex items-center gap-3 px-5 py-5 text-sm text-muted-foreground">
          <AlertTriangle className="h-4 w-4 shrink-0 text-gold" />
          직전 동일 구간 자료가 부족해 상승·하락 기여를 확정할 수 없습니다.
        </div>
      ) : (
        <>
          <div className="share-cause-hero">
            <div className="share-cause-result">
              <span className="inline-flex border border-profit/40 bg-profit/10 px-2 py-1 text-base text-profit">
                {attribution.status === "PROVISIONAL"
                  ? comparisonScopeLabel(analysis)
                  : "확정 계산"}
              </span>
              <div className="mt-3 flex items-center gap-3">
                <span
                  className={`share-cause-direction ${changeTone(shareChange)}`}
                >
                  <DirectionIcon className="h-5 w-5" />
                </span>
                <strong className={`text-3xl ${changeTone(shareChange)}`}>
                  {points(attribution.share_change_pp, 2)}
                </strong>
              </div>
              <p className="mt-2 text-sm">
                {percent(attribution.previous_share_pct, 2)} →{" "}
                {percent(attribution.current_share_pct, 2)}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                {shareChange == null
                  ? "점유율 변화 값을 확인하고 있습니다."
                  : shareChange === 0
                    ? "상승·하락 기여를 합친 점유율 변화는 없습니다."
                    : attribution.primary_driver === "BITHUMB_VOLUME_LED" ||
                        attribution.primary_driver === "COMPETITOR_VOLUME_LED"
                      ? `${primaryAxis}의 계산상 기여가 더 큽니다.`
                      : "빗썸과 경쟁 거래소의 거래금액 변화가 함께 반영됐습니다."}
              </p>
            </div>

            <div className="share-cause-metrics">
              <div>
                <p>빗썸 거래금액 변화 기여</p>
                <strong className={changeTone(directEffect)}>
                  {points(directEffect)}
                </strong>
                <span>빗썸 거래금액 {moneyChange(bithumbVolumeChange)}</span>
              </div>
              <div>
                <p>경쟁 거래소 거래금액 변화 기여</p>
                <strong className={changeTone(marketEffect)}>
                  {points(marketEffect)}
                </strong>
                <span>
                  경쟁 거래소 합계 {moneyChange(competitorVolumeChange)}
                </span>
              </div>
              <div>
                <p>경쟁 거래소 거래금액 변화</p>
                <strong
                  className={changeTone(
                    typeof competitorVolumeChange === "number"
                      ? -competitorVolumeChange
                      : null
                  )}
                >
                  {moneyChange(competitorVolumeChange)}
                </strong>
                <span>빗썸을 제외한 국내 거래소 합계</span>
              </div>
            </div>
          </div>
          <p className="border-t border-border px-5 py-3 text-xs leading-6 text-muted-foreground">
            {attribution.contribution_note}
          </p>

          <div className="grid border-t border-border 2xl:grid-cols-[minmax(0,3fr)_minmax(340px,2fr)]">
            <section className="min-w-0 border-b border-border 2xl:border-b-0 2xl:border-r">
              <header className="flex flex-wrap items-center justify-between gap-2 border-b border-border bg-inset px-4 py-3">
                <div>
                  <h4 className="text-sm font-semibold">
                    {shareChange == null
                      ? "코인 기여 확인 중"
                      : `${directionLabel}에 기여한 코인`}
                  </h4>
                  <p className="mt-1 text-base text-muted-foreground">
                    이 코인들이 같은 방향 기여 크기의{" "}
                    {percent(directionCoverage, 1)}를 설명합니다.
                  </p>
                </div>
                <span className="text-xs text-muted-foreground">
                  {directionAssets.length.toLocaleString()}개 코인
                </span>
              </header>
              <div className="share-driver-columns hidden md:grid">
                <span>코인·상장 구조</span>
                <span>점유율 기여</span>
                <span>빗썸 증감</span>
                <span>업비트 증감</span>
                <span>경쟁 전체</span>
              </div>
              <div className="max-h-[430px] overflow-y-auto">
                {directionAssets.map(row => (
                  <div key={row.asset} className="share-driver-row">
                    <div className="min-w-0">
                      <div className="flex min-w-0 items-center gap-2">
                        <strong>{row.asset}</strong>
                        <span
                          className="truncate border border-border px-1.5 py-0.5 text-base text-muted-foreground"
                          title={domesticListingLabel(row.domestic_exchanges)}
                        >
                          {domesticListingLabel(row.domestic_exchanges)}
                        </span>
                      </div>
                      <p className="mt-1 text-base text-muted-foreground">
                        코인 거래금액 중 빗썸 비중{" "}
                        {percent(row.previous_bithumb_capture_pct, 1)} →{" "}
                        {percent(row.current_bithumb_capture_pct, 1)}
                      </p>
                    </div>
                    <strong
                      className={`text-right ${changeTone(row.contribution_pp)}`}
                    >
                      <span className="share-driver-mobile-label">기여</span>
                      {points(row.contribution_pp)}
                    </strong>
                    <span
                      className={`text-right ${changeTone(row.bithumb_volume_change_krw)}`}
                    >
                      <span className="share-driver-mobile-label">빗썸</span>
                      {formatKrwCompact(row.bithumb_volume_change_krw)}
                    </span>
                    <span
                      className={`text-right ${changeTone(row.upbit_volume_change_krw)}`}
                    >
                      <span className="share-driver-mobile-label">업비트</span>
                      {formatKrwCompact(row.upbit_volume_change_krw)}
                    </span>
                    <span
                      className={`text-right ${changeTone(row.competitor_volume_change_krw)}`}
                    >
                      <span className="share-driver-mobile-label">
                        경쟁 전체
                      </span>
                      {formatKrwCompact(row.competitor_volume_change_krw)}
                    </span>
                  </div>
                ))}
                {!directionAssets.length && (
                  <p className="p-5 text-center text-xs text-muted-foreground">
                    같은 방향의 코인 기여가 없습니다.
                  </p>
                )}
              </div>
            </section>

            <section className="min-w-0">
              <header className="border-b border-border bg-inset px-4 py-3">
                <h4 className="text-sm font-semibold">상장 구조별 순기여</h4>
                <p className="mt-1 text-base text-muted-foreground">
                  코인별 기여를 상장 관계로 다시 합산한 확정값
                </p>
              </header>
              <div className="divide-y divide-border/60">
                {listingSegments.map(segment => {
                  const bithumbChange = segment.bithumb_volume_change_krw;
                  const domesticChange = segment.domestic_volume_change_krw;
                  return (
                    <div key={segment.segment} className="px-4 py-3">
                      <div className="flex items-center justify-between gap-3">
                        <div className="min-w-0">
                          <strong>{segment.label}</strong>
                          <span className="ml-2 text-base text-muted-foreground">
                            {segment.asset_count.toLocaleString()}개
                          </span>
                        </div>
                        <strong className={changeTone(segment.contribution_pp)}>
                          {points(segment.contribution_pp)}
                        </strong>
                      </div>
                      <p className="mt-1 text-base text-muted-foreground">
                        빗썸 거래금액 증감{" "}
                        <span className={changeTone(bithumbChange)}>
                          {formatKrwCompact(bithumbChange)}
                        </span>
                        <span aria-hidden="true" className="mx-2 text-muted-foreground">·</span>
                        해당 구조의 국내 5개 거래소 합계 증감{" "}
                        <span className={changeTone(domesticChange)}>
                          {formatKrwCompact(domesticChange)}
                        </span>
                      </p>
                    </div>
                  );
                })}
              </div>
            </section>
          </div>

          {evidenceCount > 0 && (
            <details className="border-t border-border">
              <summary className="flex list-none cursor-pointer items-center justify-between gap-3 bg-inset px-4 py-3 text-sm font-semibold">
                <span>보조 진단 전체 {evidenceCount.toLocaleString()}개</span>
                <span className="ui-caption font-normal text-muted-foreground">
                  설명 후보와 시간적 맥락
                </span>
              </summary>
              <div className="max-h-[460px] overflow-y-auto border-t border-border">
                {evidenceLayers.map(layer => (
                  <section
                    key={layer.level}
                    className="border-b border-border last:border-b-0"
                  >
                    <div className="bg-inset px-4 py-2.5">
                      <strong className="text-xs">{layer.label}</strong>
                      <p className="ui-caption mt-1 text-muted-foreground">
                        {layer.description}
                      </p>
                    </div>
                    {layer.items.map(finding => (
                      <article
                        key={`${finding.kind}-${finding.asset ?? finding.title}`}
                        className="grid gap-3 border-t border-border/60 px-4 py-3 md:grid-cols-[minmax(0,1fr)_auto] md:items-start"
                      >
                        <div className="min-w-0">
                          <div className="flex flex-wrap items-center gap-2">
                            <span
                              className={`ui-caption inline-flex border px-2 py-1 ${EVIDENCE_TONES[finding.evidence_level]}`}
                            >
                              {EVIDENCE_LABELS[finding.evidence_level]}
                            </span>
                            <h4 className="text-sm font-semibold">
                              {finding.title}
                            </h4>
                            {finding.asset && (
                              <span className="ui-caption border border-border px-1.5 py-0.5 text-muted-foreground">
                                {finding.asset}
                              </span>
                            )}
                          </div>
                          <p className="mt-1 text-xs leading-6 text-muted-foreground">
                            {finding.detail}
                          </p>
                        </div>
                        <p
                          className={`whitespace-nowrap text-right text-sm font-semibold ${changeTone(finding.value_pct)}`}
                        >
                          {findingMetric(finding)}
                        </p>
                      </article>
                    ))}
                  </section>
                ))}
              </div>
            </details>
          )}
        </>
      )}
    </Panel>
  );
}

function FinalMarketReport({ analysis }: { analysis: MarketShareAnalysis }) {
  const context = analysis.context;
  const attribution = analysis.change_attribution;
  const windowView = selectedWindowView(analysis);
  const hasSelectedData = windowView.showsAmounts;
  const shareAvailable = windowView.showsShare;
  const comparisonComplete = comparisonValuesAvailable(analysis);
  const comparisonIsProvisional = attribution.status === "PROVISIONAL";
  const calculationLabel = comparisonIsProvisional
    ? comparisonScopeLabel(analysis)
    : "확정 계산";
  const excludedComparisonExchanges = new Set(
    attribution.excluded_exchanges ?? []
  );
  const currentByExchange = new Map(
    analysis.exchanges.map(row => [row.exchange, row])
  );
  const comparisonByExchange = new Map(
    attribution.exchanges.map(row => [row.exchange, row])
  );
  const exchangeKeys = Array.from(
    new Set<string>([
      ...DOMESTIC_EXCHANGES,
      ...analysis.exchanges.map(row => row.exchange),
    ])
  );
  const drivers = analysis.expert_brief.major_assets;
  const currentOnlyDiagnosticCodes = new Set([
    "LIQUIDITY_CONVERSION_GAP",
    "TRADE_SIZE_MIX",
    "AGGRESSOR_FLOW_GAP",
    "EXECUTION_COST_GAP",
    "GLOBAL_DEMAND_CONVERSION_GAP",
  ]);
  const diagnostics = analysis.expert_brief.checks.filter(
    check =>
      check.value !== null &&
      (comparisonComplete || currentOnlyDiagnosticCodes.has(check.code))
  );
  const relevantNotices = [...analysis.notices.items]
    .filter(isRelevantDomesticNotice)
    .sort(
      (left, right) =>
        (noticeAnchorTimeMs(right) ?? right.published_ts_ms) -
        (noticeAnchorTimeMs(left) ?? left.published_ts_ms)
    );
  const reportStatus = coverageLabel(
    context.selected_window_status,
    selectedWindowGap(analysis)
  );

  return (
    <Panel
      title="최종 시장점유율 보고"
      description="선택 구간의 공개 체결·호가·공지 관측값을 같은 계산 시점 기준으로 정리합니다. 확인이 끝난 값과 아직 확인 중인 값, 진단 관측, 시간적 맥락을 구분합니다."
      className="final-market-report"
      icon={<FileText className="h-4 w-4" />}
      actions={
        <span
          className={`final-report-status final-report-status-${context.selected_window_status.toLowerCase()}`}
        >
          {reportStatus}
        </span>
      }
    >
      <header className="final-report-meta">
        <div>
          <span className="final-report-kicker">선택 구간</span>
          <strong>{windowLabel(context.window_sec)}</strong>
        </div>
        <div>
          <span className="final-report-kicker">시장 범위</span>
          <strong>{context.market_scope_label}</strong>
        </div>
        <div>
          <span className="final-report-kicker">기준 시각</span>
          <strong>{reportTimestamp(context.as_of)}</strong>
        </div>
        <div>
          <span className="final-report-kicker">분석 버전</span>
          <strong>v{context.analysis_version.toLocaleString()}</strong>
        </div>
      </header>

      <section className="final-report-hero">
        <div className="final-report-primary">
          <span className="final-report-section-label">{calculationLabel}</span>
          <p>빗썸 체결 거래금액 점유율</p>
          <strong>
            {shareAvailable ? percent(windowView.sharePct, 2) : "—"}
          </strong>
          {!shareAvailable && windowView.shareReason && (
            <p className="final-report-primary-change text-muted-foreground">
              {windowView.shareReason}
            </p>
          )}
          {windowView.shareBasisLabel && (
            <p className="final-report-primary-change text-muted-foreground">
              {windowView.shareBasisNote}
            </p>
          )}
          {comparisonComplete ? (
            <p className="final-report-primary-change">
              {percent(attribution.previous_share_pct, 2)} →{" "}
              {percent(attribution.current_share_pct, 2)}
              <span className={changeTone(attribution.share_change_pp)}>
                {formatShareChangeWords(attribution.share_change_pp, 2)}
              </span>
            </p>
          ) : (
            <p className="final-report-primary-change text-muted-foreground">
              {context.comparison_enabled
                ? "직전 동일 구간 비교 자료 없음"
                : "현재 최근 24시간 누적값 · 직전 24시간 비교 안 함"}
            </p>
          )}
        </div>

        <dl className="final-report-facts">
          <div>
            <dt>선택 구간 거래금액</dt>
            <dd>
              빗썸{" "}
              {hasSelectedData
                ? money(analysis.summary.bithumb_volume_krw)
                : "—"}
            </dd>
            <span>
              국내 전체{" "}
              {hasSelectedData
                ? money(analysis.summary.domestic_volume_krw)
                : "—"}
            </span>
            {windowView.amountsQualifier && (
              <span className="text-muted-foreground">
                {windowView.amountsQualifier}
              </span>
            )}
          </div>
          <div>
            <dt>직전 동일 구간 대비 거래금액</dt>
            <dd
              className={
                comparisonComplete
                  ? changeTone(attribution.bithumb_volume_change_krw)
                  : "text-muted-foreground"
              }
            >
              빗썸{" "}
              {comparisonComplete
                ? moneyChange(attribution.bithumb_volume_change_krw)
                : "—"}
            </dd>
            <span>
              국내 전체{" "}
              {comparisonComplete
                ? moneyChange(attribution.market_volume_change_krw)
                : "—"}
            </span>
          </div>
          <div>
            <dt>점유율 변화 분해</dt>
            <dd
              className={
                comparisonComplete
                  ? changeTone(attribution.bithumb_direct_effect_pp)
                  : "text-muted-foreground"
              }
            >
              빗썸 거래금액 변화 기여{" "}
              {comparisonComplete
                ? points(attribution.bithumb_direct_effect_pp)
                : "—"}
            </dd>
            <span>
              경쟁 거래소 거래금액 변화 기여{" "}
              {comparisonComplete
                ? points(
                    attribution.competitor_effect_pp ??
                      attribution.domestic_market_size_effect_pp
                  )
                : "—"}
            </span>
          </div>
        </dl>
      </section>

      <section className="final-report-section">
        <header className="final-report-section-header">
          <div>
            <span className="final-report-section-label">
              {calculationLabel}
            </span>
            <h4>국내 거래소 결과</h4>
          </div>
          <p>
            모든 금액은 선택 구간의 체결 거래금액을 원화로 환산한 값입니다.
          </p>
        </header>
        <div className="final-report-table-scroll">
          <table className="final-report-table">
            <thead>
              <tr>
                <th>거래소</th>
                <th>현재 거래금액</th>
                <th>
                  현재 점유율
                  {windowView.shareBasisLabel && (
                    <small className="block font-normal text-muted-foreground">
                      {windowView.shareBasisLabel}
                    </small>
                  )}
                </th>
                <th>체결 건수</th>
                <th>직전 거래금액</th>
                <th>거래금액 증감</th>
              </tr>
            </thead>
            <tbody>
              {exchangeKeys.map(exchange => {
                const current = currentByExchange.get(exchange);
                const previous = comparisonByExchange.get(exchange);
                const currentObserved = exchangeTradeStreamReady(
                  analysis,
                  exchange
                );
                const excludedFromComparison =
                  excludedComparisonExchanges.has(exchange);
                return (
                  <tr key={exchange}>
                    <th scope="row">
                      <ExchangeIdentity exchange={exchange} />
                    </th>
                    <td>
                      {hasSelectedData && current && currentObserved
                        ? money(current.volume_krw)
                        : "—"}
                    </td>
                    <td>
                      {shareAvailable && current && currentObserved
                        ? percent(current.share_pct, 2)
                        : "—"}
                    </td>
                    <td>
                      {hasSelectedData && current && currentObserved
                        ? `${current.trade_count.toLocaleString()}회`
                        : "—"}
                    </td>
                    <td>
                      {excludedFromComparison
                        ? "제외"
                        : comparisonComplete && previous
                          ? money(previous.previous_volume_krw)
                          : "—"}
                    </td>
                    <td
                      className={
                        comparisonComplete &&
                        previous &&
                        !excludedFromComparison
                          ? changeTone(previous.volume_change_krw)
                          : "text-muted-foreground"
                      }
                    >
                      {excludedFromComparison
                        ? "제외"
                        : comparisonComplete && previous
                          ? moneyChange(previous.volume_change_krw)
                          : "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>

      <section className="final-report-section">
        <header className="final-report-section-header">
          <div>
            <span className="final-report-section-label">
              {calculationLabel}
            </span>
            <h4>점유율 변화 핵심 코인</h4>
          </div>
          <p>
            {comparisonComplete
              ? `전체 ${analysis.expert_brief.all_assets_in_change_attribution.toLocaleString()}개를 계산하고, 상승·하락 각 방향 누적 80%를 설명하는 ${drivers.length.toLocaleString()}개를 표시합니다. 순영향은 빗썸 거래 변화 효과와 경쟁 거래소 변화 효과의 합입니다.`
              : `현재 ${analysis.summary.analyzed_asset_count.toLocaleString()}개 코인을 관측했습니다. 코인별 변화 기여는 직전 같은 길이 구간이 확정되면 계산합니다.`}
          </p>
        </header>
        {comparisonComplete ? (
          <div className="final-report-table-scroll final-report-driver-scroll">
            <table className="final-report-table final-report-driver-table">
              <thead>
                <tr>
                  <th>코인·해석</th>
                  <th>점유율 변화 기여</th>
                  <th>빗썸 거래금액 기여</th>
                  <th>경쟁 거래소 거래금액 기여</th>
                  <th>빗썸 거래금액 증감</th>
                  <th>국내 거래금액 증감</th>
                  <th>업비트 증감</th>
                </tr>
              </thead>
              <tbody>
                {drivers.map(row => (
                  <tr key={row.asset}>
                    <th scope="row">
                      <span className="final-report-asset-line">
                        <strong>{row.asset}</strong>
                        <span>
                          {domesticListingLabel(row.domestic_exchanges)}
                        </span>
                      </span>
                      <small>{assetImpactInterpretation(row)}</small>
                    </th>
                    <td className={changeTone(row.contribution_pp)}>
                      {points(row.contribution_pp)}
                    </td>
                    <td className={changeTone(row.bithumb_direct_effect_pp)}>
                      {points(row.bithumb_direct_effect_pp)}
                    </td>
                    <td className={changeTone(row.competitor_effect_pp)}>
                      {points(row.competitor_effect_pp)}
                    </td>
                    <td className={changeTone(row.bithumb_volume_change_krw)}>
                      {moneyChange(row.bithumb_volume_change_krw)}
                    </td>
                    <td className={changeTone(row.domestic_volume_change_krw)}>
                      {moneyChange(row.domestic_volume_change_krw)}
                    </td>
                    <td className={changeTone(row.upbit_volume_change_krw)}>
                      {moneyChange(row.upbit_volume_change_krw)}
                    </td>
                  </tr>
                ))}
                {!drivers.length && (
                  <tr>
                    <td
                      colSpan={7}
                      className="text-center text-muted-foreground"
                    >
                      점유율 변화에 유의미한 코인 영향이 없습니다.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="final-report-unavailable">
            직전 동일 구간 자료가 부족해 코인별 증감과 점유율 변화 기여는
            확정값으로 표시하지 않습니다.
          </div>
        )}
      </section>

      {diagnostics.length > 0 && (
        <section className="final-report-section">
          <header className="final-report-section-header">
            <div>
              <span className="final-report-section-label final-report-section-label-diagnostic">
                진단 관측
              </span>
              <h4>시장 구조 점검</h4>
            </div>
            <p>
              관측값으로 확인한 설명 후보이며 원인 확정이나 인과 판단은
              아닙니다.
            </p>
          </header>
          <div className="final-report-diagnostics">
            {diagnostics.map(check => (
              <article key={check.code}>
                <div>
                  <span>{diagnosticStatusLabel(check.status)}</span>
                  <h5>{check.title}</h5>
                </div>
                <strong className={changeTone(check.value)}>
                  {diagnosticMetric(check.value, check.unit)}
                </strong>
                <p>{check.detail}</p>
              </article>
            ))}
          </div>
        </section>
      )}

      {relevantNotices.length > 0 && (
        <section className="final-report-section">
          <header className="final-report-section-header">
            <div>
              <span className="final-report-section-label final-report-section-label-context">
                시간적 맥락
              </span>
              <h4>선택 구간과 겹친 국내 공지·이벤트</h4>
            </div>
            <p>
              국내 공지 중 선택 자산의 시장 관측과 연결된 항목만 표시합니다.
              시간적 연관이며 인과관계는 아닙니다.
            </p>
          </header>
          <div className="final-report-notices">
            {relevantNotices.map(notice => {
              const assets = exchangeNoticeAssetLabels(notice);
              return (
                <article key={`${notice.exchange}-${notice.notice_id}`}>
                  <div className="final-report-notice-heading">
                    <div>
                      <span>
                        {exchangeLabel(notice.exchange)} ·{" "}
                        {noticeCategoryLabel(notice.category)}
                      </span>
                      <a href={notice.url} target="_blank" rel="noreferrer">
                        {notice.title}
                      </a>
                    </div>
                    <time>{noticeTimestampLabel(notice)}</time>
                  </div>
                  {(assets.length > 0 || notice.summary) && (
                    <p>
                      {assets.length > 0 && (
                        <strong>{assets.join(", ")}</strong>
                      )}
                      {assets.length > 0 && notice.summary ? " · " : ""}
                      {notice.summary && noticeSummaryDisplayText(notice.summary)}
                    </p>
                  )}
                  {uniqueNoticeSchedules(notice.schedules).map((schedule, index) => (
                    <p
                      key={`${notice.notice_id}-${schedule.kind}-${schedule.start_ts_ms}-${index}`}
                    >
                      {noticeScheduleLabel(schedule.kind)} ·{" "}
                      {noticeScheduleTime(schedule.start_ts_ms)}
                      {schedule.end_ts_ms > 0
                        ? ` ~ ${noticeScheduleTime(schedule.end_ts_ms)}`
                        : ""}
                    </p>
                  ))}
                  {(notice.reaction_studies ?? [])
                    .filter(
                      study =>
                        (study.status === "MATURE" ||
                          study.status === "LIVE_ESTIMATE") &&
                        study.bithumb_pre_share_pct !== null &&
                        study.bithumb_pre_share_pct !== undefined &&
                        Number.isFinite(study.bithumb_pre_share_pct) &&
                        study.bithumb_post_share_pct !== null &&
                        study.bithumb_post_share_pct !== undefined &&
                        Number.isFinite(study.bithumb_post_share_pct)
                    )
                    .map(study => (
                      <p
                        key={`${study.asset}-${study.exchange}-${study.anchor_ts_ms}`}
                      >
                        {study.asset} 공지 전후 빗썸 점유율 ·{" "}
                        {noticeStudyStatusLabel(study.status)} ·{" "}
                        {noticeStudySharesConsistent(study) ? (
                          <>
                            {percent(study.bithumb_pre_share_pct, 2)} →{" "}
                            {percent(study.bithumb_post_share_pct, 2)}
                            {study.bithumb_share_change_pp !== null &&
                            study.bithumb_share_change_pp !== undefined
                              ? ` (${points(study.bithumb_share_change_pp)})`
                              : ""}
                          </>
                        ) : (
                          "자료 확인 필요"
                        )}
                      </p>
                    ))}
                </article>
              );
            })}
          </div>
        </section>
      )}

      <footer className="final-report-footer">
        <span>상태 {reportStatus}</span>
        <span>
          관측 {duration(context.selected_window_coverage_sec)} / 필요{" "}
          {duration(context.selected_window_required_sec)}
        </span>
        <span>{marketShareGapLabel(selectedWindowGap(analysis))}</span>
        <span>
          처리 전 누락{" "}
          {analysis.data_quality.components.trade_share.queue_dropped.toLocaleString()}
          건
        </span>
        {analysisVersionLabel(context.aggregation_version) && (
          <span>{analysisVersionLabel(context.aggregation_version)}</span>
        )}
      </footer>
    </Panel>
  );
}

function ExecutiveReportTable({ analysis }: { analysis: MarketShareAnalysis }) {
  const attribution = analysis.change_attribution;
  const comparisonComplete = comparisonValuesAvailable(analysis);
  const shareAvailable = shareDenominatorAvailable(analysis);
  const currentByExchange = new Map(
    analysis.exchanges.map(row => [row.exchange, row])
  );
  const comparisonByExchange = new Map(
    attribution.exchanges.map(row => [row.exchange, row])
  );
  const risingAssets = analysis.expert_brief.positive_assets;
  const fallingAssets = analysis.expert_brief.negative_assets;
  const upbitCurrent = currentByExchange.get("upbit");
  const upbitComparison = comparisonByExchange.get("upbit");
  const assetRows = [
    ...risingAssets.map(row => ({ direction: "상승" as const, row })),
    ...fallingAssets.map(row => ({ direction: "하락" as const, row })),
  ];

  return (
    <Panel
      title={
        <span className="inline-help-heading">
          보고용 점유율 대조표
          <InlineHelp text="선택 구간과 직전 동일 구간을 보고서 양식과 같은 열 구조로 맞춘 확정 계산표입니다. 표시 코인은 상승·하락 각 방향 누적 80%를 설명하는 전체 항목입니다." />
        </span>
      }
      className="executive-report-table"
      icon={<FileText className="h-4 w-4" />}
      actions={
        <span
          className={`final-report-status final-report-status-${analysis.context.selected_window_status.toLowerCase()}`}
        >
          {coverageLabel(
            analysis.context.selected_window_status,
            selectedWindowGap(analysis)
          )}
        </span>
      }
    >
      <section className="executive-report-section">
        <header className="executive-report-section-heading">
          <strong>1. 국내 거래소 점유율 비교</strong>
          <span>
            {windowLabel(analysis.context.window_sec)} · 기준{" "}
            {reportTimestamp(analysis.context.as_of)}
          </span>
        </header>
        <div className="executive-report-scroll">
          <table className="executive-report-ledger executive-report-exchange-ledger">
            <thead>
              <tr>
                <th rowSpan={2}>거래소</th>
                <th colSpan={3}>체결 거래금액</th>
                <th colSpan={3}>국내 점유율</th>
                <th rowSpan={2}>
                  선택 구간
                  <br />
                  체결 건수
                </th>
              </tr>
              <tr>
                <th>직전 구간</th>
                <th>선택 구간</th>
                <th>증감</th>
                <th>직전 구간</th>
                <th>선택 구간</th>
                <th>증감</th>
              </tr>
            </thead>
            <tbody>
              {DOMESTIC_EXCHANGES.map(exchange => {
                const current = currentByExchange.get(exchange);
                const comparison = comparisonByExchange.get(exchange);
                return (
                  <tr key={exchange}>
                    <th scope="row">
                      <ExchangeIdentity exchange={exchange} />
                    </th>
                    <td>
                      {comparisonComplete && comparison
                        ? money(comparison.previous_volume_krw)
                        : "—"}
                    </td>
                    <td>{current ? money(current.volume_krw) : "—"}</td>
                    <td
                      className={
                        comparisonComplete && comparison
                          ? changeTone(comparison.volume_change_krw)
                          : "text-muted-foreground"
                      }
                    >
                      {comparisonComplete && comparison
                        ? moneyChange(comparison.volume_change_krw)
                        : "—"}
                    </td>
                    <td>
                      {comparisonComplete && comparison
                        ? percent(comparison.previous_share_pct, 2)
                        : "—"}
                    </td>
                    <td>
                      {shareAvailable && current
                        ? percent(current.share_pct, 2)
                        : "—"}
                    </td>
                    <td
                      className={
                        comparisonComplete && comparison
                          ? changeTone(comparison.share_change_pp)
                          : "text-muted-foreground"
                      }
                    >
                      {comparisonComplete && comparison
                        ? points(comparison.share_change_pp, 2)
                        : "—"}
                    </td>
                    <td>
                      {current
                        ? `${current.trade_count.toLocaleString("ko-KR")}회`
                        : "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
            <tfoot>
              <tr>
                <th scope="row">국내 5개 거래소 합계</th>
                <td>
                  {comparisonComplete
                    ? money(attribution.previous_volume_krw)
                    : "—"}
                </td>
                <td>{money(analysis.summary.domestic_volume_krw)}</td>
                <td
                  className={
                    comparisonComplete
                      ? changeTone(attribution.market_volume_change_krw)
                      : "text-muted-foreground"
                  }
                >
                  {comparisonComplete
                    ? moneyChange(attribution.market_volume_change_krw)
                    : "—"}
                </td>
                <td>{comparisonComplete ? "100.00%" : "—"}</td>
                <td>{shareAvailable ? "100.00%" : "—"}</td>
                <td>{comparisonComplete ? "변화 없음" : "—"}</td>
                <td>
                  {analysis.summary.domestic_trade_count.toLocaleString(
                    "ko-KR"
                  )}
                  회
                </td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>

      <section className="executive-report-section">
        <header className="executive-report-section-heading">
          <strong>2. 주요 코인별 거래금액 증감·점유율 기여</strong>
          <span>
            {comparisonComplete
              ? `전체 ${analysis.expert_brief.all_assets_in_change_attribution.toLocaleString("ko-KR")}개 계산 · 표시 ${assetRows.length.toLocaleString("ko-KR")}개`
              : `현재 ${analysis.summary.analyzed_asset_count.toLocaleString("ko-KR")}개 관측 · 비교 계산 대기`}
          </span>
        </header>
        <div className="executive-report-scroll executive-report-asset-scroll">
          <table className="executive-report-ledger executive-report-asset-ledger">
            <thead>
              <tr>
                <th rowSpan={2}>방향</th>
                <th rowSpan={2}>코인·상장 관계</th>
                <th colSpan={3} className="executive-report-bithumb-group">
                  빗썸 거래금액
                </th>
                <th colSpan={3} className="executive-report-upbit-group">
                  업비트 거래금액
                </th>
                <th colSpan={3}>국내 5개 거래소 거래금액</th>
                <th rowSpan={2}>
                  빗썸 점유율
                  <br />
                  변화 기여
                </th>
              </tr>
              <tr>
                <th>직전</th>
                <th>현재</th>
                <th>증감</th>
                <th>직전</th>
                <th>현재</th>
                <th>증감</th>
                <th>직전</th>
                <th>현재</th>
                <th>증감</th>
              </tr>
            </thead>
            <tbody>
              {comparisonComplete &&
                assetRows.map(({ direction, row }, index) => {
                  const startsDirection =
                    index === 0 || assetRows[index - 1].direction !== direction;
                  return (
                    <tr
                      key={row.asset}
                      className={
                        startsDirection
                          ? "executive-report-direction-start"
                          : undefined
                      }
                    >
                      <td>
                        <span
                          className={`executive-report-direction executive-report-direction-${direction === "상승" ? "up" : "down"}`}
                        >
                          {direction}
                        </span>
                      </td>
                      <th scope="row">
                        <strong>{row.asset}</strong>
                        <small>
                          {domesticListingLabel(row.domestic_exchanges)}
                        </small>
                      </th>
                      <td>{money(row.previous_bithumb_volume_krw)}</td>
                      <td>{money(row.current_bithumb_volume_krw)}</td>
                      <td className={changeTone(row.bithumb_volume_change_krw)}>
                        {moneyChange(row.bithumb_volume_change_krw)}
                      </td>
                      <td>{money(row.previous_upbit_volume_krw)}</td>
                      <td>{money(row.current_upbit_volume_krw)}</td>
                      <td className={changeTone(row.upbit_volume_change_krw)}>
                        {moneyChange(row.upbit_volume_change_krw)}
                      </td>
                      <td>{money(row.previous_domestic_volume_krw)}</td>
                      <td>{money(row.current_domestic_volume_krw)}</td>
                      <td
                        className={changeTone(row.domestic_volume_change_krw)}
                      >
                        {moneyChange(row.domestic_volume_change_krw)}
                      </td>
                      <td className={changeTone(row.contribution_pp)}>
                        {points(row.contribution_pp)}
                      </td>
                    </tr>
                  );
                })}
              {!comparisonComplete && (
                <tr>
                  <td colSpan={12} className="executive-report-empty">
                    {analysis.context.comparison_enabled
                      ? "직전 동일 구간 자료가 부족해 코인별 증감·기여도를 확정하지 않습니다."
                      : "24시간 화면은 최근 24시간 누적값을 보여주며 직전 24시간 비교는 하지 않습니다."}
                  </td>
                </tr>
              )}
              {comparisonComplete && !assetRows.length && (
                <tr>
                  <td colSpan={12} className="executive-report-empty">
                    표시할 점유율 변화 코인이 없습니다.
                  </td>
                </tr>
              )}
            </tbody>
            <tfoot>
              <tr>
                <th colSpan={2} scope="row">
                  전체 계산 합계
                </th>
                <td>
                  {comparisonComplete
                    ? money(attribution.previous_bithumb_volume_krw)
                    : "—"}
                </td>
                <td>{money(analysis.summary.bithumb_volume_krw)}</td>
                <td
                  className={
                    comparisonComplete
                      ? changeTone(attribution.bithumb_volume_change_krw)
                      : "text-muted-foreground"
                  }
                >
                  {comparisonComplete
                    ? moneyChange(attribution.bithumb_volume_change_krw)
                    : "—"}
                </td>
                <td>
                  {comparisonComplete && upbitComparison
                    ? money(upbitComparison.previous_volume_krw)
                    : "—"}
                </td>
                <td>{upbitCurrent ? money(upbitCurrent.volume_krw) : "—"}</td>
                <td
                  className={
                    comparisonComplete && upbitComparison
                      ? changeTone(upbitComparison.volume_change_krw)
                      : "text-muted-foreground"
                  }
                >
                  {comparisonComplete && upbitComparison
                    ? moneyChange(upbitComparison.volume_change_krw)
                    : "—"}
                </td>
                <td>
                  {comparisonComplete
                    ? money(attribution.previous_volume_krw)
                    : "—"}
                </td>
                <td>{money(analysis.summary.domestic_volume_krw)}</td>
                <td
                  className={
                    comparisonComplete
                      ? changeTone(attribution.market_volume_change_krw)
                      : "text-muted-foreground"
                  }
                >
                  {comparisonComplete
                    ? moneyChange(attribution.market_volume_change_krw)
                    : "—"}
                </td>
                <td
                  className={
                    comparisonComplete
                      ? changeTone(attribution.share_change_pp)
                      : "text-muted-foreground"
                  }
                >
                  {comparisonComplete
                    ? points(attribution.share_change_pp)
                    : "—"}
                </td>
              </tr>
            </tfoot>
          </table>
        </div>
      </section>
    </Panel>
  );
}

function ExchangeSharePulse({
  exchange,
  tickStore,
  connection,
  marketScope,
  windowSec,
}: {
  exchange: string;
  tickStore: MarketShareTickStore;
  connection: MarketShareConnection;
  marketScope: MarketScope;
  windowSec: number;
}) {
  const tick = useSyncExternalStore(
    tickStore.subscribe,
    tickStore.getSnapshot,
    tickStore.getSnapshot
  );
  const [history, setHistory] = useState<SharePulsePoint[]>([]);

  useEffect(() => setHistory([]), [exchange, marketScope, windowSec]);
  useEffect(() => {
    if (connection !== "live") setHistory([]);
  }, [connection]);
  useEffect(() => {
    if (
      connection !== "live" ||
      !tick ||
      tick.market_scope !== marketScope ||
      tick.window_sec !== windowSec ||
      !liveTickExchangeObserved(tick, exchange) ||
      tick.coverage_sec <= 0 ||
      tick.summary.domestic_volume_krw <= 0
    )
      return;

    const exchangeTick = tick.exchanges.find(row => row.exchange === exchange);
    if (
      !exchangeTick ||
      exchangeTick.share_pct === null ||
      !Number.isFinite(exchangeTick.share_pct)
    )
      return;
    const point = {
      at: tick.generated_at * 1_000,
      value: exchangeTick.share_pct,
    };
    setHistory(current => {
      const next =
        current.at(-1)?.at === point.at
          ? [...current.slice(0, -1), point]
          : [...current, point];
      return next.filter(row => row.at >= point.at - SHARE_PULSE_WINDOW_MS);
    });
  }, [connection, exchange, marketScope, tick, windowSec]);

  const { segments, latest } = useMemo(
    () => sharePulseGeometry(history),
    [history]
  );
  const gradientId = `exchange-share-pulse-${exchange}`;
  return (
    <div
      className="exchange-share-pulse"
      style={{ color: `var(--exchange-${exchange})` }}
      aria-hidden="true"
    >
      <svg
        className="exchange-share-pulse-svg"
        viewBox="0 0 100 100"
        preserveAspectRatio="none"
      >
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="1" y2="0">
            <stop offset="0%" stopColor="currentColor" stopOpacity="0" />
            <stop offset="28%" stopColor="currentColor" stopOpacity="0.22" />
            <stop offset="100%" stopColor="currentColor" stopOpacity="0.72" />
          </linearGradient>
        </defs>
        {segments.map(segment => {
          const path = smoothPulsePath(segment);
          return path ? (
            <path
              key={`${segment[0].at}-${segment.at(-1)!.at}`}
              className="exchange-share-pulse-path"
              d={path}
              stroke={`url(#${gradientId})`}
            />
          ) : null;
        })}
      </svg>
      {latest && (
        <span
          key={`${exchange}-${latest.at}`}
          className="exchange-share-pulse-dot"
          style={{ left: `${latest.x}%`, top: `${latest.y}%` }}
        />
      )}
    </div>
  );
}

function ExchangeSummaryCard({
  exchange,
  analysis,
  tickStore,
  connection,
  collapsible = false,
}: {
  exchange: string;
  analysis: MarketShareAnalysis;
  tickStore: MarketShareTickStore;
  connection: MarketShareConnection;
  collapsible?: boolean;
}) {
  const [expanded, setExpanded] = useState(!collapsible);
  const context = analysis.context;
  const trading = analysis.exchanges.find(row => row.exchange === exchange);
  const previous = analysis.change_attribution.exchanges.find(
    row => row.exchange === exchange
  );
  const liquidity = analysis.liquidity.exchanges.find(
    row => row.exchange === exchange
  );
  const collector = analysis.collectors[exchange];
  // Observed counts and amounts are real values even while the window is only
  // partly covered; only ratios that need the whole denominator wait for it.
  const windowView = selectedWindowView(analysis);
  const hasWindowData = windowView.showsAmounts;
  // Comparing a partly observed amount with a full previous interval would
  // print a change that is really just missing time.
  const completeWindow = selectedWindowValuesAvailable(analysis);
  const shareAvailable = windowView.showsShare;
  const tradeShareAvailable =
    hasWindowData && analysis.summary.domestic_trade_count > 0;
  const scopedTradeStreamReady =
    context.market_scope === "KRW"
      ? (collector?.krw_trade_stream_ready ?? collector?.trade_stream_ready)
      : collector?.trade_stream_ready;
  const tradeStatus = !collector?.enabled
    ? "사용 안 함"
    : collector.active_trade_parse_error_chunks > 0
      ? "해석 오류"
      : scopedTradeStreamReady
        ? "정상"
        : "수신 지연";
  const orderbookStatus =
    collector && !collector.orderbook_enabled
      ? "사용 안 함"
      : collector?.orderbook_stream_ready
        ? "정상"
        : "부분";
  const tradingRows = [
    [
      "국내 거래금액 중 이 거래소 상장 코인의 비중",
      shareAvailable
        ? percent(trading?.listed_asset_domestic_volume_coverage_pct, 1)
        : "—",
    ],
    [
      `최근 ${windowLabel(context.window_sec)} 국내 체결 건수 중 이 거래소 비중`,
      tradeShareAvailable ? percent(trading?.trade_count_share_pct, 2) : "—",
    ],
    [
      `체결 건수(${windowSpanLabel(context.window_sec)})`,
      hasWindowData && trading
        ? `${trading.trade_count.toLocaleString()}회`
        : "—",
    ],
    [
      "주문 체결 1건당 평균 거래금액",
      hasWindowData && trading ? money(trading.average_trade_krw) : "—",
    ],
  ];
  const cumulativeDepths = ORDERBOOK_DEPTH_LEVELS.map(level => ({
    level,
    value:
      observedLiquidityValue(
        liquidity?.live_asset_count,
        liquidity?.cumulative_depth_krw?.[level] ??
          (level === "10" ? liquidity?.depth_krw : undefined)
      ) ?? undefined,
  }));
  const depthWaterfallRows = cumulativeDepths.map((row, index) => ({
    ...row,
    fromLevel: index > 0 ? cumulativeDepths[index - 1].level : null,
    previousValue: index > 0 ? cumulativeDepths[index - 1].value : 0,
  }));
  const maxDepth = Math.max(
    ...depthWaterfallRows.map(row => row.value ?? 0),
    1
  );
  return (
    <article
      className={`exchange-panel min-w-0 ${expanded ? "exchange-panel-expanded" : "exchange-panel-collapsed"}`}
    >
      <div
        className="exchange-panel-accent"
        style={{ backgroundColor: `var(--exchange-${exchange})` }}
      />
      <header className="exchange-panel-header flex items-start justify-between px-5 py-4">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <span
              className="exchange-panel-icon"
              style={{
                color: `var(--exchange-${exchange})`,
                borderColor: `var(--exchange-${exchange})`,
              }}
              aria-hidden="true"
            >
              <Radio className="h-4 w-4" />
            </span>
            <h3 className="exchange-panel-title mr-1 text-lg font-semibold">
              <ExchangeBrandLabel exchange={exchange} />
            </h3>
            <span
              className={`exchange-panel-status ${tradeStatus === "정상" ? "border-profit/40 text-profit" : "border-gold/40 text-gold"}`}
            >
              체결 {tradeStatus}
            </span>
            <span
              className={`exchange-panel-status ${orderbookStatus === "정상" ? "border-profit/40 text-profit" : orderbookStatus === "사용 안 함" ? "border-border text-muted-foreground" : "border-gold/40 text-gold"}`}
            >
              호가 {orderbookStatus}
            </span>
            <span
              className={`exchange-panel-status ${context.selected_window_status === "CONFIRMED" ? "border-profit/40 text-profit" : "border-gold/40 text-gold"}`}
            >
              {coverageLabel(
                context.selected_window_status,
                selectedWindowGap(analysis)
              )}
            </span>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            중복 제외 코인{" "}
            {trading
              ? `${trading.registered_asset_count.toLocaleString()}개`
              : "—"}{" "}
            ·{" "}
            {context.market_scope === "KRW"
              ? "KRW 현물"
              : "모든 활성 현물 통합"}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {collapsible && (
            <button
              type="button"
              aria-expanded={expanded}
              aria-label={`${exchangeLabel(exchange)} 카드 ${expanded ? "접기" : "펼치기"}`}
              onClick={() => setExpanded(current => !current)}
              className="inline-flex items-center gap-1 border border-border px-2.5 py-1 text-sm text-muted-foreground hover:text-foreground"
            >
              {expanded ? "접기" : "펼치기"}
              <ChevronDown
                className={`h-3.5 w-3.5 transition-transform ${expanded ? "rotate-180" : ""}`}
              />
            </button>
          )}
        </div>
      </header>
      {expanded && (
        <div className="exchange-panel-body">
          <section className="exchange-primary-metric px-5 py-4">
            <ExchangeSharePulse
              exchange={exchange}
              tickStore={tickStore}
              connection={connection}
              marketScope={context.market_scope}
              windowSec={context.window_sec}
            />
            <div className="exchange-primary-content">
              <p className="text-xs text-muted-foreground">
                최근 {windowLabel(context.window_sec)} 체결 거래금액 점유율
                {windowView.shareBasisLabel && (
                  <>
                    {" · "}
                    <strong>{windowView.shareBasisLabel}</strong>
                  </>
                )}
              </p>
              <div className="mt-2 flex flex-wrap items-end justify-between gap-3">
                <strong className="text-3xl tracking-tight">
                  {shareAvailable ? percent(trading?.share_pct, 2) : "—"}
                </strong>
                <div className="text-right">
                  <p className="text-lg font-semibold">
                    {hasWindowData && trading ? money(trading.volume_krw) : "—"}
                  </p>
                  <p className="mt-1 text-base text-muted-foreground">
                    {completeWindow && previous
                      ? `직전 동일 ${windowLabel(context.window_sec)} 구간 대비 ${signedPercent(previous.volume_change_pct)}`
                      : "직전 동일 구간 비교 없음"}
                  </p>
                </div>
              </div>
            </div>
          </section>
          <div className="exchange-metric-grid grid grid-cols-2 text-xs">
            {tradingRows.map(([label, value]) => (
              <div key={label} className="exchange-metric-cell p-3">
                <p className="text-base leading-6 text-muted-foreground">
                  {label}
                </p>
                <p className="mt-1.5 text-sm font-semibold">{value}</p>
              </div>
            ))}
          </div>
          <section className="exchange-book-section">
            <div className="grid grid-cols-1 gap-px border-b border-border bg-border px-1 py-3">
              <div className="px-3">
                <div className="flex items-center justify-between gap-3">
                  <p className="text-base text-muted-foreground">
                    매수·매도 누적 호가 잔량
                  </p>
                  <span className="text-base text-muted-foreground">
                    구간별 증가분 · 누적 합계
                  </span>
                </div>
                <div
                  className="depth-waterfall mt-3"
                  role="img"
                  aria-label="가장 좋은 가격부터 10단계까지 누적 주문 잔량 그래프"
                >
                  {depthWaterfallRows.map(
                    ({ level, value, fromLevel, previousValue }) => {
                      const canPlot =
                        value !== undefined && previousValue !== undefined;
                      const increment = canPlot
                        ? Math.max(0, value - previousValue)
                        : 0;
                      const start = canPlot
                        ? (previousValue / maxDepth) * 100
                        : 0;
                      const width = canPlot ? (increment / maxDepth) * 100 : 0;
                      return (
                        <div key={level} className="depth-waterfall-row">
                          <div className="depth-waterfall-label">
                            <span className="font-medium">
                              {fromLevel
                                ? `${fromLevel}→${level}단계`
                                : `${level}단계`}
                            </span>
                            <span className="text-base text-muted-foreground">
                              {canPlot && fromLevel
                                ? `추가 ${money(increment)}`
                                : canPlot
                                  ? "기준 잔량"
                                  : "자료 없음"}
                            </span>
                          </div>
                          <div
                            className="depth-waterfall-track"
                            aria-hidden="true"
                          >
                            {canPlot && (
                              <>
                                <span
                                  className="depth-waterfall-segment"
                                  style={{
                                    left: `${start}%`,
                                    width: `${Math.max(width, 0.75)}%`,
                                  }}
                                />
                                <span
                                  className="depth-waterfall-endpoint"
                                  style={{
                                    left: `${Math.min(100, start + width)}%`,
                                  }}
                                />
                              </>
                            )}
                          </div>
                          <strong
                            className="depth-waterfall-total"
                            title={
                              value === undefined
                                ? "자료 없음"
                                : money(value)
                            }
                          >
                            {value === undefined ? "—" : money(value)}
                          </strong>
                        </div>
                      );
                    }
                  )}
                </div>
              </div>
            </div>
          </section>
        </div>
      )}
    </article>
  );
}

function Overview({
  analysis,
  tickStore,
  connection,
  marketScope,
  requestedWindowSec,
  windowSec,
}: {
  analysis: MarketShareAnalysis;
  tickStore: MarketShareTickStore;
  connection: MarketShareConnection;
  marketScope: MarketScope;
  requestedWindowSec: number;
  windowSec: number;
}) {
  const context = analysis.context;
  const attribution = analysis.change_attribution;
  // Observed amounts stay visible while a window is only partly covered, with
  // the received and missing span; a share waits for the server to publish it.
  const windowView = selectedWindowView(analysis);
  const hasSelectedData = windowView.showsAmounts;
  const shareAvailable = windowView.showsShare;
  const tradeShareAvailable =
    hasSelectedData && analysis.summary.domestic_trade_count > 0;
  const comparisonComplete = comparisonValuesAvailable(analysis);
  const headline = analysis.expert_brief.attribution_headline;
  const dominant = headline?.dominant_listing_structure ?? null;
  const dominantCoins = dominant?.leading_assets ?? [];
  const bithumbTrading = analysis.exchanges.find(
    row => row.exchange === "bithumb"
  );
  const turnoverSpeed = currentTurnoverSpeed(analysis.momentum);
  const bithumbLiquidity = analysis.liquidity.exchanges.find(
    row => row.exchange === "bithumb"
  );
  const bithumbLiquidityShare = observedLiquidityValue(
    bithumbLiquidity?.live_asset_count,
    bithumbLiquidity?.liquidity_share_pct
  );
  const bithumbDepth = observedLiquidityValue(
    bithumbLiquidity?.live_asset_count,
    bithumbLiquidity?.depth_krw
  );
  const primaryCoin = headline?.primary_asset ?? null;
  const volumeRelationship = shareVolumeRelationshipSentence(
    headline?.volume_relationship
  );

  return (
    <div className="space-y-6">
      <div className="market-brief-strip">
        <span className="market-brief-icon" aria-hidden="true">
          <Activity className="h-4 w-4" />
        </span>
        <div className="min-w-0 text-sm leading-6">
          {comparisonComplete ? (
            <>
              빗썸 점유율은{" "}
              <strong>
                {shareChangeSentence(
                  context.window_sec,
                  attribution.share_change_pp
                )}
              </strong>
              {dominant && (
                <p className="market-brief-copy ui-caption mt-2 text-muted-foreground">
                  상장 구조별로 합산하면{" "}
                  <strong className="text-foreground">
                    {listingHeadlineLabel(dominant)}
                  </strong>
                  이{" "}
                  <strong className={changeTone(dominant.contribution_pp)}>
                    {points(dominant.contribution_pp, 2)}
                  </strong>
                  로 점유율 변화 기여가 가장 컸습니다.{" "}
                  {listingStructureVolumeSummary(dominant)}
                </p>
              )}
              <p className="market-brief-copy ui-caption mt-2 text-muted-foreground">
                전체 기준으로{" "}
                {volumeTransitionSentence(
                  "빗썸 거래금액은",
                  attribution.previous_bithumb_volume_krw,
                  attribution.current_bithumb_volume_krw,
                  attribution.bithumb_volume_change_krw
                )}{" "}
                {volumeTransitionSentence(
                  "국내 전체 거래금액은",
                  attribution.previous_volume_krw,
                  attribution.current_volume_krw,
                  attribution.market_volume_change_krw
                )}
              </p>
              {volumeRelationship && (
                <p className="market-brief-copy ui-caption mt-2 text-muted-foreground">
                  {volumeRelationship}
                </p>
              )}
              {primaryCoin && (
                <p className="market-brief-copy ui-caption mt-2 text-muted-foreground">
                  코인별로는{" "}
                  <strong className={changeTone(primaryCoin.contribution_pp)}>
                    {primaryCoin.asset} {points(primaryCoin.contribution_pp, 2)}
                  </strong>
                  가 점유율을 가장 크게 움직였습니다.{" "}
                  {assetImpactInterpretation(primaryCoin)}
                </p>
              )}
              {dominant && dominantCoins.length > 0 && (
                // A table instead of one paragraph of about thirty amounts.
                <div className="market-brief-coins mt-3 border-t border-primary/20 pt-3">
                  <p className="ui-caption font-semibold text-foreground">
                    {listingHeadlineLabel(dominant)} 주요 코인
                  </p>
                  <div
                    className="market-brief-coins-scroll"
                    tabIndex={0}
                    aria-label={`${listingHeadlineLabel(dominant)} 주요 코인 표 가로 스크롤`}
                  >
                    <table className="market-brief-coin-table ui-caption">
                      <thead>
                        <tr>
                          <th scope="col">코인</th>
                          <th scope="col">점유율 기여</th>
                          <th scope="col">국내 거래금액</th>
                          <th scope="col">빗썸</th>
                          <th scope="col">경쟁 거래소</th>
                          <th scope="col">빗썸 거래금액 기여</th>
                          <th scope="col">경쟁 거래소 기여</th>
                        </tr>
                      </thead>
                      <tbody>
                        {dominantCoins.map(row => (
                          <tr key={row.asset}>
                            <th scope="row">{row.asset}</th>
                            <td className={changeTone(row.contribution_pp)}>
                              {points(row.contribution_pp, 2)}
                            </td>
                            <td>
                              {money(row.previous_domestic_volume_krw)} →{" "}
                              {money(row.current_domestic_volume_krw)}
                              <small>
                                {formatKrwCompact(row.domestic_volume_change_krw)}
                              </small>
                            </td>
                            <td>
                              {money(row.previous_bithumb_volume_krw)} →{" "}
                              {money(row.current_bithumb_volume_krw)}
                              <small>
                                {formatKrwCompact(row.bithumb_volume_change_krw)}
                              </small>
                            </td>
                            <td>
                              {money(row.previous_competitor_volume_krw)} →{" "}
                              {money(row.current_competitor_volume_krw)}
                              <small>
                                {formatKrwCompact(
                                  row.competitor_volume_change_krw
                                )}
                              </small>
                            </td>
                            <td>{points(row.bithumb_direct_effect_pp, 2)}</td>
                            <td>{points(row.competitor_effect_pp, 2)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </div>
              )}
            </>
          ) : (
            <>
              {hasSelectedData ? (
                <>
                  {shareAvailable ? (
                    <>
                      최근 {windowLabel(context.window_sec)} 빗썸 점유율은{" "}
                      <strong>{percent(windowView.sharePct, 2)}</strong>입니다.
                    </>
                  ) : (
                    currentShareSentence({
                      windowLabel: windowLabel(context.window_sec),
                      sharePercent: null,
                    })
                  )}
                  {!shareAvailable && windowView.shareReason && (
                    <span className="text-muted-foreground">
                      {" "}
                      {windowView.shareReason}
                    </span>
                  )}
                  {windowView.shareBasisNote && (
                    <span className="text-muted-foreground">
                      {" "}
                      {windowView.shareBasisNote}
                    </span>
                  )}
                  <span className="text-muted-foreground">
                    {" "}
                    빗썸은 {money(analysis.summary.bithumb_volume_krw)}, 국내
                    전체는 {money(analysis.summary.domestic_volume_krw)}이며
                    {context.data_source.official_24h_ticker_used
                      ? " 국내 5개 거래소의 공식 최근 24시간 거래금액 합계입니다."
                      : " 새 체결마다 누적합니다."}
                    {context.selected_window_status === "PARTIAL" &&
                      unobservedSpanSentence({
                        gapSec: selectedWindowGap(analysis),
                        supplemented:
                          context.data_source.official_minute_supplement_used ===
                          true,
                      })}
                  </span>
                </>
              ) : (
                "공개 체결을 수신하면 현재 선택 구간 합계가 자동으로 표시됩니다."
              )}
            </>
          )}
        </div>
      </div>

      <section className="metric-grid sm:grid-cols-2 xl:grid-cols-4">
        <KpiCard
          label="빗썸 거래금액 점유율"
          icon={<BarChart3 className="h-4 w-4" />}
          value={shareAvailable ? percent(windowView.sharePct, 2) : "—"}
          tone={shareAvailable ? "gold" : "muted"}
          sub={
            hasSelectedData ? (
              <>
                {!shareAvailable && windowView.shareReason && (
                  <>
                    {windowView.shareReason}
                    <br />
                  </>
                )}
                {windowView.shareBasisLabel && (
                  <>
                    {windowView.shareBasisLabel}
                    <br />
                  </>
                )}
                빗썸 {money(analysis.summary.bithumb_volume_krw)}
                <br />
                국내 전체 {money(analysis.summary.domestic_volume_krw)}
                {windowView.amountsQualifier && (
                  <>
                    <br />
                    {windowView.amountsQualifier}
                  </>
                )}
              </>
            ) : (
              coverageLabel(
                context.selected_window_status,
                selectedWindowGap(analysis)
              )
            )
          }
        />
        <KpiCard
          label="빗썸 초당 거래금액"
          icon={<Activity className="h-4 w-4" />}
          value={
            turnoverSpeed
              ? `${money(turnoverSpeed.volume_per_sec_krw)}/초`
              : "—"
          }
          tone={turnoverSpeed ? "gold" : "muted"}
          sub={
            turnoverSpeed ? (
              <>
                모든 시간 탭에서 최근 10초 기준
                <br />
                국내 전체 {money(turnoverSpeed.market_volume_per_sec_krw)}/초 ·
                관측 {turnoverSpeed.rate_denominator_sec}초
              </>
            ) : (
              "최근 10초 공개 체결 자료 없음"
            )
          }
        />
        <KpiCard
          label="국내 체결 건수 중 빗썸 비중"
          icon={<Radio className="h-4 w-4" />}
          value={
            tradeShareAvailable
              ? percent(bithumbTrading?.trade_count_share_pct, 2)
              : "—"
          }
          tone="neutral"
          sub={
            tradeShareAvailable ? (
              <>
                최근 {windowLabel(context.window_sec)} 빗썸{" "}
                {bithumbTrading
                  ? `${bithumbTrading.trade_count.toLocaleString()}회`
                  : "—"}
                <br />
                국내 전체{" "}
                {analysis.summary.domestic_trade_count.toLocaleString()}회
                {windowView.amountsQualifier && (
                  <>
                    <br />
                    {windowView.amountsQualifier}
                  </>
                )}
              </>
            ) : (
              coverageLabel(
                context.selected_window_status,
                selectedWindowGap(analysis)
              )
            )
          }
        />
        <KpiCard
          label="국내 10단계 호가 잔량 중 빗썸 비중"
          icon={<Layers3 className="h-4 w-4" />}
          value={percent(bithumbLiquidityShare, 2)}
          tone={bithumbLiquidityShare === null ? "muted" : "gold"}
          sub={
            <>
              빗썸 매수·매도 10단계 잔량 합계
              <br />
              {money(bithumbDepth)}
            </>
          }
        />
      </section>

      <Panel title="거래소 현황" icon={<RadioTower className="h-4 w-4" />}>
        <div className="grid gap-3 p-3 xl:grid-cols-2">
          {DOMESTIC_EXCHANGES.slice(0, 2).map(exchange => (
            <ExchangeSummaryCard
              key={exchange}
              exchange={exchange}
              analysis={analysis}
              tickStore={tickStore}
              connection={connection}
            />
          ))}
        </div>
        <div className="grid items-start gap-3 border-t border-border p-3 lg:grid-cols-3">
          {DOMESTIC_EXCHANGES.slice(2).map(exchange => (
            <ExchangeSummaryCard
              key={exchange}
              exchange={exchange}
              analysis={analysis}
              tickStore={tickStore}
              connection={connection}
              collapsible
            />
          ))}
        </div>
      </Panel>

      <div className="grid items-start gap-4">
        <ShareBridge analysis={analysis} />
        <Panel
          title={
            <span className="inline-help-heading">
              주요 상승·하락 영향
              <InlineHelp
                text={
                  comparisonComplete
                    ? `직전 동일 구간과 비교한 코인별 결과입니다. 국내 5개 거래소 합계는 해당 코인 하나의 국내 거래금액이며, 그중 빗썸 금액도 이 합계에 포함됩니다. 전체 ${analysis.expert_brief.all_assets_in_change_attribution.toLocaleString()}개를 계산합니다.`
                    : `현재 ${analysis.summary.analyzed_asset_count.toLocaleString()}개 코인은 관측했지만, 국내 5개 거래소의 비교 구간이 확정되지 않아 코인별 변화 기여는 계산하지 않습니다.`
                }
              />
            </span>
          }
          className="min-h-[430px]"
          icon={<Activity className="h-4 w-4" />}
        >
          {comparisonComplete ? (
            <div className="grid grid-cols-1 divide-y divide-border/60 md:h-[344px] md:grid-cols-2 md:divide-x md:divide-y-0">
              {(
                [
                  [
                    "상승 영향",
                    analysis.expert_brief.positive_assets,
                    analysis.expert_brief.positive_asset_coverage_pct,
                  ],
                  [
                    "하락 영향",
                    analysis.expert_brief.negative_assets,
                    analysis.expert_brief.negative_asset_coverage_pct,
                  ],
                ] as const
              ).map(([label, rows, coverage]) => (
                <div key={label} className="asset-impact-scroll overflow-y-auto">
                  <div className="sticky top-0 z-10 flex items-center justify-between gap-2 border-b border-border bg-secondary px-3 py-2">
                    <strong className="ui-body">{label}</strong>
                    <span className="ui-caption text-right text-muted-foreground">
                      표시 코인이 {percent(coverage)} 설명
                    </span>
                  </div>
                  {rows.map(row => (
                    <article
                      key={row.asset}
                      className="market-asset-impact-card border-b border-border/60 px-3 py-3"
                    >
                      <div className="flex items-start justify-between gap-3">
                        <div className="flex min-w-0 items-center gap-2">
                          <strong className="ui-body shrink-0">
                            {row.asset}
                          </strong>
                          <span
                            className="ui-caption truncate border border-border px-1.5 py-0.5 text-muted-foreground"
                            title={domesticListingLabel(row.domestic_exchanges)}
                          >
                            {domesticListingLabel(row.domestic_exchanges)}
                          </span>
                        </div>
                        <div className="shrink-0 text-right">
                          <span className="ui-caption block text-muted-foreground">
                            점유율 변화 기여
                          </span>
                          <strong
                            className={`ui-body font-mono ${changeTone(row.contribution_pp)}`}
                          >
                            {points(row.contribution_pp)}
                          </strong>
                        </div>
                      </div>
                      <p className="asset-impact-copy ui-caption mt-2 text-foreground/90">
                        {assetImpactInterpretation(row)}
                      </p>
                      <dl className="mt-2 grid grid-cols-2 gap-px bg-border">
                        <div className="bg-card px-2 py-1.5">
                          <dt className="ui-caption text-muted-foreground">
                            해당 코인 국내 5개 거래소 합계
                          </dt>
                          <dd
                            className={`asset-impact-value ui-body mt-1 font-mono font-semibold ${changeTone(row.domestic_volume_change_krw)}`}
                          >
                            {formatKrwCompact(row.domestic_volume_change_krw)}
                          </dd>
                        </div>
                        <div className="bg-card px-2 py-1.5">
                          <dt className="ui-caption text-muted-foreground">
                            그중 빗썸
                          </dt>
                          <dd
                            className={`asset-impact-value ui-body mt-1 font-mono font-semibold ${changeTone(row.bithumb_volume_change_krw)}`}
                          >
                            {formatKrwCompact(row.bithumb_volume_change_krw)}
                          </dd>
                        </div>
                      </dl>
                    </article>
                  ))}
                  {!rows.length && (
                    <p className="p-4 text-center text-xs text-muted-foreground">
                      표시할 영향 없음
                    </p>
                  )}
                </div>
              ))}
            </div>
          ) : (
            <div className="flex h-[344px] items-center gap-3 px-5 py-4 text-sm text-muted-foreground">
              <AlertTriangle className="h-4 w-4 shrink-0 text-gold" />
              비교 구간 자료가 충분하지 않아 확정 변화 기여도는 표시하지
              않습니다.
            </div>
          )}
        </Panel>
      </div>

      <LiveOverviewCharts
        analysis={analysis}
        tickStore={tickStore}
        connection={connection}
        marketScope={marketScope}
        requestedWindowSec={requestedWindowSec}
      />

      <ShareChangeExplanation analysis={analysis} />

      <Panel
        title="분석 구간"
        description="저장된 이력과 현재 실시간 체결을 하나의 시간축으로 연결한 결과입니다."
        icon={<Gauge className="h-4 w-4" />}
      >
        <div className="grid divide-y divide-border/60 sm:grid-cols-5 sm:divide-x sm:divide-y-0">
          {analysis.momentum.map(row => {
            const status = row.status || row.coverage_status || "UNAVAILABLE";
            return (
              <div key={row.window} className="p-4 text-xs">
                <div className="flex items-center justify-between gap-3">
                  <span className="text-muted-foreground">
                    {windowLabel(row.duration_sec)}
                  </span>
                  {hasObservedMomentumRate(row) ? (
                    <span className="flex items-center gap-2">
                      <strong>{percent(row.share_pct, 2)}</strong>
                      {status !== "CONFIRMED" && (
                        <span className="text-base text-gold">
                          {coverageLabel(status, row.gap_seconds)}
                        </span>
                      )}
                    </span>
                  ) : (
                    <span className="text-base text-gold">
                      {coverageLabel(status, row.gap_seconds)}
                    </span>
                  )}
                </div>
                <div className="mt-3 h-1.5 bg-secondary">
                  <div
                    className="smooth-bar h-full bg-[var(--exchange-bithumb)]"
                    style={{
                      width: `${hasObservedMomentumRate(row) && row.share_pct !== null ? Math.min(100, row.share_pct) : 0}%`,
                    }}
                  />
                </div>
              </div>
            );
          })}
        </div>
      </Panel>

      <FinalMarketReport analysis={analysis} />
      <ExecutiveReportTable analysis={analysis} />
    </div>
  );
}

function SummaryMarketRail({
  analysis,
  compact = false,
}: {
  analysis: MarketShareAnalysis;
  compact?: boolean;
}) {
  // A share is a ratio and waits for the server to publish it. An exchange's
  // amount is an observation: it stays visible while the share is withheld,
  // unless that exchange's trade stream itself is not being received.
  const windowView = selectedWindowView(analysis);
  const available = windowView.showsShare;
  const amountsShown = windowView.showsAmounts;
  const [selectedExchange, setTargetExchange] = useState("bithumb");
  const [hoveredExchange, setHoveredExchange] = useState<string | null>(null);
  const exchangeRows = DOMESTIC_EXCHANGES.map(exchange => ({
    exchange,
    row: analysis.exchanges.find(candidate => candidate.exchange === exchange),
    observed: exchangeTradeStreamReady(analysis, exchange),
  }));
  let cumulativeShare = 0;
  const orbitRows = exchangeRows.map(entry => {
    const share =
      available &&
      entry.observed &&
      entry.row &&
      Number.isFinite(entry.row.share_pct)
        ? Math.max(0, Math.min(100, entry.row.share_pct ?? 0))
        : 0;
    const orbitRow = {
      ...entry,
      share,
      dashOffset: -cumulativeShare,
    };
    cumulativeShare += share;
    return orbitRow;
  });
  const activeExchange = hoveredExchange ?? selectedExchange;
  const activeEntry =
    orbitRows.find(entry => entry.exchange === activeExchange) ?? orbitRows[0];
  const activeColor = `var(--exchange-${activeEntry.exchange})`;

  const selectExchange = (exchange: string) => {
    if (isDomesticExchange(exchange)) {
      setTargetExchange(exchange);
    }
    setHoveredExchange(null);
  };

  return (
    <Panel
      title="거래소 점유율 구조"
      descriptionRole="status"
      description={
        available
          ? windowView.shareBasisLabel
            ? `${windowView.shareBasisLabel} · ${windowView.amountsQualifier}`
            : undefined
          : `${windowView.shareReason}${windowView.amountsQualifier ? ` ${windowView.amountsQualifier}` : ""}`
      }
      icon={<BarChart3 className="h-4 w-4" />}
      className={compact ? "summary-share-panel-compact" : undefined}
    >
      <div
        className={`summary-share-visual${compact ? " summary-share-visual-compact" : ""}`}
        style={
          {
            "--summary-share-active": activeColor,
          } as CSSProperties
        }
      >
        <div className="summary-share-orbit-stage">
          <div className="summary-share-orbit-chart">
            <div className="summary-share-orbit-halo" aria-hidden="true" />
            <svg
              className="summary-share-orbit-svg"
              viewBox="0 0 240 240"
              role="img"
              aria-label="국내 거래소 점유율 원형 분포"
            >
              <circle
                className="summary-share-orbit-guide"
                cx="120"
                cy="120"
                r="84"
                pathLength="100"
              />
              {orbitRows.map(
                ({ exchange, row, observed, share, dashOffset }) => {
                  const active = exchange === activeExchange;
                  const label = exchangeLabel(exchange);
                  return (
                    <circle
                      key={exchange}
                      className="summary-share-orbit-segment"
                      data-active={active ? "true" : undefined}
                      cx="120"
                      cy="120"
                      r="84"
                      pathLength="100"
                      stroke={`var(--exchange-${exchange})`}
                      strokeDasharray={`${share} ${Math.max(0, 100 - share)}`}
                      strokeDashoffset={dashOffset}
                      transform="rotate(-90 120 120)"
                      role="button"
                      tabIndex={0}
                      aria-label={`${label} ${available && observed && row ? percent(row.share_pct, 2) : "점유율 표시 안 함"}`}
                      aria-pressed={selectedExchange === exchange}
                      onPointerEnter={() => setHoveredExchange(exchange)}
                      onPointerLeave={() => setHoveredExchange(null)}
                      onFocus={() => setHoveredExchange(exchange)}
                      onBlur={() => setHoveredExchange(null)}
                      onClick={() => selectExchange(exchange)}
                      onKeyDown={event => {
                        if (event.key !== "Enter" && event.key !== " ") return;
                        event.preventDefault();
                        selectExchange(exchange);
                      }}
                    />
                  );
                }
              )}
            </svg>
            <div className="summary-share-orbit-core">
              <span
                className="summary-share-orbit-core-dot"
                style={{ backgroundColor: activeColor }}
              />
              <span className="summary-share-orbit-core-label">
                <ExchangeBrandLabel exchange={activeEntry.exchange} />
              </span>
              <strong>
                {available && activeEntry.observed && activeEntry.row
                  ? percent(activeEntry.row.share_pct, 2)
                  : "—"}
              </strong>
              <span className="summary-share-orbit-core-volume">
                {amountsShown && activeEntry.observed && activeEntry.row
                  ? money(activeEntry.row.volume_krw)
                  : exchangeAmountPlaceholder(activeEntry.observed)}
              </span>
            </div>
          </div>
          <div className="summary-share-orbit-caption">
            <span>거래소 분포</span>
            <strong>국내 거래소 분포</strong>
            <p>거래소를 누르면 상세가 열립니다</p>
          </div>
        </div>

        <div className="summary-share-explorer">
          <div className="summary-share-explorer-list">
            {orbitRows.map(({ exchange, row, observed, share }, index) => {
              const active = exchange === activeExchange;
              return (
                <button
                  key={exchange}
                  type="button"
                  className="summary-share-explorer-item"
                  data-active={active ? "true" : undefined}
                  aria-pressed={selectedExchange === exchange}
                  onPointerEnter={() => setHoveredExchange(exchange)}
                  onPointerLeave={() => setHoveredExchange(null)}
                  onFocus={() => setHoveredExchange(exchange)}
                  onBlur={() => setHoveredExchange(null)}
                  onClick={() => selectExchange(exchange)}
                  onKeyDown={event => {
                    if (event.key !== "Enter" && event.key !== " ") return;
                    event.preventDefault();
                    selectExchange(exchange);
                  }}
                  style={
                    {
                      "--summary-share-color": `var(--exchange-${exchange})`,
                      "--summary-share-value": `${share}%`,
                    } as CSSProperties
                  }
                >
                  <span className="summary-share-explorer-rank">
                    {String(index + 1).padStart(2, "0")}
                  </span>
                  <span className="summary-share-explorer-name">
                    <span className="summary-share-explorer-dot" />
                    <ExchangeBrandLabel exchange={exchange} />
                  </span>
                  <strong className="summary-share-explorer-value">
                    {available && observed && row
                      ? percent(row.share_pct, 2)
                      : "—"}
                  </strong>
                  <span
                    className="summary-share-explorer-bar"
                    aria-hidden="true"
                  >
                    <span />
                  </span>
                  <span className="summary-share-explorer-volume">
                    {amountsShown && observed && row
                      ? money(row.volume_krw)
                      : exchangeAmountPlaceholder(observed)}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      </div>
    </Panel>
  );
}

function SummaryChangeDecomposition({
  analysis,
}: {
  analysis: MarketShareAnalysis;
}) {
  const attribution = analysis.change_attribution;
  const complete = comparisonValuesAvailable(analysis);
  const currentAvailable = selectedWindowValuesAvailable(analysis);
  const rows = [
    {
      key: "bithumb",
      label: "빗썸",
      previous: attribution.previous_bithumb_volume_krw,
      current: attribution.current_bithumb_volume_krw,
      change: attribution.bithumb_volume_change_krw,
      effect: attribution.bithumb_direct_effect_pp,
    },
    {
      key: "competitors",
      label: "경쟁 거래소 합계",
      previous: attribution.previous_competitor_volume_krw,
      current: attribution.current_competitor_volume_krw,
      change: attribution.competitor_volume_change_krw,
      effect: attribution.competitor_effect_pp,
    },
  ];
  return (
    <Panel
      title="점유율 변화 계산"
      descriptionRole={complete ? "help" : "status"}
      description={
        complete
          ? "현재와 직전 동일 구간의 거래금액을 빗썸과 경쟁 거래소로 나눠 계산합니다. 두 효과의 합은 점유율 변화와 같으며, 원인을 확정하지 않습니다."
          : currentAvailable
            ? `현재 관측 거래금액은 표시합니다. ${comparisonUnavailableMessage(analysis)}`
            : comparisonUnavailableMessage(analysis)
      }
      icon={<Activity className="h-4 w-4" />}
    >
      <div className="summary-source-table">
        <div className="summary-source-head">
          <span>계산 항목</span>
          <span>직전 동일 구간</span>
          <span>현재 구간</span>
          <span>거래금액 증감</span>
          <span>점유율 변화 기여</span>
        </div>
        {rows.map(row => (
          <div key={row.key} className="summary-source-row">
            <strong>{row.label}</strong>
            <span>{complete ? money(row.previous) : "—"}</span>
            <span>{currentAvailable ? money(row.current) : "—"}</span>
            <span
              className={
                complete ? changeTone(row.change) : "text-muted-foreground"
              }
            >
              {complete ? moneyChange(row.change) : "—"}
            </span>
            <strong
              className={
                complete ? changeTone(row.effect) : "text-muted-foreground"
              }
            >
              {complete ? points(row.effect) : "—"}
            </strong>
          </div>
        ))}
        <div className="summary-source-total">
          <strong>{comparisonScopeLabel(analysis)} 합계</strong>
          <span>{complete ? money(attribution.previous_volume_krw) : "—"}</span>
          <span>
            {currentAvailable ? money(attribution.current_volume_krw) : "—"}
          </span>
          <span
            className={
              complete
                ? changeTone(attribution.market_volume_change_krw)
                : "text-muted-foreground"
            }
          >
            {complete ? moneyChange(attribution.market_volume_change_krw) : "—"}
          </span>
          <strong
            className={
              complete
                ? changeTone(attribution.share_change_pp)
                : "text-muted-foreground"
            }
          >
            {complete ? points(attribution.share_change_pp) : "—"}
          </strong>
        </div>
      </div>
    </Panel>
  );
}

function SummaryTimePersistence({
  analysis,
  showMinuteRecovery = false,
}: {
  analysis: MarketShareQualityAnalysis;
  showMinuteRecovery?: boolean;
}) {
  const source = momentumSourcePresentation(analysis.momentum);
  const recovery = minuteRecoveryPresentation(
    analysis.context.data_source,
    analysis.context.window_sec
  );
  return (
    <Panel
      title="구간별 흐름"
      description={MOMENTUM_SOURCE_HELP}
      icon={<Radio className="h-4 w-4" />}
    >
      <div className="summary-momentum-source">
        <strong>{source.title}</strong>
        <span>{source.detail}</span>
      </div>
      {showMinuteRecovery && (
        <div
          className="border-b border-border px-4 py-3 text-sm"
          data-minute-recovery={recovery.kind}
          role="status"
        >
          <div className="mobile-help-heading">
            <strong>
              선택 구간 분봉 보완 · {windowLabel(analysis.context.window_sec)}
            </strong>
            <InlineHelp
              text={`${recovery.detail} 종목별 분봉 건수는 거래소·종목·분 조합의 수이며, 복구된 시간이나 체결 건수가 아닙니다.`}
            />
          </div>
          <p className="mt-2 text-muted-foreground">{recovery.label}</p>
          {recovery.volumeKRW !== null && (
            <p className="mt-1 break-words tabular-nums">
              합계에 반영한 금액{" "}
              {recovery.volumeKRW === 0 ? "0원" : recovery.volumeKRW < 1 ? "1원 미만" : money(recovery.volumeKRW)}{" "}
              · 종목별 분봉 {recovery.rowCount?.toLocaleString("ko-KR")}건
              {recovery.exchanges.length > 0
                ? ` · ${recovery.exchanges.map(exchangeLabel).join("·")}`
                : ""}
            </p>
          )}
          <p className="mt-1 text-muted-foreground">
            실시간 체결 관측 상태는 분봉 복구 금액과 별도로 확인합니다.
          </p>
        </div>
      )}
      <div className="summary-momentum-list">
        {analysis.momentum.map(row => {
          const status = row.status || row.coverage_status || "UNAVAILABLE";
          const available = hasObservedMomentumRate(row);
          const selected = row.duration_sec === analysis.context.window_sec;
          const supplemented = row.data_source?.official_minute_supplement_used;
          const officialTicker = row.data_source?.official_24h_ticker_used;
          const rateLabel = officialTicker
            ? "공식값 초당"
            : supplemented
              ? "보완값 초당"
              : status === "CONFIRMED"
                ? "초당"
                : "관측값 초당";
          return (
            <div
              key={row.window}
              className={`summary-momentum-row ${selected ? "summary-momentum-row-selected" : ""}`}
            >
              <div className="summary-momentum-heading">
                <strong>{windowLabel(row.duration_sec)}</strong>
                <span
                  className={`summary-status ${status === "CONFIRMED" ? "summary-status-live" : ""}`}
                >
                  {momentumCoverageLabel(row)}
                </span>
                <strong className="tabular-nums">
                  {available ? percent(row.share_pct, 2) : "—"}
                </strong>
              </div>
              <div className="summary-momentum-track" aria-hidden="true">
                <span
                  className="summary-momentum-fill"
                  style={{
                    width: `${available ? Math.max(0, Math.min(100, row.share_pct ?? 0)) : 0}%`,
                  }}
                />
              </div>
              <div className="summary-momentum-speed">
                <span>
                  빗썸{" "}
                  {available
                    ? `${rateLabel} ${money(row.volume_per_sec_krw)}`
                    : "—"}
                </span>
                <span>
                  국내{" "}
                  {available
                    ? `${rateLabel} ${money(row.market_volume_per_sec_krw)}`
                    : "—"}
                </span>
              </div>
            </div>
          );
        })}
      </div>
      <OfficialAlignedHistoryExplorer analysis={analysis} />
    </Panel>
  );
}

const OFFICIAL_HISTORY_TIME_FORMAT = new Intl.DateTimeFormat("ko-KR", {
  timeZone: "Asia/Seoul",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});

function officialHistoryTimestamp(valueMS: number): string {
  if (!Number.isFinite(valueMS) || valueMS <= 0) return "—";
  return OFFICIAL_HISTORY_TIME_FORMAT.format(new Date(valueMS));
}

let retainedOfficialHistoryInput = "";
let retainedOfficialHistoryQueryToMS: number | null = null;
let retainedOfficialHistorySelectedMS: number | null = null;

function OfficialAlignedHistoryExplorer({
  analysis,
}: {
  analysis: MarketShareQualityAnalysis;
}) {
  const latestEndpointMS =
    analysis.official_ticker_1m.current?.bucket_ms ??
    latestSafeOfficialHourMS(Date.now());
  const initialQueryToMS =
    retainedOfficialHistoryQueryToMS !== null &&
    retainedOfficialHistoryQueryToMS <= latestEndpointMS
      ? retainedOfficialHistoryQueryToMS
      : latestEndpointMS;
  const [inputValue, setInputValue] = useState(
    () =>
      retainedOfficialHistoryInput ||
      officialHistoryKSTInputValue(initialQueryToMS)
  );
  const [queryToMS, setQueryToMS] = useState(initialQueryToMS);
  const [selectedMS, setSelectedMS] = useState(
    retainedOfficialHistorySelectedMS ?? initialQueryToMS
  );
  const [history, setHistory] =
    useState<MarketShareOfficialAlignedHistoryResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    getOfficialAlignedHistory(
      queryToMS - OFFICIAL_ALIGNED_HISTORY_WINDOW_MS,
      queryToMS,
      controller.signal
    )
      .then(payload => {
        setHistory(payload);
        setSelectedMS(queryToMS);
        retainedOfficialHistorySelectedMS = queryToMS;
      })
      .catch(reason => {
        if (controller.signal.aborted) return;
        setError(
          reason instanceof Error
            ? reason.message
            : "공식 시간봉 이력을 불러오지 못했습니다."
        );
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [queryToMS]);

  const selectedPoint: MarketShareOfficialTicker1mPoint | null =
    history?.items.find(item => item.bucket_ms === selectedMS) ?? null;

  function selectRangeEndpoint(): void {
    const parsed = parseOfficialHistoryKSTInput(inputValue);
    if (parsed === null) {
      setError("기준 시각은 한국시간 정각으로 선택해 주세요.");
      return;
    }
    if (parsed > latestEndpointMS) {
      setError("아직 마감되지 않은 미래 시간은 조회할 수 없습니다.");
      return;
    }
    retainedOfficialHistoryInput = inputValue;
    retainedOfficialHistoryQueryToMS = parsed;
    retainedOfficialHistorySelectedMS = parsed;
    setQueryToMS(parsed);
  }

  function selectLatest(): void {
    const latestInput = officialHistoryKSTInputValue(latestEndpointMS);
    retainedOfficialHistoryInput = latestInput;
    retainedOfficialHistoryQueryToMS = latestEndpointMS;
    retainedOfficialHistorySelectedMS = latestEndpointMS;
    setInputValue(latestInput);
    setQueryToMS(latestEndpointMS);
    setSelectedMS(latestEndpointMS);
    setError(null);
  }

  return (
    <section className="summary-official-history" aria-label="공식 시간봉 이력">
      <div className="summary-official-history-header">
        <div>
          <strong>공식 시간봉 기록</strong>
          <span>업비트·빗썸·코인원 공식 1시간봉 · 각 시각까지 24시간</span>
        </div>
        <span
          className={`summary-status ${history?.status === "LIVE" ? "summary-status-live" : ""}`}
        >
          {loading ? "불러오는 중" : officialHistorySampleCoverage(history)}
        </span>
      </div>

      <form
        className="summary-official-history-controls"
        onSubmit={event => {
          event.preventDefault();
          selectRangeEndpoint();
        }}
      >
        <label htmlFor="official-history-at">기준 시각 (한국시간)</label>
        <input
          id="official-history-at"
          type="datetime-local"
          step={3600}
          value={inputValue}
          max={officialHistoryKSTInputValue(latestEndpointMS)}
          onChange={event => {
            retainedOfficialHistoryInput = event.target.value;
            setInputValue(event.target.value);
          }}
        />
        <button type="submit">24시간 보기</button>
        <button type="button" onClick={selectLatest}>
          최신
        </button>
      </form>

      {error && <p className="summary-official-history-error">{error}</p>}

      <div className="summary-official-history-metrics">
        <div>
          <span>선택 시각</span>
          <strong>{officialHistoryTimestamp(selectedMS)}</strong>
        </div>
        <div>
          <span>24시간 빗썸 점유율</span>
          <strong>{percent(selectedPoint?.bithumb_share_pct, 2)}</strong>
        </div>
        <div>
          <span>빗썸 24시간 · 초당 평균</span>
          <strong>
            {selectedPoint
              ? `${money(selectedPoint.bithumb_volume_krw)} · ${money(selectedPoint.bithumb_volume_krw / 86_400)}`
              : "—"}
          </strong>
        </div>
        <div>
          <span>국내 3개 24시간 · 초당 평균</span>
          <strong>
            {selectedPoint
              ? `${money(selectedPoint.total_volume_krw)} · ${money(selectedPoint.total_volume_krw / 86_400)}`
              : "—"}
          </strong>
        </div>
      </div>

      {history && history.items.length > 0 && (
        <div className="summary-official-history-points">
          {history.items.map(item => (
            <button
              key={item.bucket_ms}
              type="button"
              className={
                item.bucket_ms === selectedMS
                  ? "summary-official-history-point summary-official-history-point-selected"
                  : "summary-official-history-point"
              }
              onClick={() => {
                retainedOfficialHistorySelectedMS = item.bucket_ms;
                setSelectedMS(item.bucket_ms);
              }}
              title={`${officialHistoryTimestamp(item.bucket_ms)} · ${percent(item.bithumb_share_pct, 2)}`}
            >
              <span>{officialHistoryTimestamp(item.bucket_ms)}</span>
              <strong>{percent(item.bithumb_share_pct, 2)}</strong>
            </button>
          ))}
        </div>
      )}

      <p className="summary-official-history-note">
        위 실시간 값과 거래소 범위·수집 방식이 달라 직접 합산하지 않습니다. 아래
        값은 선택한 정각까지의 최근 24시간 공식 거래금액입니다.
      </p>
    </section>
  );
}

const SHARE_HEATMAP_INTERVAL_MS = 10_000;
const SHARE_HEATMAP_CELL_COUNT = 50;

type ShareHeatmapCell = {
  bucketMS: number;
  sharePct: number | null;
  changePP: number | null;
};

type SummaryUpdateMotion = {
  direction: "up" | "down" | "flat";
};

function useSummaryUpdateMotion(
  analysis: MarketShareAnalysis
): SummaryUpdateMotion | null {
  const [motion, setMotion] = useState<SummaryUpdateMotion | null>(null);
  const previousRef = useRef<{
    contextKey: string;
    lastConfirmedBucketMS: number | null;
  } | null>(null);
  const contextKey = `${analysis.context.market_scope}:${analysis.context.window_sec}`;
  const comparisonConfirmed = comparisonValuesAvailable(analysis);
  const bucketMS =
    Math.floor((analysis.context.as_of * 1_000) / SHARE_HEATMAP_INTERVAL_MS) *
    SHARE_HEATMAP_INTERVAL_MS;
  const shareChange = analysis.change_attribution.share_change_pp;
  const direction =
    typeof shareChange !== "number" || shareChange === 0
      ? "flat"
      : shareChange > 0
        ? "up"
        : "down";

  useEffect(() => {
    const previous = previousRef.current;
    if (!previous || previous.contextKey !== contextKey) {
      previousRef.current = {
        contextKey,
        lastConfirmedBucketMS: comparisonConfirmed ? bucketMS : null,
      };
      setMotion(null);
      return;
    }
    if (!comparisonConfirmed) return;
    if (
      previous.lastConfirmedBucketMS !== null &&
      bucketMS > previous.lastConfirmedBucketMS
    ) {
      setMotion({ direction });
    }
    previousRef.current = { contextKey, lastConfirmedBucketMS: bucketMS };
  }, [bucketMS, comparisonConfirmed, contextKey, direction]);

  return motion;
}

function appendShareHeatmapCell(
  current: ShareHeatmapCell[],
  bucketMS: number,
  sharePct: number,
  changePP: number
): ShareHeatmapCell[] {
  const previous = current.at(-1);
  if (previous?.bucketMS === bucketMS) {
    return [...current.slice(0, -1), { bucketMS, sharePct, changePP }].slice(
      -SHARE_HEATMAP_CELL_COUNT
    );
  }

  const next = [...current];
  if (previous && bucketMS > previous.bucketMS + SHARE_HEATMAP_INTERVAL_MS) {
    const missingCount = Math.min(
      SHARE_HEATMAP_CELL_COUNT,
      Math.floor((bucketMS - previous.bucketMS) / SHARE_HEATMAP_INTERVAL_MS) - 1
    );
    for (let index = missingCount; index > 0; index--) {
      next.push({
        bucketMS: bucketMS - index * SHARE_HEATMAP_INTERVAL_MS,
        sharePct: null,
        changePP: null,
      });
    }
  }
  next.push({
    bucketMS,
    sharePct,
    changePP,
  });
  return next.slice(-SHARE_HEATMAP_CELL_COUNT);
}

function BithumbShareHeatmap({
  analysis,
  updateMotion,
}: {
  analysis: MarketShareAnalysis;
  updateMotion: SummaryUpdateMotion | null;
}) {
  const [cells, setCells] = useState<ShareHeatmapCell[]>([]);
  const marketScope = analysis.context.market_scope;
  const windowSec = analysis.context.window_sec;

  useEffect(() => setCells([]), [marketScope, windowSec]);
  useEffect(() => {
    if (
      !comparisonValuesAvailable(analysis) ||
      analysis.change_attribution.share_change_pp === null ||
      !Number.isFinite(analysis.change_attribution.share_change_pp) ||
      analysis.summary.bithumb_share_pct === null ||
      !Number.isFinite(analysis.summary.bithumb_share_pct)
    )
      return;
    const bucketMS =
      Math.floor((analysis.context.as_of * 1_000) / SHARE_HEATMAP_INTERVAL_MS) *
      SHARE_HEATMAP_INTERVAL_MS;
    setCells(current =>
      appendShareHeatmapCell(
        current,
        bucketMS,
        analysis.summary.bithumb_share_pct as number,
        analysis.change_attribution.share_change_pp as number
      )
    );
  }, [analysis]);

  const displayCells: Array<ShareHeatmapCell | null> = [
    ...cells,
    ...Array<null>(Math.max(0, SHARE_HEATMAP_CELL_COUNT - cells.length)).fill(
      null
    ),
  ];
  return (
    <section
      className="summary-share-heatmap"
      aria-label="빗썸 점유율 10초 히트맵"
    >
      <header className="summary-share-heatmap-header">
        <div>
          <strong>빗썸 점유율 흐름</strong>
          <span>
            {cells.length
              ? `10초 × ${SHARE_HEATMAP_CELL_COUNT}칸`
              : "국내 5개 거래소 비교 대기"}
          </span>
        </div>
        {cells.length > 0 && (
          <div className="summary-share-heatmap-legend" aria-label="색상 범례">
            <span>
              <i data-tone="down" />
              하락
            </span>
            <span>
              <i data-tone="flat" />
              보합
            </span>
            <span>
              <i data-tone="up" />
              상승
            </span>
          </div>
        )}
      </header>
      {cells.length > 0 ? (
        <div className="summary-share-heatmap-grid">
          {displayCells.map((cell, index) => {
            const latest = Boolean(cell && index === cells.length - 1);
            const tone =
              !cell || cell.sharePct === null
                ? "empty"
                : cell.changePP === null || cell.changePP === 0
                  ? "flat"
                  : cell.changePP > 0
                    ? "up"
                    : "down";
            const title = !cell
              ? "아직 관측되지 않은 10초 구간"
              : cell.sharePct === null
                ? `${reportTimestamp(cell.bucketMS / 1_000)} · 자료 없음`
                : `${reportTimestamp(cell.bucketMS / 1_000)} · 빗썸 ${percent(cell.sharePct, 2)} · ${points(cell.changePP)}`;
            return (
              <span
                key={cell ? `${cell.bucketMS}` : `empty-${index}`}
                className="summary-share-heatmap-cell"
                data-tone={tone}
                data-latest={latest ? "true" : undefined}
                data-update-direction={
                  latest ? updateMotion?.direction : undefined
                }
                title={title}
                aria-label={title}
              />
            );
          })}
        </div>
      ) : (
        <p className="px-4 pb-4 text-xs text-muted-foreground">
          지금은 현재 점유율만 확인할 수 있습니다. 국내 5개 거래소의 이전 10초
          구간까지 확인되면 상승·하락 흐름이 나타납니다.
        </p>
      )}
    </section>
  );
}

function SummaryExchangeCards({ analysis }: { analysis: MarketShareAnalysis }) {
  const previousByExchange = new Map(
    analysis.change_attribution.exchanges.map(row => [row.exchange, row])
  );
  const windowView = selectedWindowView(analysis);
  const shareAvailable = windowView.showsShare;
  const comparisonReady = comparisonValuesAvailable(analysis);
  const excludedFromComparison = new Set(
    analysis.change_attribution.excluded_exchanges ?? []
  );
  return (
    <Panel
      title="거래소 현황 요약"
      descriptionRole="status"
      description={
        [
          comparisonReady ? "" : comparisonUnavailableMessage(analysis),
          windowView.shareBasisNote,
        ]
          .filter(Boolean)
          .join(" ") || undefined
      }
      icon={<RadioTower className="h-4 w-4" />}
    >
      <div className="summary-exchange-grid">
        {DOMESTIC_EXCHANGES.map(exchange => {
          const current = analysis.exchanges.find(
            row => row.exchange === exchange
          );
          const change = previousByExchange.get(exchange);
          const collector = analysis.collectors[exchange];
          const volumeChange = change?.volume_change_krw ?? null;
          const tradeActivityFresh =
            analysis.context.market_scope === "KRW"
              ? collector?.krw_trade_activity_fresh
              : collector?.trade_activity_fresh;
          const tradeStreamReady =
            analysis.context.market_scope === "KRW"
              ? collector?.krw_trade_stream_ready
              : collector?.trade_stream_ready;
          const orderbookStatus = collector?.orderbook_stream_ready
            ? "호가 실시간"
            : collector?.fresh_orderbook_market_count
              ? "호가 일부"
              : "호가 확인 필요";
          const tradeStatus = tradeActivityFresh
            ? "체결 실시간"
            : tradeStreamReady
              ? "체결 활동 없음"
              : "체결 확인 필요";
          return (
            <article key={exchange} className="summary-exchange-card">
              <header className="flex items-start justify-between gap-3">
                <div className="flex min-w-0 items-center gap-2">
                  <span
                    className="summary-rail-dot"
                    style={{ backgroundColor: `var(--exchange-${exchange})` }}
                  />
                  <h4 className="truncate text-base font-semibold">
                    <ExchangeBrandLabel exchange={exchange} />
                  </h4>
                </div>
                <strong className="text-lg tabular-nums">
                  {shareAvailable && tradeStreamReady
                    ? percent(current?.share_pct, 2)
                    : "—"}
                </strong>
              </header>
              <div className="mt-2 flex flex-wrap gap-1.5 text-base">
                <span
                  className={
                    tradeActivityFresh
                      ? "summary-status summary-status-live"
                      : "summary-status"
                  }
                >
                  {tradeStatus}
                </span>
                <span
                  className={
                    collector?.orderbook_stream_ready
                      ? "summary-status summary-status-live"
                      : "summary-status"
                  }
                >
                  {orderbookStatus}
                </span>
              </div>
              <dl className="summary-exchange-metrics mt-3">
                <div>
                  <dt>거래금액</dt>
                  <dd>
                    {current && tradeStreamReady
                      ? money(current.volume_krw)
                      : "—"}
                  </dd>
                </div>
                {comparisonReady &&
                change &&
                !excludedFromComparison.has(exchange) ? (
                  <>
                    <div>
                      <dt>직전 구간보다 거래금액</dt>
                      <dd className={changeTone(volumeChange)}>
                        {formatKrwCompact(change.volume_change_krw)}
                      </dd>
                    </div>
                    <div>
                      <dt>점유율 변화</dt>
                      <dd className={changeTone(change.share_change_pp)}>
                        {points(change.share_change_pp, 2)}
                      </dd>
                    </div>
                    <div>
                      <dt>빗썸 점유율 변화에 미친 몫</dt>
                      <dd className={changeTone(change.contribution_pp)}>
                        {points(change.contribution_pp, 2)}
                      </dd>
                    </div>
                  </>
                ) : null}
              </dl>
            </article>
          );
        })}
      </div>
    </Panel>
  );
}

function SummaryDrivers({ analysis }: { analysis: MarketShareAnalysis }) {
  const currentAssets = new Map(
    analysis.assets.map(asset => [asset.asset, asset])
  );
  const attributionReady = comparisonValuesAvailable(analysis);
  const comparisonMessage = comparisonUnavailableMessage(analysis);
  const groups = [
    [
      "상승 쪽 기여",
      analysis.expert_brief.positive_assets,
      analysis.expert_brief.positive_asset_coverage_pct,
    ],
    [
      "하락 쪽 기여",
      analysis.expert_brief.negative_assets,
      analysis.expert_brief.negative_asset_coverage_pct,
    ],
  ] as const;
  return (
    <Panel
      title="주요 자산 기여"
      descriptionRole={attributionReady ? "help" : "status"}
      description={
        attributionReady
          ? `상승·하락 방향별 점유율 변화 기여가 큰 자산을 보여줍니다. 전체 ${analysis.expert_brief.all_assets_in_change_attribution.toLocaleString()}개 자산을 계산하고, 각 방향의 80%를 설명하는 항목을 표시합니다.`
          : analysis.context.selected_window_status === "UNAVAILABLE"
            ? comparisonMessage
            : `현재 ${analysis.assets.length.toLocaleString()}개 자산의 관측값은 있지만 ${comparisonMessage}`
      }
      className="summary-drivers-panel min-h-[430px]"
      icon={<Coins className="h-4 w-4" />}
    >
      <div className="summary-driver-columns">
        {groups.map(([label, rows, coverage]) => (
          <section key={label} className="summary-driver-column">
            <header className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
              <strong>{label}</strong>
              <span className="text-xs text-muted-foreground">
                누적 {percent(coverage, 1)}
              </span>
            </header>
            {rows.map(row => (
              <article key={row.asset} className="summary-driver-row">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <strong>{row.asset}</strong>
                    <span className="summary-action-badge">
                      {summaryActionLabel(currentAssets.get(row.asset), row)}
                    </span>
                  </div>
                  <p className="mt-1 truncate text-base text-muted-foreground">
                    {domesticListingLabel(row.domestic_exchanges)}
                  </p>
                </div>
                <div className="text-right">
                  <strong className={changeTone(row.contribution_pp)}>
                    {points(row.contribution_pp)}
                  </strong>
                  <p className="mt-1 text-base text-muted-foreground">
                    점유율 변화 기여
                  </p>
                </div>
                <div className="summary-driver-volume-split">
                  <span className={changeTone(row.bithumb_volume_change_krw)}>
                    빗썸 증감 {formatKrwCompact(row.bithumb_volume_change_krw)}
                  </span>
                  <span
                    className={changeTone(row.competitor_volume_change_krw)}
                  >
                    경쟁 증감{" "}
                    {formatKrwCompact(row.competitor_volume_change_krw)}
                  </span>
                  <span className={changeTone(row.domestic_volume_change_krw)}>
                    국내 증감 {formatKrwCompact(row.domestic_volume_change_krw)}
                  </span>
                </div>
                <p className="summary-driver-interpretation">
                  {assetImpactInterpretation(row)}
                </p>
                <div className="summary-driver-capture">
                  <span>코인 거래금액 중 빗썸 비중</span>
                  <strong>
                    {percent(row.previous_bithumb_capture_pct, 1)} →{" "}
                    {percent(row.current_bithumb_capture_pct, 1)}
                  </strong>
                </div>
              </article>
            ))}
            {!rows.length && (
              <p className="p-5 text-center text-xs text-muted-foreground">
                {comparisonValuesAvailable(analysis)
                  ? "표시할 기여 없음"
                  : comparisonMessage}
              </p>
            )}
          </section>
        ))}
      </div>
    </Panel>
  );
}

function SummaryEvidence({ analysis }: { analysis: MarketShareAnalysis }) {
  const layers = [
    {
      layer: analysis.evidence_layers.measured,
      shortLabel: "변화",
      title: "이전 구간과 비교해 확인된 변화",
      description:
        "점유율과 거래금액이 직전 같은 길이의 구간보다 얼마나 달라졌는지 보여줍니다.",
      valueLabel: "변화",
    },
    {
      layer: analysis.evidence_layers.diagnostic,
      shortLabel: "코인",
      title: "지금 눈에 띄는 코인",
      description:
        "현재 구간에서 거래가 특정 거래소에 몰린 코인을 쉬운 문장으로 정리했습니다.",
      valueLabel: "핵심 수치",
    },
  ].filter(({ layer }) => layer.items.length > 0);
  const visibleChecks = analysis.expert_brief.checks.filter(
    check =>
      check.status !== "UNAVAILABLE" &&
      check.value !== null &&
      Number.isFinite(check.value)
  );
  const findingCount = layers.reduce(
    (total, { layer }) => total + layer.items.length,
    0
  );
  const checkCount = visibleChecks.length;
  const totalCount = findingCount + checkCount;
  const comparisonReady = comparisonValuesAvailable(analysis);
  return (
    <Panel
      title="계산·진단 근거"
      description="지금 확인할 수 있는 내용만 쉬운 문장으로 정리합니다. 값이 없는 분석은 표시하지 않습니다."
      icon={<Gauge className="h-4 w-4" />}
      actions={
        <div className="flex items-center gap-3">
          <a
            href="#summary2"
            className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
          >
            공지·반응 보기
          </a>
          <span className="text-xs text-muted-foreground">
            확인된 내용 {totalCount}개
          </span>
        </div>
      }
    >
      <div className="summary-evidence-guide">
        <div className="summary-evidence-boundary">
          <AlertTriangle className="h-4 w-4" aria-hidden="true" />
          <div>
            <strong>
              {comparisonReady
                ? "현재 구간과 이전 구간을 비교했습니다"
                : "현재 구간만 확인했습니다"}
            </strong>
            <p>
              {comparisonReady
                ? "아래 숫자는 계산 결과이며, 상승·하락의 원인을 증명하지는 않습니다."
                : "이전 구간이 필요한 변화 분석은 숨겼습니다. 아래에는 지금 확인 가능한 거래와 호가 특징만 표시합니다."}
            </p>
          </div>
        </div>
      </div>
      <div className="summary-evidence-stack">
        {layers.map(
          ({ layer, shortLabel, title, description, valueLabel }, index) => (
            <section
              key={layer.level}
              className="summary-evidence-layer"
              data-level={layer.level.toLowerCase()}
            >
              <header className="summary-evidence-layer-header">
                <span className="summary-evidence-layer-step">
                  {String(index + 1).padStart(2, "0")}
                </span>
                <div className="min-w-0">
                  <span className="summary-evidence-layer-kicker">
                    {shortLabel}
                  </span>
                  <strong>{title}</strong>
                  <p>{description}</p>
                </div>
                <div className="summary-evidence-layer-count">
                  <strong>{layer.items.length.toLocaleString()}</strong>
                  <span>항목</span>
                </div>
              </header>
              {layer.items.map(finding => {
                const copy = readableFindingCopy(finding);
                return (
                  <article
                    key={`${layer.level}-${finding.kind}-${finding.asset ?? finding.title}`}
                    className="summary-evidence-item"
                  >
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span
                          className={`ui-caption inline-flex border px-2 py-1 ${EVIDENCE_TONES[finding.evidence_level]}`}
                        >
                          현재 관측
                        </span>
                        <h4 className="text-sm font-semibold">{copy.title}</h4>
                      </div>
                      <p className="mt-1 text-xs leading-6 text-muted-foreground">
                        {copy.detail}
                      </p>
                      <EvidenceBasis basis={readableBasis(finding.basis)} />
                    </div>
                    <div className="summary-evidence-metric">
                      <span>{valueLabel}</span>
                      <strong className="summary-evidence-value">
                        {findingMetric(finding) || "—"}
                      </strong>
                    </div>
                  </article>
                );
              })}
            </section>
          )
        )}
        {checkCount > 0 && (
          <section className="summary-evidence-layer" data-level="cross-check">
            <header className="summary-evidence-layer-header">
              <span className="summary-evidence-layer-step">
                {String(layers.length + 1).padStart(2, "0")}
              </span>
              <div className="min-w-0">
                <span className="summary-evidence-layer-kicker">시장</span>
                <strong>빗썸과 다른 거래소의 차이</strong>
                <p>체결 건수·거래금액·호가에서 눈에 띄는 차이만 보여줍니다.</p>
              </div>
              <div className="summary-evidence-layer-count">
                <strong>{checkCount.toLocaleString()}</strong>
                <span>항목</span>
              </div>
            </header>
            {visibleChecks.map(check => {
              const copy = readableCheckCopy(check);
              return (
                <article key={check.code} className="summary-evidence-item">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="summary-action-badge">현재 비교</span>
                      <h4 className="text-sm font-semibold">{copy.title}</h4>
                    </div>
                    <p className="mt-1 text-xs leading-6 text-muted-foreground">
                      {copy.detail}
                    </p>
                    <EvidenceBasis basis={readableBasis(check.basis)} />
                  </div>
                  <div className="summary-evidence-metric">
                    <span>차이</span>
                    <strong className="summary-evidence-value">
                      {diagnosticMetric(check.value, check.unit)}
                    </strong>
                  </div>
                </article>
              );
            })}
          </section>
        )}
        {totalCount === 0 && (
          <p className="p-5 text-sm text-muted-foreground">
            지금 바로 설명할 수 있는 비교 결과가 없습니다. 현재 점유율은 위
            거래소 현황에서 확인할 수 있습니다.
          </p>
        )}
      </div>
    </Panel>
  );
}

function SummaryHeadline({
  analysis,
  hideIncomplete = false,
  updateMotion,
}: {
  analysis: MarketShareAnalysis;
  hideIncomplete?: boolean;
  updateMotion: SummaryUpdateMotion | null;
}) {
  const attribution = analysis.change_attribution;
  const comparisonComplete = comparisonValuesAvailable(analysis);
  const currentShare = analysis.summary.bithumb_share_pct;
  const quality = comparisonCoverageQualifier(analysis);
  const comparisonMessage = comparisonUnavailableMessage(analysis);
  const currentValuesAvailable = selectedWindowValuesAvailable(analysis);
  const windowView = selectedWindowView(analysis);
  const currentObservationMessage = !currentValuesAvailable
    ? comparisonMessage
    : `현재 선택 구간의 거래금액만 표시합니다. ${comparisonMessage}`;
  const change = comparisonComplete ? attribution.share_change_pp : null;
  // A share the server withheld is explained, not called uncalculable, and the
  // amounts that were observed stay visible with their coverage.
  const currentShareSummary = windowView.showsShare
    ? `빗썸 현재 관측 점유율은 ${percent(windowView.sharePct, 2)}입니다.`
    : `${windowLabel(analysis.context.window_sec)} 빗썸 점유율을 표시하지 않습니다. ${windowView.shareReason}`;
  // A share that covers only the observed range says so next to the amounts.
  const observedRangeNote = windowView.shareBasisNote;
  const observedAmountsLine = windowView.showsAmounts
    ? `관측한 거래금액 빗썸 ${money(analysis.summary.bithumb_volume_krw)} · 국내 ${money(analysis.summary.domestic_volume_krw)}`
    : "";
  const contributionDrivers = [
    {
      key: "positive",
      label: "상승 기여",
      direction: "up",
      driver: analysis.expert_brief.positive_assets[0],
    },
    {
      key: "negative",
      label: "하락 기여",
      direction: "down",
      driver: analysis.expert_brief.negative_assets[0],
    },
  ];
  const competitorEffect =
    attribution.competitor_effect_pp ??
    attribution.domestic_market_size_effect_pp;
  const [
    animatedCurrentShare,
    animatedChange,
    animatedBithumbEffect,
    animatedCompetitorEffect,
  ] = useAnimatedMetrics(
    [
      currentShare,
      change,
      attribution.bithumb_direct_effect_pp,
      competitorEffect,
    ],
    2,
    420,
    32,
    true
  );
  const directionTone =
    change === null
      ? "neutral"
      : change > 0
        ? "up"
        : change < 0
          ? "down"
          : "neutral";
  return (
    <section
      className="summary-headline"
      data-direction={directionTone}
      data-update-direction={updateMotion?.direction}
      aria-label="요약 판단"
    >
      <div className="summary-headline-main">
        <div className="summary-headline-topline">
          <span className="summary-headline-kicker">
            {comparisonComplete ? (
              <CheckCircle2 className="h-4 w-4" aria-hidden="true" />
            ) : (
              <CircleDot className="h-4 w-4" aria-hidden="true" />
            )}
            {comparisonComplete ? "비교 결과" : "현재 자료 상태"} · {quality}
          </span>
          <span className="summary-headline-window">
            {comparisonComplete
              ? `직전 동일 ${windowLabel(analysis.context.window_sec)} ↔ 현재`
              : windowLabel(analysis.context.window_sec)}
          </span>
        </div>
        {!comparisonComplete && <h2>{currentShareSummary}</h2>}
        {!comparisonComplete && observedRangeNote && (
          <p className="summary-headline-explainer">{observedRangeNote}</p>
        )}
        {!comparisonComplete && windowView.showsAmounts && (
          <p className="summary-headline-explainer">
            {observedAmountsLine}
            {windowView.amountsQualifier ? ` · ${windowView.amountsQualifier}` : ""}
          </p>
        )}
        {comparisonComplete ? (
          <>
            <div
              className="summary-headline-effects"
              aria-label="점유율 변화 효과 분해"
            >
              <div className="summary-headline-effect summary-headline-share-card">
                <span>빗썸 점유율</span>
                <strong>
                  {percent(animatedCurrentShare, 2)}
                  <span
                    className={`summary-headline-share-change ${changeTone(change)}`}
                  >
                    ({formatShareChangeWords(animatedChange, 2)})
                  </span>
                </strong>
                <small>
                  직전 동일 구간 {percent(attribution.previous_share_pct, 2)}
                </small>
                <small>
                  빗썸 {money(analysis.summary.bithumb_volume_krw)} · 국내{" "}
                  {money(analysis.summary.domestic_volume_krw)}
                </small>
              </div>
              <div className="summary-headline-effect summary-headline-effect-decomposition">
                <div className="summary-headline-effect-operands">
                  <span>빗썸 거래금액 변화 기여</span>
                  <strong
                    className={changeTone(attribution.bithumb_direct_effect_pp)}
                  >
                    {points(animatedBithumbEffect, 2)}
                    <span className="summary-headline-effect-volume-inline">
                      · {moneyChange(attribution.bithumb_volume_change_krw)}
                    </span>
                  </strong>
                  <div className="summary-headline-effect-nested">
                    <span>경쟁 거래소 거래금액 변화 기여</span>
                    <strong className={changeTone(competitorEffect)}>
                      {points(animatedCompetitorEffect, 2)}
                      <span className="summary-headline-effect-volume-inline">
                        ·{" "}
                        {moneyChange(attribution.competitor_volume_change_krw)}
                      </span>
                    </strong>
                  </div>
                </div>
                <div className="summary-headline-effect-total-inline">
                  <span>점유율 변화 기여 합계</span>
                  <strong className={changeTone(change)}>
                    {points(animatedChange, 2)}
                  </strong>
                </div>
              </div>
            </div>
          </>
        ) : (
          !hideIncomplete && (
            <p className="summary-headline-explainer">
              {currentObservationMessage}
            </p>
          )
        )}
      </div>
      {(comparisonComplete || !hideIncomplete) && (
        <aside className="summary-headline-driver">
          <div className="summary-headline-driver-label">
            <Coins className="h-4 w-4" aria-hidden="true" />
            <span>
              {comparisonComplete
                ? "점유율 변화 기여가 큰 코인"
                : "코인별 기여 상태"}
            </span>
          </div>
          {comparisonComplete &&
          contributionDrivers.some(item => item.driver) ? (
            <div className="summary-headline-driver-list">
              {contributionDrivers.map(item => {
                const driver = item.driver;
                if (!driver) return null;
                return (
                  <section
                    className="summary-headline-driver-entry"
                    data-direction={item.direction}
                    key={item.key}
                  >
                    <div className="summary-headline-driver-entry-heading">
                      <span className="summary-headline-driver-direction">
                        {item.label}
                      </span>
                      <div className="summary-headline-driver-impact">
                        <strong>{driver.asset}</strong>
                        <strong className={changeTone(driver.contribution_pp)}>
                          {points(driver.contribution_pp, 2)}
                        </strong>
                      </div>
                    </div>
                    <dl className="summary-headline-volume-grid">
                      <div>
                        <dt>빗썸</dt>
                        <dd
                          className={changeTone(
                            driver.bithumb_volume_change_krw
                          )}
                        >
                          {formatKrwCompact(driver.bithumb_volume_change_krw)}
                        </dd>
                      </div>
                      <div>
                        <dt>경쟁</dt>
                        <dd
                          className={changeTone(
                            driver.competitor_volume_change_krw
                          )}
                        >
                          {formatKrwCompact(
                            driver.competitor_volume_change_krw
                          )}
                        </dd>
                      </div>
                      <div>
                        <dt>국내 합계</dt>
                        <dd
                          className={changeTone(
                            driver.domestic_volume_change_krw
                          )}
                        >
                          {formatKrwCompact(driver.domestic_volume_change_krw)}
                        </dd>
                      </div>
                    </dl>
                  </section>
                );
              })}
            </div>
          ) : (
            <>
              <strong className="text-muted-foreground">{quality}</strong>
              <p className="summary-headline-driver-note">
                <CircleDot className="h-4 w-4" aria-hidden="true" />
                {comparisonMessage}
              </p>
            </>
          )}
        </aside>
      )}
    </section>
  );
}

function SummaryView({
  analysis,
  tickStore,
  connection,
  marketScope,
  requestedWindowSec,
  windowSec,
}: {
  analysis: MarketShareAnalysis;
  tickStore: MarketShareTickStore;
  connection: MarketShareConnection;
  marketScope: MarketScope;
  requestedWindowSec: number;
  windowSec: number;
}) {
  const displayedAnalysis = analysis;
  const hasConfirmedDisplay = hasSummaryComparison(displayedAnalysis);
  const updateMotion = useSummaryUpdateMotion(displayedAnalysis);
  return (
    <div className="summary-view space-y-6">
      <SummaryHeadline
        analysis={displayedAnalysis}
        hideIncomplete={!hasConfirmedDisplay}
        updateMotion={updateMotion}
      />

      <BithumbShareHeatmap
        analysis={displayedAnalysis}
        updateMotion={updateMotion}
      />

      <SummaryExchangeCards analysis={displayedAnalysis} />

      {/* The exchange mix reads only the current per-exchange rows, so it
          stays visible without a comparison. The change decomposition needs
          the previous interval and keeps waiting for one. */}
      <div
        className="summary-analysis-layout"
        data-comparison={hasConfirmedDisplay ? "ready" : "unavailable"}
      >
        <SummaryMarketRail analysis={displayedAnalysis} compact />
        {hasConfirmedDisplay && (
          <div className="summary-analysis-stack">
            <SummaryChangeDecomposition analysis={displayedAnalysis} />
          </div>
        )}
      </div>

      {hasConfirmedDisplay && (
        <div className="grid items-start gap-4 2xl:grid-cols-2">
          <ShareBridge analysis={displayedAnalysis} />
          <SummaryDrivers analysis={displayedAnalysis} />
        </div>
      )}

      <LiveOverviewCharts
        analysis={displayedAnalysis}
        tickStore={tickStore}
        connection={connection}
        marketScope={marketScope}
        requestedWindowSec={requestedWindowSec}
      />

      <DeferredRender
        rootMargin="480px 0px"
        fallback={
          <div className="glass-card min-h-[320px]" aria-hidden="true" />
        }
      >
        <SummaryEvidence analysis={displayedAnalysis} />
      </DeferredRender>
    </div>
  );
}

type DriverView = "impact" | "capture";

function DriverDrawer({
  asset,
  analysis,
  onClose,
}: {
  asset: MarketShareDriversAnalysis["assets"][number];
  analysis: MarketShareDriversAnalysis;
  onClose: () => void;
}) {
  const dialogRef = useRef<HTMLElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  const change = analysis.change_attribution.assets.find(
    row => row.asset === asset.asset
  );
  const notices = analysis.notices.items.filter(
    notice =>
      isNoticeRelevantToSelectedWindow(notice) &&
      exchangeNoticeAssetLabels(notice).includes(asset.asset)
  );
  useEffect(() => {
    const previouslyFocused =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const scrollContainer =
      document.querySelector<HTMLElement>(".dashboard-content");
    const previousBodyOverflow = document.body.style.overflow;
    const previousContentOverflow = scrollContainer?.style.overflowY ?? "";
    document.body.style.overflow = "hidden";
    if (scrollContainer) scrollContainer.style.overflowY = "hidden";
    closeButtonRef.current?.focus();

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        onCloseRef.current();
        return;
      }
      if (event.key !== "Tab" || !dialogRef.current) return;
      const focusable = Array.from(
        dialogRef.current.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
        )
      ).filter(element => !element.hasAttribute("hidden"));
      if (!focusable.length) {
        event.preventDefault();
        return;
      }
      const first = focusable[0];
      const last = focusable.at(-1) as HTMLElement;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      document.body.style.overflow = previousBodyOverflow;
      if (scrollContainer)
        scrollContainer.style.overflowY = previousContentOverflow;
      previouslyFocused?.focus({ preventScroll: true });
    };
  }, []);

  return (
    <div
      className="fixed inset-0 z-40 bg-background/70"
      onMouseDown={event => {
        if (event.target === event.currentTarget) onClose();
      }}
      role="presentation"
    >
      <aside
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="driver-drawer-title"
        className="absolute right-0 top-0 h-full w-full max-w-xl overflow-y-auto border-l border-border bg-card p-5 shadow-2xl"
      >
        <div className="sticky top-0 z-20 flex items-center justify-between border-b border-border bg-card pb-4">
          <div>
            <h3 id="driver-drawer-title" className="text-xl font-semibold">
              {asset.asset}
            </h3>
            <p className="mt-1 text-xs text-muted-foreground">
              {listingLabel(asset)} ·{" "}
              {asset.quotes.join(", ") || "기준통화 없음"}
            </p>
          </div>
          <button
            ref={closeButtonRef}
            type="button"
            onClick={onClose}
            className="border border-border p-2"
            aria-label="닫기"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        {asset.official_minute_replacement_used && (
          <p className="mt-3 text-muted-foreground ui-caption">
            {(asset.official_minute_replacement_exchanges ?? []).map(exchangeLabel).join("·")} 공식 1분 자료로 대체 · 거래금액에 반영 · 체결 상세 미복구
          </p>
        )}
        <div className="mt-4 grid grid-cols-2 gap-px border border-border bg-border text-xs">
          {[
            [
              "점유율 변화 기여",
              change ? points(asset.share_change_contribution_pp) : "—",
            ],
            [
              "코인 거래금액 중 빗썸 비중",
              percent(asset.bithumb_capture_pct, 2),
            ],
            ["국내 거래금액", money(asset.domestic_volume_krw)],
            ["빗썸 거래금액", money(asset.bithumb_volume_krw)],
            [
              "코인 체결 건수 중 빗썸 비중",
              percent(asset.bithumb_trade_count_capture_pct, 2),
            ],
            [
              "코인 체결 수량 중 빗썸 비중",
              percent(asset.bithumb_base_quantity_capture_pct, 2),
            ],
          ].map(([label, value]) => (
            <div key={label} className="bg-card p-3">
              <p className="text-base text-muted-foreground">{label}</p>
              <p className="mt-1 font-semibold">{value}</p>
            </div>
          ))}
        </div>
        {change && (
          <Panel title="직전 동일 구간 비교" className="mt-4">
            <div className="grid grid-cols-2 gap-3 p-4 text-xs">
              <p>
                빗썸 거래금액 증감{" "}
                <strong
                  className={changeTone(change.bithumb_volume_change_krw)}
                >
                  {moneyChange(change.bithumb_volume_change_krw)}
                </strong>
              </p>
              <p>
                해당 코인의 국내 5개 거래소 합계 증감{" "}
                <strong
                  className={changeTone(change.domestic_volume_change_krw)}
                >
                  {moneyChange(change.domestic_volume_change_krw)}
                </strong>
              </p>
              <p>
                코인 거래금액 중 빗썸 비중{" "}
                <strong>
                  {percent(change.previous_bithumb_capture_pct, 2)} →{" "}
                  {percent(change.current_bithumb_capture_pct, 2)}
                </strong>
              </p>
              <p>
                거래소{" "}
                <strong>
                  {asset.domestic_exchanges.map(exchangeLabel).join(", ")}
                </strong>
              </p>
            </div>
          </Panel>
        )}
        {notices.length > 0 && (
          <Panel
            title="공지·이벤트 문맥"
            description="시간적 연관이며 인과관계가 아닙니다."
            className="mt-4"
          >
            <div className="divide-y divide-border/60">
              {notices.slice(0, 10).map(notice => (
                <a
                  key={`${notice.exchange}-${notice.notice_id}`}
                  href={notice.url}
                  target="_blank"
                  rel="noreferrer"
                  className="block p-4 text-xs hover:bg-secondary"
                >
                  <strong>{notice.title}</strong>
                  <p className="mt-1 text-base text-muted-foreground">
                    {exchangeLabel(notice.exchange)} ·{" "}
                    {noticeTimestampLabel(notice)}
                  </p>
                </a>
              ))}
            </div>
          </Panel>
        )}
      </aside>
    </div>
  );
}

function ContributionLeaderCard({
  title,
  description,
  rows,
  value,
  toneValue,
}: {
  title: string;
  description: string;
  rows: MarketShareChangeAsset[];
  value: (row: MarketShareChangeAsset) => string;
  toneValue: (row: MarketShareChangeAsset) => number;
}) {
  return (
    <article className="flex min-h-56 flex-col bg-card p-4">
      <div>
        <div className="mobile-help-heading">
          <h3 className="contribution-leader-title font-semibold">{title}</h3>
          <MobileHelp label={`${title} 설명`}>{description}</MobileHelp>
        </div>
        <p className="mt-1 text-xs leading-5 text-muted-foreground mobile-description-copy">
          {description}
        </p>
      </div>
      {rows.length ? (
        <ol className="mt-4 space-y-2">
          {rows.map((row, index) => (
            <li
              key={row.asset}
              className="flex items-center justify-between gap-3 border-t border-border/60 pt-2 text-xs"
            >
              <span className="flex min-w-0 items-center gap-2 font-semibold">
                <span className="w-4 text-right tabular-nums text-muted-foreground">
                  {index + 1}
                </span>
                <span className="truncate">{row.asset}</span>
              </span>
              <strong className={changeTone(toneValue(row))}>
                {value(row)}
              </strong>
            </li>
          ))}
        </ol>
      ) : (
        <p className="mt-auto pt-6 text-xs text-muted-foreground">
          비교 가능한 코인이 아직 없습니다.
        </p>
      )}
    </article>
  );
}

function CoinContributionHeadline({
  rows,
}: {
  rows: MarketShareChangeAsset[];
}) {
  const summary = useMemo(() => buildCoinContributionSummary(rows, 5), [rows]);

  return (
    <section
      className="overflow-hidden border border-border bg-border"
      aria-labelledby="coin-contribution-headline-title"
    >
      <div className="bg-card px-4 py-3">
        <div className="mobile-help-heading">
          <h2
            id="coin-contribution-headline-title"
            className="text-sm font-semibold"
          >
            지금 점유율을 움직인 코인
          </h2>
          <MobileHelp label="코인 기여 계산 설명">
            선택한 시간과 바로 전 같은 길이의 시간을 비교해 계산한 영향입니다.
            원인으로 단정하지 않습니다.
          </MobileHelp>
        </div>
        <p className="mt-1 text-xs text-muted-foreground mobile-description-copy">
          선택한 시간과 바로 전 같은 길이의 시간을 비교해 계산한 영향입니다.
          원인으로 단정하지 않습니다.
        </p>
      </div>
      <div className="grid gap-px bg-border md:grid-cols-2 xl:grid-cols-4">
        <ContributionLeaderCard
          title="점유율 상승 기여 TOP 5"
          description="빗썸 점유율을 위로 움직인 코인"
          rows={summary.positive}
          value={row => points(row.contribution_pp)}
          toneValue={row => row.contribution_pp}
        />
        <ContributionLeaderCard
          title="점유율 하락 기여 TOP 5"
          description="빗썸 점유율을 아래로 움직인 코인"
          rows={summary.negative}
          value={row => points(row.contribution_pp)}
          toneValue={row => row.contribution_pp}
        />
        <ContributionLeaderCard
          title="빗썸 거래금액 급증 TOP 5"
          description="직전 동일 구간보다 빗썸 거래금액이 늘어난 코인"
          rows={summary.bithumbVolumeGrowth}
          value={row => moneyChange(row.bithumb_volume_change_krw)}
          toneValue={row => row.bithumb_volume_change_krw}
        />
        <ContributionLeaderCard
          title="경쟁 거래소 거래금액 급증 TOP 5"
          description="빗썸 외 국내 거래소 거래금액이 늘어난 코인"
          rows={summary.competitorVolumeGrowth}
          value={row => moneyChange(row.competitor_volume_change_krw)}
          toneValue={row => row.competitor_volume_change_krw}
        />
      </div>
    </section>
  );
}

interface DriverColumn {
  key: CoinTableSortKey;
  label: string;
}

const IMPACT_DRIVER_COLUMNS: DriverColumn[] = [
  { key: "asset", label: "코인" },
  { key: "contribution", label: "점유율 변화 기여" },
  { key: "capture", label: "코인 거래금액 중 빗썸 비중" },
  { key: "bithumb-change", label: "빗썸 거래금액 증감" },
  { key: "domestic-change", label: "국내 거래금액 증감" },
  { key: "competitor", label: "경쟁 거래금액 1위" },
  { key: "listing", label: "국내 상장 구조" },
  { key: "notice", label: "관련 공지" },
];

const CAPTURE_DRIVER_COLUMNS: DriverColumn[] = [
  { key: "asset", label: "코인" },
  { key: "domestic-volume", label: "국내 거래금액" },
  { key: "capture", label: "코인 거래금액 중 빗썸 비중" },
  { key: "capture-gap", label: "전체 빗썸 점유율과 차이" },
  { key: "competitor", label: "경쟁 거래금액 1위" },
  { key: "bithumb-listed", label: "빗썸 상장 여부" },
  { key: "contribution", label: "점유율 변화 기여" },
];

function DriverSortHeader({
  column,
  sort,
  onSort,
  first,
}: {
  column: DriverColumn;
  sort: CoinTableSort;
  onSort: (key: CoinTableSortKey) => void;
  first: boolean;
}) {
  const active = sort.key === column.key;
  const SortIcon = active
    ? sort.direction === "desc"
      ? ArrowDown
      : ArrowUp
    : ArrowUpDown;
  return (
    <th
      aria-sort={
        active
          ? sort.direction === "desc"
            ? "descending"
            : "ascending"
          : "none"
      }
      className={first ? undefined : "text-right"}
    >
      <button
        type="button"
        onClick={() => onSort(column.key)}
        className={`inline-flex min-h-7 w-full items-center gap-1 text-inherit hover:text-primary ${first ? "justify-start" : "justify-end"}`}
        aria-label={`${column.label} ${active ? (sort.direction === "desc" ? "내림차순, 오름차순으로 변경" : "오름차순, 내림차순으로 변경") : "정렬"}`}
      >
        {first ? <CircleDot className="h-3.5 w-3.5" /> : null}
        <span>{column.label}</span>
        <SortIcon
          aria-hidden="true"
          className={`h-3.5 w-3.5 ${active ? "text-primary" : "text-muted-foreground/60"}`}
        />
      </button>
    </th>
  );
}

interface DriverTablePreferences {
  view: DriverView;
  search: string;
  directionFilter: CoinContributionDirection;
  listingFilter: CoinListingFilter;
  noticeFilter: CoinNoticeFilter;
  tradeFilter: CoinTradeFilter;
  sort: CoinTableSort;
  page: number;
}

// When a coin needs trades to have values in each view (see coinTradedInView).
const UNTRADED_PERIOD: Record<DriverView, string> = {
  impact: "선택 구간과 직전 구간 모두",
  capture: "선택 구간에",
};

function competitorCell(asset: CoinTableAsset): string {
  if (asset.competitor_leader) return exchangeLabel(asset.competitor_leader);
  return (asset.domestic_volume_krw ?? 0) > 0 ? "없음" : "—";
}

const DEFAULT_DRIVER_TABLE_PREFERENCES: DriverTablePreferences = {
  view: "impact",
  search: "",
  directionFilter: "all",
  listingFilter: "all",
  noticeFilter: "all",
  tradeFilter: "traded",
  sort: {
    key: null,
    direction: "desc",
  },
  page: 1,
};

function DriverTable({
  analysis,
  preferences,
  setPreferences,
}: {
  analysis: MarketShareDriversAnalysis;
  preferences: DriverTablePreferences;
  setPreferences: Dispatch<SetStateAction<DriverTablePreferences>>;
}) {
  const {
    view,
    search,
    directionFilter,
    listingFilter,
    noticeFilter,
    tradeFilter,
    sort,
    page,
  } = preferences;
  const [selectedAsset, setSelectedAsset] = useState<string | null>(null);
  const pageSize = 100;
  const changes = useMemo(
    () =>
      new Map(analysis.change_attribution.assets.map(row => [row.asset, row])),
    [analysis.change_attribution.assets]
  );
  const events = useMemo(() => {
    const values = new Set<string>();
    analysis.notices.items
      .filter(isNoticeRelevantToSelectedWindow)
      .forEach(notice =>
        exchangeNoticeAssetLabels(notice).forEach(asset => values.add(asset))
      );
    return values;
  }, [analysis.notices.items]);
  const { rows, hiddenUntraded } = useMemo(
    () =>
      queryCoinTableRows({
        assets: analysis.assets,
        changes,
        noticeAssets: events,
        filters: {
          search,
          direction: directionFilter,
          listing: listingFilter,
          notice: noticeFilter,
          trade: tradeFilter,
        },
        sort,
        view,
      }),
    [
      analysis.assets,
      changes,
      directionFilter,
      events,
      listingFilter,
      noticeFilter,
      search,
      sort,
      tradeFilter,
      view,
    ]
  );
  const rowCount = rows.length;
  const pages = Math.max(1, Math.ceil(rowCount / pageSize));
  const safePage = clampPage(page, rowCount, pageSize);
  const visible = rows.slice((safePage - 1) * pageSize, safePage * pageSize);
  const selected =
    selectedAsset === null
      ? null
      : (analysis.assets.find(asset => asset.asset === selectedAsset) ?? null);
  const panelDescription =
    view === "impact"
      ? `${withParticle(windowLabel(analysis.context.window_sec), "과/와")} 직전 동일 구간을 비교해 코인별 점유율 변화 기여와 거래금액 증감을 보여줍니다. 기여도는 계산 결과이며 원인을 뜻하지 않습니다. 전체 ${analysis.assets.length.toLocaleString()}개 코인을 계산했습니다. ${UNTRADED_PERIOD[view]} 거래가 없는 코인은 표시할 값이 없어 기본으로 숨깁니다.`
      : `${windowLabel(analysis.context.window_sec)} 코인별 국내 거래금액 중 빗썸 비중을 전체 빗썸 점유율과 비교합니다. 전체 ${analysis.assets.length.toLocaleString()}개 코인을 계산했습니다. ${UNTRADED_PERIOD[view]} 거래가 없는 코인은 표시할 값이 없어 기본으로 숨깁니다.`;
  const updatePreferences = (patch: Partial<DriverTablePreferences>) => {
    setPreferences(current => ({ ...current, ...patch }));
  };
  const updatePage = (next: number | ((current: number) => number)) => {
    setPreferences(current => ({
      ...current,
      page: typeof next === "function" ? next(current.page) : next,
    }));
  };
  useEffect(
    () =>
      setPreferences(current =>
        current.page === 1 ? current : { ...current, page: 1 }
      ),
    [
      directionFilter,
      listingFilter,
      noticeFilter,
      search,
      sort,
      tradeFilter,
      view,
    ]
  );
  useEffect(() => {
    setPreferences(current => {
      const nextPage = clampPage(current.page, rowCount, pageSize);
      return nextPage === current.page
        ? current
        : { ...current, page: nextPage };
    });
  }, [rowCount]);
  useEffect(() => {
    if (selectedAsset !== null && selected === null) setSelectedAsset(null);
  }, [selected, selectedAsset]);

  const columns =
    view === "impact" ? IMPACT_DRIVER_COLUMNS : CAPTURE_DRIVER_COLUMNS;
  const changeSort = (key: CoinTableSortKey) => {
    setPreferences(current => ({
      ...current,
      sort:
        current.sort.key === key
          ? {
              key,
              direction: current.sort.direction === "desc" ? "asc" : "desc",
            }
          : { key, direction: "desc" },
    }));
  };
  const resetFilters = () => {
    setPreferences({ ...DEFAULT_DRIVER_TABLE_PREFERENCES, view });
  };
  const filtersActive =
    search !== "" ||
    directionFilter !== "all" ||
    listingFilter !== "all" ||
    noticeFilter !== "all" ||
    tradeFilter !== "traded" ||
    sort.key !== null;

  const cells = (asset: CoinTableAsset): React.ReactNode[] => {
    const change = changes.get(asset.asset);
    if (view === "impact")
      return [
        asset.asset,
        change ? (
          <span className={changeTone(asset.share_change_contribution_pp)}>
            {points(asset.share_change_contribution_pp)}
          </span>
        ) : (
          "—"
        ),
        percent(asset.bithumb_capture_pct, 2),
        <span className={changeTone(change?.bithumb_volume_change_krw)}>
          {change ? moneyChange(change.bithumb_volume_change_krw) : "—"}
        </span>,
        <span className={changeTone(change?.domestic_volume_change_krw)}>
          {change ? moneyChange(change.domestic_volume_change_krw) : "—"}
        </span>,
        competitorCell(asset),
        listingLabel(asset),
        events.has(asset.asset) ? "공지" : "—",
      ];
    return [
      asset.asset,
      money(asset.domestic_volume_krw),
      percent(asset.bithumb_capture_pct, 2),
      <span className={changeTone(asset.capture_gap_vs_venue_pp)}>
        {points(asset.capture_gap_vs_venue_pp, 2)}
      </span>,
      competitorCell(asset),
      asset.listed_on_bithumb ? "상장" : "미상장",
      change ? (
        <span className={changeTone(asset.share_change_contribution_pp)}>
          {points(asset.share_change_contribution_pp)}
        </span>
      ) : (
        "—"
      ),
    ];
  };

  return (
    <>
      <CoinContributionHeadline rows={analysis.change_attribution.assets} />
      <Panel
        title="코인별 점유율 기여"
        description={panelDescription}
        className="min-h-[720px]"
        icon={<Coins className="h-4 w-4" />}
        actions={
          <label className="market-table-search">
            <Search className="h-4 w-4 text-muted-foreground" />
            <input
              value={search}
              onChange={event =>
                updatePreferences({ search: event.target.value })
              }
              placeholder="코인 검색"
              aria-label="코인 검색"
              className="w-36 bg-transparent outline-none placeholder:text-muted-foreground/70"
            />
          </label>
        }
      >
        <div className="market-table-tabs">
          {(
            [
              ["impact", "점유율 변화 기여"],
              ["capture", "코인별 빗썸 비중"],
            ] as const
          ).map(([key, label]) => (
            <button
              key={key}
              type="button"
              aria-pressed={view === key}
              onClick={() => {
                updatePreferences({
                  view: key,
                  sort: { key: null, direction: "desc" },
                  page: 1,
                });
              }}
              className={`market-table-tab ${view === key ? "market-table-tab-active" : ""}`}
            >
              {label}
            </button>
          ))}
        </div>
        <div
          className="flex flex-wrap items-end gap-2 border-b border-border bg-card px-3 py-3"
          aria-label="코인 표 필터"
        >
          <label className="grid gap-1 text-xs text-muted-foreground">
            <span>기여 방향</span>
            <select
              value={directionFilter}
              onChange={event =>
                updatePreferences({
                  directionFilter: event.target
                    .value as CoinContributionDirection,
                })
              }
              className="min-h-9 rounded-md border border-border bg-background px-3 text-foreground outline-none focus:border-primary"
            >
              <option value="all">전체 방향</option>
              <option value="positive">상승 기여</option>
              <option value="negative">하락 기여</option>
              <option value="flat">변화 없음</option>
            </select>
          </label>
          <label className="grid gap-1 text-xs text-muted-foreground">
            <span>상장 구조</span>
            <select
              value={listingFilter}
              onChange={event =>
                updatePreferences({
                  listingFilter: event.target.value as CoinListingFilter,
                })
              }
              className="min-h-9 rounded-md border border-border bg-background px-3 text-foreground outline-none focus:border-primary"
            >
              <option value="all">전체 구조</option>
              <option value="bithumb-only">빗썸 단독</option>
              <option value="bithumb-upbit">빗썸·업비트 동시상장</option>
              <option value="bithumb-other">빗썸·기타 동시상장</option>
              <option value="bithumb-unlisted">빗썸 미상장</option>
            </select>
          </label>
          <label className="grid gap-1 text-xs text-muted-foreground">
            <span>관련 공지</span>
            <select
              value={noticeFilter}
              onChange={event =>
                updatePreferences({
                  noticeFilter: event.target.value as CoinNoticeFilter,
                })
              }
              className="min-h-9 rounded-md border border-border bg-background px-3 text-foreground outline-none focus:border-primary"
            >
              <option value="all">전체</option>
              <option value="present">공지 있음</option>
              <option value="absent">공지 없음</option>
            </select>
          </label>
          <label className="grid gap-1 text-xs text-muted-foreground">
            <span>거래 여부</span>
            <select
              value={tradeFilter}
              onChange={event =>
                updatePreferences({
                  tradeFilter: event.target.value as CoinTradeFilter,
                })
              }
              className="min-h-9 rounded-md border border-border bg-background px-3 text-foreground outline-none focus:border-primary"
            >
              <option value="traded">거래 있는 코인</option>
              <option value="all">거래 없는 코인 포함</option>
            </select>
          </label>
          <button
            type="button"
            onClick={resetFilters}
            disabled={!filtersActive}
            className="min-h-9 rounded-md border border-border bg-background px-3 text-xs font-semibold text-foreground disabled:cursor-not-allowed disabled:opacity-40"
          >
            필터·정렬 초기화
          </button>
          <span className="ml-auto pb-2 text-xs text-muted-foreground">
            필터 결과 {rowCount.toLocaleString("ko-KR")}개
          </span>
        </div>
        <div className="market-table-scroll h-[540px]">
          <table
            className="market-data-table market-data-table-sticky-first w-full text-left text-xs"
            data-table-schema="label-numeric"
          >
            <thead>
              <tr>
                {columns.map((column, index) => (
                  <DriverSortHeader
                    key={column.key}
                    column={column}
                    sort={sort}
                    onSort={changeSort}
                    first={index === 0}
                  />
                ))}
              </tr>
            </thead>
            <tbody>
              {visible.map(asset => (
                <tr
                  key={asset.asset}
                  role="button"
                  tabIndex={0}
                  aria-haspopup="dialog"
                  aria-label={`${asset.asset} 상세 분석 열기`}
                  onClick={() => setSelectedAsset(asset.asset)}
                  onKeyDown={event => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      setSelectedAsset(asset.asset);
                    }
                  }}
                  className="market-data-row-interactive"
                >
                  {cells(asset).map((cell, index) => (
                    <td
                      key={`${asset.asset}-${columns[index].key}`}
                      className={`whitespace-nowrap ${index === 0 ? "font-semibold" : "text-right"}`}
                    >
                      {index === 0 ? (
                        <span className="market-table-identity">
                          <span className="market-table-coin-icon">
                            <CircleDot className="h-3 w-3" />
                          </span>
                          {cell}
                        </span>
                      ) : (
                        cell
                      )}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
          {/* Outside the table and pinned left, so a phone shows the message
              in view instead of in the middle of the wide table. */}
          {!visible.length ? (
            <div className="sticky left-0 flex min-h-32 w-full flex-wrap items-center justify-center gap-x-3 gap-y-2 px-4 py-6 text-center text-xs text-muted-foreground">
              {hiddenUntraded ? (
                <>
                  <span>
                    조건에 맞는 코인 {hiddenUntraded.toLocaleString("ko-KR")}
                    개는 {UNTRADED_PERIOD[view]} 거래가 없어 숨겼습니다.
                  </span>
                  <button
                    type="button"
                    onClick={() => updatePreferences({ tradeFilter: "all" })}
                    className="min-h-9 rounded-md border border-border bg-background px-3 font-semibold text-foreground"
                  >
                    거래 없는 코인 포함
                  </button>
                </>
              ) : (
                "선택한 조건에 해당하는 코인이 없습니다."
              )}
            </div>
          ) : null}
        </div>
        <div className="market-table-footer">
          <span>
            <span className="whitespace-nowrap">
              전체 {analysis.assets.length.toLocaleString("ko-KR")}개
            </span>
            {" · "}
            {hiddenUntraded ? (
              <>
                <span className="whitespace-nowrap">
                  거래 없는 {hiddenUntraded.toLocaleString("ko-KR")}개 숨김
                </span>
                {" · "}
              </>
            ) : null}
            <span className="whitespace-nowrap">
              필터 결과 {rowCount.toLocaleString("ko-KR")}개 중{" "}
              {visible.length.toLocaleString("ko-KR")}개 표시
            </span>
          </span>
          {/* The counts wrap instead; a shrinking pager pushed "다음" out of
              the panel at tablet widths. */}
          <div className="flex shrink-0 items-center gap-2">
            <button
              type="button"
              disabled={safePage <= 1}
              onClick={() => updatePage(value => Math.max(1, value - 1))}
              className="market-table-page-button"
            >
              <ChevronLeft className="h-4 w-4" />
              이전
            </button>
            <span className="min-w-20 text-center tabular-nums text-foreground">
              {safePage} / {pages}
            </span>
            <button
              type="button"
              disabled={safePage >= pages}
              onClick={() => updatePage(value => Math.min(pages, value + 1))}
              className="market-table-page-button"
            >
              다음
              <ChevronRight className="h-4 w-4" />
            </button>
          </div>
        </div>
      </Panel>
      {selected && (
        <DriverDrawer
          asset={selected}
          analysis={analysis}
          onClose={() => setSelectedAsset(null)}
        />
      )}
    </>
  );
}

function QualityView({
  analysis,
  queueDropped,
  showMinuteRecovery = false,
}: {
  analysis: MarketShareQualityAnalysis;
  queueDropped: number;
  showMinuteRecovery?: boolean;
}) {
  const quality = analysis.data_quality;
  const components = quality.components;
  const observationGaps = quality.observation_gaps ?? [];
  const accounting = quality.trade_accounting;
  const accountingInput = tradeAccountingInputPresentation(accounting ?? {});
  const accountingStatus = tradeAccountingStatusPresentation(
    accounting?.status,
    accountingInput.balancedLabel
  );
  const accountingToneClass =
    accountingStatus.tone === "profit"
      ? "text-profit"
      : accountingStatus.tone === "gold"
        ? "text-gold"
        : "text-loss";
  const aggregationErrors = components.trade_share.aggregation_loop_errors;
  const aggregationErrorActive = Boolean(
    components.trade_share.aggregation_error_active
  );
  const activeParseErrors =
    components.trade_share.active_trade_parse_error_chunks ?? 0;
  const aggregationErrorLabel = aggregationErrors
    ? `${aggregationErrors.toLocaleString()}건 · ${aggregationErrorActive ? "현재" : "복구됨"}`
    : "없음";
  const supplementConflicts =
    analysis.context.data_source.official_minute_supplement_conflict_count ?? 0;
  const precisionDifferences =
    analysis.context.data_source
      .official_minute_supplement_precision_difference_count ?? 0;
  return (
    <div className="space-y-6">
      <NumericIntegrityStatus
        report={quality.numeric_integrity}
        observationComplete={
          analysis.context.window_sec === 86400
            ? components.history_24h.window_complete
            : analysis.summary.selected_window_complete
        }
      />
      {supplementConflicts > 0 ? (
        <p
          role="alert"
          className="ui-body rounded-lg border border-gold/30 p-3 text-gold"
        >
          공식 분봉과 관측 금액이 충돌한 자료{" "}
          {supplementConflicts.toLocaleString()}건은 보완 합계에서 제외했습니다.
          원본 확인이 필요합니다.
        </p>
      ) : null}
      {precisionDifferences > 0 ? (
        <p
          role="status"
          className="ui-body rounded-lg border border-gold/30 p-3 text-gold"
        >
          선택 구간 공식 분봉의 소수 정밀도 차이{" "}
          {precisionDifferences.toLocaleString()}건은 원본 관측 금액을 유지하고
          보완 금액을 더하지 않았습니다. 단일 체결 근거가 확인된 경우이며, 누락
          관측이 복구됐다는 뜻은 아닙니다.
        </p>
      ) : null}
      <section className="metric-grid sm:grid-cols-2 xl:grid-cols-4">
        <KpiCard
          label="실시간 체결 자료"
          icon={<Radio className="h-4 w-4" />}
          value={qualityStatusLabel(components.trade_share.status)}
          valueClassName="text-xl"
          tone={components.trade_share.status === "HEALTHY" ? "profit" : "loss"}
          sub={`국내 실시간 체결 ${components.trade_share.domestic_ws_realtime_complete ? "모두 수집" : "일부 누락"} · 읽지 못한 체결 ${components.trade_share.unparsed_trade_events.toLocaleString()}건${activeParseErrors ? ` · 지금 읽기 오류 ${activeParseErrors}개 구간` : " · 지금 읽기 오류 없음"} · 처리 오류 ${components.trade_share.trade_processing_errors.toLocaleString()}건 · 계산 오류 ${aggregationErrorLabel}`}
        />
        <KpiCard
          label="10단계 호가 자료"
          icon={<Layers3 className="h-4 w-4" />}
          value={qualityStatusLabel(components.liquidity.status)}
          valueClassName="text-xl"
          tone={components.liquidity.status === "HEALTHY" ? "profit" : "gold"}
          sub={`호가 지연 ${components.liquidity.effective_stale_market_count ?? components.liquidity.stale_market_count}개 · 아직 못 받음 ${components.liquidity.unavailable_market_count}개 · 거래가 적어 멈춘 호가 ${components.liquidity.quiet_market_count ?? 0}개(정상)`}
        />
        <KpiCard
          label="원화 환산"
          icon={<Coins className="h-4 w-4" />}
          value={qualityStatusLabel(components.fx.status)}
          valueClassName="text-xl"
          tone={components.fx.status === "HEALTHY" ? "profit" : "gold"}
          sub={`선택 구간에서 원화로 바꾸지 못한 국내 체결 ${components.fx.domestic_unresolved_trade_count.toLocaleString()}건 · 바이낸스 참고값 ${analysis.official_binance_24h.conversion_status === "RESOLVED" ? "원화 환산 가능" : "원화 환산 대기"}`}
        />
        <KpiCard
          label={
            showMinuteRecovery
              ? "최근 24시간 실시간 체결 관측"
              : "최근 24시간 체결 자료"
          }
          icon={<Gauge className="h-4 w-4" />}
          value={coverageLabel(
            components.history_24h.status,
            components.history_24h.gap_seconds
          )}
          tone={components.history_24h.window_complete ? "profit" : "gold"}
          sub={`실시간 체결 원본 기준 · ${formatObservedWindowCoverage(components.history_24h.coverage_sec, components.history_24h.required_sec, components.history_24h.gap_seconds)} (분봉으로 채우지 않음)`}
        />
      </section>

      <SummaryTimePersistence
        analysis={analysis}
        showMinuteRecovery={showMinuteRecovery}
      />

      <Panel
        title="실시간 자료가 끊긴 기록"
        description="실시간 자료가 끊긴 시간을 거래소·거래쌍과 함께 보여줍니다. 표시된 거래금액은 추정치가 아니라 끊기기 직전 10초 동안 실제로 확인된 금액입니다."
        icon={<History className="h-4 w-4" />}
      >
        {observationGaps.length ? (
          <div className="market-table-scroll">
            <table className="market-data-table w-full text-left text-xs">
              <thead>
                <tr>
                  {[
                    "상태",
                    "시작",
                    "종료",
                    "지속",
                    "영향 거래소",
                    "영향 거래쌍",
                    "확인된 거래금액",
                    "영향 범위",
                    "사유",
                  ].map((label, index) => (
                    <th
                      key={label}
                      className={index && label !== "사유" ? "text-right" : ""}
                    >
                      {label}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {observationGaps.map(gap => (
                  <tr key={gap.gap_id}>
                    <td>
                      <span
                        className={`market-table-status ${gap.status === "RECOVERED" ? "market-status-live" : gap.status === "OPEN" ? "market-status-error" : "market-status-warn"}`}
                      >
                        {observationGapStatusLabel(gap.status)}
                      </span>
                    </td>
                    <td className="text-right">
                      {formatMarketTimeMS(
                        gap.start_second * 1000,
                        "time-seconds"
                      )}
                    </td>
                    <td className="text-right">
                      {gap.end_second === null
                        ? "—"
                        : formatMarketTimeMS(
                            gap.end_second * 1000,
                            "time-seconds"
                          )}
                    </td>
                    <td className="text-right">
                      {formatDuration(gap.duration_sec)}
                    </td>
                    <td className="text-right">
                      {gap.affected_exchanges.map(exchangeLabel).join(" · ") ||
                        "확인 불가"}
                    </td>
                    <td className="text-right">
                      {gap.affected_markets.length.toLocaleString()}개
                    </td>
                    <td className="text-right">
                      {gap.observed_volume_krw === null
                        ? "—"
                        : money(gap.observed_volume_krw)}
                    </td>
                    <td className="text-right">
                      {gap.observed_volume_at_risk_pct === null
                        ? "—"
                        : percent(gap.observed_volume_at_risk_pct, 1)}
                    </td>
                    {/* The table sizes to its content and right-aligned cells
                        stay on one line; a bounded left-aligned block wraps so
                        a phone can show the whole column. */}
                    <td className="text-muted-foreground">
                      <span className="block max-w-64">
                        {observationGapReasonLabels(gap.reason_codes).join(
                          " · "
                        ) || "자료 불완전"}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="p-4 text-xs text-muted-foreground">
            {observationGapRecordsEmptyLabel(quality.observation_gaps)}
          </p>
        )}
      </Panel>

      {/* Counters, collector tables and processing totals are for operators.
          They stay one click away; the summary line keeps their status
          visible while closed. */}
      <details className="quality-operator-details group">
        <summary className="flex min-h-11 cursor-pointer list-none flex-wrap items-center justify-between gap-x-3 gap-y-1 rounded-lg border border-border bg-card px-4 py-3 text-sm font-semibold transition-colors hover:bg-primary/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary">
          <span>수집·처리 상세</span>
          <span className="inline-flex items-center gap-2 text-xs font-normal text-muted-foreground">
            <span className={accounting ? accountingToneClass : ""}>
              {accounting
                ? `건수 검증 ${accountingStatus.label}`
                : "건수 검증 자료 대기"}
            </span>
            {analysis.liquidity.quarantined_assets.length > 0 && (
              <span className="text-gold">
                이상 가격 제외{" "}
                {analysis.liquidity.quarantined_assets.length.toLocaleString()}
                개
              </span>
            )}
            <ChevronDown
              className="h-4 w-4 transition-transform group-open:rotate-180"
              aria-hidden="true"
            />
          </span>
        </summary>
        <div className="mt-6 space-y-6">
          <Panel
            title={accountingInput.title}
            description={accountingInput.description}
            icon={<CheckCircle2 className="h-4 w-4" />}
          >
            {accounting ? (
              <div className="grid gap-3 p-4 text-xs sm:grid-cols-3">
                <div>
                  <span className="text-muted-foreground">검증 상태</span>
                  <strong
                    className={`mt-1 block ${accountingToneClass}`}
                  >
                    {accountingStatus.label}
                  </strong>
                </div>
                <div>
                  <span className="text-muted-foreground">
                    {accountingInput.countersLabel}
                  </span>
                  <strong className="mt-1 block">
                    {accounting.collector_trade_events.toLocaleString()}
                    {accountingInput.separator}
                    {accounting.trades_enqueued.toLocaleString()}
                  </strong>
                  <span className="text-muted-foreground">
                    {accountingInput.differenceLabel}
                    {/* A difference that cannot be compared has no count. */}
                    {formatQualitySignedCount(accounting.collector_lag) === "—"
                      ? ""
                      : ` ${formatQualitySignedCount(accounting.collector_lag)}건`}{" "}
                    · {currentQueueLabel(accounting.queue_in_flight)}
                    <InlineHelp text={accountingInput.help} />
                  </span>
                </div>
                <div>
                  <span className="text-muted-foreground">
                    처리·대기열 건수 검증
                    <InlineHelp text="처리 결과 차이 = 처리 수 − (집계 사용 + 중복 제외 + 유효성 제외). 대기열 차이 = 등록 수 − 처리 수 − 현재 대기 수입니다. 두 값이 0이면 각각의 처리 단계에서 건수가 맞습니다." />
                  </span>
                  <strong
                    className={`mt-1 block ${accounting.processed_conservation_error === 0 && accounting.enqueued_conservation_error === 0 ? "text-profit" : "text-loss"}`}
                  >
                    처리 결과 차이{" "}
                    {formatQualitySignedCount(
                      accounting.processed_conservation_error
                    )}
                    건 · 대기열 차이{" "}
                    {formatQualitySignedCount(
                      accounting.enqueued_conservation_error
                    )}
                    건
                  </strong>
                </div>
              </div>
            ) : (
              <p className="p-4 text-xs text-muted-foreground">
                체결 보존 검증 자료를 아직 받지 못했습니다.
              </p>
            )}
          </Panel>

          <Panel
            title="실시간 수집 범위"
            description={`상태는 현재 선택한 ${analysis.context.market_scope_label} 기준입니다. 등록 수·수신 횟수·경과 시간은 모든 등록 거래쌍 기준입니다. 같은 코인은 기준통화가 달라도 한 번만 셉니다.`}
            icon={<RadioTower className="h-4 w-4" />}
          >
            <div className="market-table-scroll">
              <table
                className="market-data-table market-data-table-sticky-first w-full text-left text-xs"
                data-table-schema="label-status-numeric"
              >
                <thead>
                  <tr>
                    {[
                      "거래소",
                      "상태",
                      "전체 등록 코인",
                      "전체 등록 거래쌍",
                      "체결 메시지",
                      "처리 전달",
                      "읽기 실패",
                      "체결 확인 거래쌍",
                      "호가 확인 거래쌍",
                      "연결 수",
                      "재연결",
                      "읽기 오류",
                      "연결 요청 오류",
                      "체결 수신 후 최장 경과",
                      "호가 수신 후 최장 경과",
                    ].map((label, index) => (
                      <th key={label} className={index > 1 ? "text-right" : ""}>
                        {label}
                        {label === "체결 수신 후 최장 경과" && (
                          <InlineHelp text="여러 수집 연결 중 마지막 체결을 받은 뒤 가장 오래 지난 시간입니다. 거래소 전체의 최근 체결 시각이나 지연된 시간은 아닙니다." />
                        )}
                        {label === "호가 수신 후 최장 경과" && (
                          <InlineHelp text="여러 수집 연결 중 마지막 호가를 받은 뒤 가장 오래 지난 시간입니다. 거래소 전체의 최근 호가 시각이나 지연된 시간은 아닙니다." />
                        )}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {Object.entries(analysis.collectors).map(
                    ([exchange, row]) => {
                      const scopedTradeStreamReady =
                        analysis.context.market_scope === "KRW"
                          ? (row.krw_trade_stream_ready ??
                            row.trade_stream_ready)
                          : row.trade_stream_ready;
                      const scopedTradeActivityFresh =
                        analysis.context.market_scope === "KRW"
                          ? (row.krw_trade_activity_fresh ??
                            row.trade_activity_fresh)
                          : row.trade_activity_fresh;
                      const collectorStatus = !row.enabled
                        ? "OFF"
                        : row.active_trade_parse_error_chunks > 0
                          ? "체결 해석 오류"
                          : scopedTradeStreamReady
                            ? scopedTradeActivityFresh
                              ? "LIVE"
                              : "QUIET"
                            : "체결 수신 지연";
                      const collectorLabel =
                        collectorStatus === "LIVE"
                          ? `${analysis.context.market_scope_label} 실시간`
                          : collectorStatus === "QUIET"
                            ? `${analysis.context.market_scope_label} 연결 정상·거래 적음`
                            : collectorStatus === "OFF"
                              ? "비활성"
                              : collectorStatus;
                      const collectorTone =
                        collectorStatus === "LIVE"
                          ? "market-status-live"
                          : collectorStatus === "OFF"
                            ? "market-status-off"
                            : collectorStatus.includes("오류")
                              ? "market-status-error"
                              : "market-status-warn";
                      return (
                        <tr key={exchange}>
                          <td className="font-semibold">
                            <ExchangeIdentity exchange={exchange} />
                          </td>
                          <td>
                            <span
                              className={`market-table-status ${collectorTone}`}
                            >
                              <Radio className="h-3.5 w-3.5" />
                              {collectorLabel}
                            </span>
                            {row.orderbook_enabled === false && (
                              <span className="mt-1 block text-base text-muted-foreground">
                                10단계 호가 사용 안 함
                              </span>
                            )}
                          </td>
                          <td className="text-right">
                            {row.asset_count?.toLocaleString() ?? "—"}
                          </td>
                          <td className="text-right">
                            {row.market_count.toLocaleString()}
                          </td>
                          <td className="text-right">
                            {row.trade_messages.toLocaleString()}
                          </td>
                          <td className="text-right">
                            {row.trades.toLocaleString()}
                          </td>
                          <td
                            className={`text-right ${row.unparsed_trade_events ? "text-loss" : "text-muted-foreground"}`}
                          >
                            {row.unparsed_trade_events.toLocaleString()}
                          </td>
                          <td className="text-right">
                            {row.observed_trade_market_count.toLocaleString()}
                          </td>
                          <td className="text-right">
                            {row.observed_orderbook_market_count.toLocaleString()}
                          </td>
                          <td className="text-right">
                            {row.connected_chunks}/{row.chunk_count}
                          </td>
                          <td className="text-right">
                            {row.reconnects.toLocaleString()}
                          </td>
                          <td className="text-right">
                            {row.parse_errors.toLocaleString()}
                          </td>
                          <td className="text-right">
                            {row.subscription_errors.toLocaleString()}
                          </td>
                          <td className="text-right">
                            {formatCollectorElapsed(row.max_trade_age_sec)}
                          </td>
                          <td className="text-right">
                            {formatCollectorElapsed(row.max_orderbook_age_sec)}
                          </td>
                        </tr>
                      );
                    }
                  )}
                </tbody>
              </table>
            </div>
          </Panel>

          <Panel
            title="이상 가격 자동 제외"
            description="같은 코인인지 확인되지 않았고 거래소 간 가격 차이가 너무 크면 점유율 진단과 가격 신호에서 자동으로 뺍니다."
            icon={<AlertTriangle className="h-4 w-4" />}
          >
            {analysis.liquidity.quarantined_assets.length ? (
              <div className="max-h-80 overflow-auto divide-y divide-border/60">
                {analysis.liquidity.quarantined_assets.map(row => (
                  <div
                    key={row.asset}
                    className="grid grid-cols-[72px_1fr_auto_auto] items-center gap-3 px-4 py-3 text-xs"
                  >
                    <strong>{row.asset}</strong>
                    <span className="text-muted-foreground">
                      {exchangeLabel(row.buy_exchange ?? "")} →{" "}
                      {exchangeLabel(row.sell_exchange ?? "")} · 같은 코인인지
                      확인 전
                    </span>
                    <span className="text-loss">
                      {bps(row.observed_gap_bps)}
                    </span>
                    <span className="text-muted-foreground">
                      호가 {age(row.max_orderbook_age_sec)}
                    </span>
                  </div>
                ))}
              </div>
            ) : (
              <p className="p-4 text-xs text-muted-foreground">
                현재 자동 제외 기준{" "}
                {bps(
                  analysis.liquidity.price_anomaly_policy
                    .quarantine_threshold_bps
                )}
                를 넘은 자산이 없습니다.
              </p>
            )}
          </Panel>

          <Panel
            title="10단계 호가 상태와 제외 이유"
            description="거래 적음은 연결이 정상이지만 최근 거래가 없어 마지막 값을 유지하는 상태입니다. 거래 중 호가가 새로 들어오지 않으면 호가 지연으로 표시합니다."
            className="min-h-[460px]"
            icon={<Layers3 className="h-4 w-4" />}
          >
            <div className="h-[380px] overflow-auto divide-y divide-border/60">
              {quality.orderbook_health_by_exchange.map(venue => (
                <details
                  key={venue.exchange}
                  className="px-4 py-3"
                  open={venue.issues.length > 0 && venue.exchange === "bithumb"}
                >
                  <summary className="flex min-h-7 cursor-pointer items-center justify-between text-xs">
                    <span className="font-semibold">
                      {exchangeLabel(venue.exchange)}
                    </span>
                    <span className="text-muted-foreground">
                      정상 {venue.fresh_market_count}/
                      {venue.registered_market_count} · 아직 준비 안 됨{" "}
                      {venue.unavailable_market_count} ·{" "}
                      {orderbookAgeBreakdown(
                        venue.stale_market_count,
                        venue.diagnoses
                      )}
                    </span>
                  </summary>
                  <div className="mt-3 border border-border">
                    {venue.issues.map(issue => (
                      <div
                        key={`${issue.exchange}-${issue.market}`}
                        className="grid grid-cols-[1fr_110px_120px_80px] gap-3 border-b border-border/50 px-3 py-2 text-base last:border-b-0"
                      >
                        <span>{issue.market}</span>
                        <span>
                          {orderbookActivityLabel(issue.activity_state)}
                        </span>
                        <span className="ui-caption text-muted-foreground">
                          {orderbookDiagnosisLabel(issue.diagnosis)}
                        </span>
                        <span className="ui-caption text-right text-muted-foreground">
                          {issue.age_sec === null
                            ? "미수신"
                            : `${issue.age_sec.toFixed(1)}초`}
                        </span>
                      </div>
                    ))}
                  </div>
                </details>
              ))}
            </div>
          </Panel>

          <Panel
            title="체결 자료 처리 현황"
            description="공식 실시간 체결만 계산합니다. 중간 저장값과 중복 자료는 거래금액·체결 건수에서 제외합니다."
            icon={<Radio className="h-4 w-4" />}
          >
            <div className="grid grid-cols-2 gap-px bg-border text-xs 2xl:grid-cols-7">
              {[
                [
                  "정상적으로 읽은 체결",
                  `${components.trade_share.collector_trade_events.toLocaleString()}건`,
                ],
                [
                  "처리 대기 등록",
                  `${components.trade_share.trades_enqueued.toLocaleString()}건`,
                ],
                [
                  "처리 완료",
                  `${components.trade_share.trades_processed.toLocaleString()}건`,
                ],
                [
                  "계산에 사용",
                  `${components.trade_share.aggregator_accepted_trades.toLocaleString()}건`,
                ],
                [
                  "중복 제외",
                  `${components.trade_share.duplicate_trade_events.toLocaleString()}건`,
                ],
                [
                  "유효성 제외",
                  `${components.trade_share.invalid_trade_events.toLocaleString()}건`,
                ],
                [
                  "읽지 못한 체결",
                  `${components.trade_share.unparsed_trade_events.toLocaleString()}건`,
                ],
                [
                  "계산 처리 상태",
                  components.trade_share.aggregator_consumer_alive
                    ? "정상"
                    : "중단",
                ],
                [
                  "처리 오류",
                  `${components.trade_share.trade_processing_errors.toLocaleString()}건`,
                ],
                ["반복 계산 오류", aggregationErrorLabel],
                ["처리 대기 중 누락", `${queueDropped.toLocaleString()}건`],
                [
                  "실시간 체결 대체 수집",
                  liveCollectionFallbackLabel(
                    quality.rest_market_data_fallback_enabled
                  ),
                ],
                [
                  "계산 방식",
                  aggregationEngineLabel(quality.aggregation_engine),
                ],
                [
                  "코인·마켓 제한",
                  qualityFilterLabel(
                    quality.coin_filter_applied,
                    quality.market_filter_applied
                  ),
                ],
              ].map(([label, value]) => (
                <div key={label} className="bg-card p-4">
                  <p className="text-base text-muted-foreground">{label}</p>
                  <p className="mt-1 font-semibold">{value}</p>
                </div>
              ))}
            </div>
          </Panel>
        </div>
      </details>
    </div>
  );
}

export default function MarketShare({
  view,
  surface = "detail",
  showMinuteRecovery = false,
}: {
  view: MarketShareView;
  surface?: MarketShareSurface;
  showMinuteRecovery?: boolean;
}) {
  const [windowSec, setWindowSec] = useState<number>(
    MARKET_SHARE_WINDOW_SEC.TEN_SECONDS
  );
  const [marketScope, setMarketScope] = useState<MarketScope>("KRW");
  const [driverTablePreferences, setDriverTablePreferences] =
    useState<DriverTablePreferences>(DEFAULT_DRIVER_TABLE_PREFERENCES);
  const requestedWindowSec = windowSec;
  const {
    data,
    tickStore,
    connection,
    readiness,
    registry,
    error,
    errorSeverity,
  } = useMarketShare(requestedWindowSec, null, marketScope, view, "none");
  const analysis = data?.analysis;
  const analysisLive = connection === "live";

  if (!analysis) {
    return (
      <div className="space-y-6">
        <PageHeader
          title={VIEW_TITLES[view]}
          subtitle="국내 5개 거래소의 실시간 체결 자료를 연결하고 있습니다."
          icon={<Radio className="h-4 w-4" />}
        />
        <StartupReadiness
          readiness={readiness}
          registry={registry}
          error={error}
          errorSeverity={errorSeverity}
        />
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <ContextHeader
        windowSec={windowSec}
        marketScope={marketScope}
        onWindowChange={setWindowSec}
        onScopeChange={setMarketScope}
        view={view}
      />
      {(
        <TargetExchangePerspectivePanel
          analysis={analysis}
          targetExchangeOverride={
            surface === "summary" || view === "drivers" ? "bithumb" : undefined
          }
        />
      )}
      {view === "quality" && shouldShowExchangeRegistry(surface) && (
        <section className="glass-card overflow-hidden">
          <div className="border-b border-border px-4 py-3">
            <div className="mobile-help-heading">
              <h2 className="text-sm font-semibold">거래소별 상장 마켓</h2>
              <MobileHelp label="거래소별 상장 마켓 설명">
                기준통화별 활성 마켓 수와 여러 마켓에 중복 상장된 코인을 한 번만
                센 코인 수를 함께 보여줍니다.
              </MobileHelp>
            </div>
            <p className="mt-1 text-xs text-muted-foreground mobile-description-copy">
              기준통화별 활성 마켓 수와 여러 마켓에 중복 상장된 코인을 한 번만
              센 코인 수를 함께 보여줍니다.
            </p>
          </div>
          <ExchangeRegistryReadiness registry={analysis.registry} />
          <MarketRegistryChanges />
        </section>
      )}
      <WindowCoverageNotice analysis={analysis} />
      {error && (
        <div
          className={`flex items-start gap-3 border p-3 text-xs ${errorSeverity === "error" ? "border-loss/40 bg-loss/10 text-loss" : "border-gold/40 bg-gold/10 text-gold"}`}
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          {error}
        </div>
      )}
      {!analysisLive && view !== "quality" && (
        <Panel
          title="실시간 분석 갱신 대기"
          description="연결 지연 또는 끊김 상태의 이전 점유율·거래금액은 표시하지 않습니다."
          icon={<AlertTriangle className="h-4 w-4 text-gold" />}
        >
          <div className="p-4 text-xs text-muted-foreground">
            새 체결 자료가 확인되면 현재 분석값을 다시 표시합니다.
          </div>
        </Panel>
      )}
      {analysisLive && view === "summary" && (
        <SummaryView
          analysis={analysis}
          tickStore={tickStore}
          connection={connection}
          marketScope={marketScope}
          requestedWindowSec={requestedWindowSec}
          windowSec={windowSec}
        />
      )}
      {analysisLive && view === "overview" && (
        <Overview
          analysis={analysis}
          tickStore={tickStore}
          connection={connection}
          marketScope={marketScope}
          requestedWindowSec={requestedWindowSec}
          windowSec={windowSec}
        />
      )}
      {analysisLive && view === "drivers" && (
        <DriverTable
          analysis={analysis}
          preferences={driverTablePreferences}
          setPreferences={setDriverTablePreferences}
        />
      )}
      {view === "quality" && (
        <QualityView
          analysis={analysis}
          queueDropped={data.queue_dropped}
          showMinuteRecovery={showMinuteRecovery}
        />
      )}
    </div>
  );
}
