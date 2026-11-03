import {
  lazy,
  memo,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";
import {
  Activity,
  AlertTriangle,
  ArrowDown,
  ArrowDownRight,
  ArrowUp,
  ArrowUpDown,
  ArrowUpRight,
  BarChart3,
  Bell,
  ChevronDown,
  Clock3,
  GitBranch,
  Info,
  Radio,
  Repeat2,
  TrendingUp,
} from "lucide-react";
import { DeferredRender } from "@/components/common/DeferredRender";
import { AnimatedMetricValue } from "@/components/common/AnimatedMetricValue";
import { InlineHelp } from "@/components/common/InlineHelp";
import { KpiCard } from "@/components/common/KpiCard";
import {
  MOBILE_VISIBLE_ROWS,
  MobileMoreToggle,
} from "@/components/common/MobileMoreToggle";
import { Panel } from "@/components/common/Panel";
import { OfficialMovementPanel } from "@/components/OfficialMovementPanel";
import { OfficialSelectedHourMetrics } from "@/components/OfficialSelectedHourMetrics";
import { RealtimeFlowPanel } from "@/components/RealtimeFlowPanel";
import { useMediaQuery } from "@/hooks/useMediaQuery";
import {
  useMarketShare,
  type MarketShareConnection,
  type MarketShareTickStore,
} from "@/hooks/useMarketShare";
import {
  getOfficialAlignedContribution,
  getOfficialAlignedHistory,
} from "@/lib/api-client";
import { exchangeLabel } from "@/lib/exchanges";
import {
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import { noticeSummaryDisplayText } from "@/lib/notice-analysis-status";
import {
  formatObservationSeconds,
  formatRateDifferenceBps,
} from "@/lib/market-numeric-presentation";
import { formatObservedWindowCoverage } from "@/lib/observed-window-coverage";
import { MARKET_SHARE_WINDOW_SEC } from "@/lib/market-share-config";
import {
  domesticExchangeFrameIsComplete,
  officialDomesticSnapshotIsComplete,
  officialTrendSnapshotIsComplete,
  selectStableExchangeFrame,
  selectStableOfficialDomestic,
  selectStableOfficialTrend,
  type StableExchangeObservation,
} from "@/lib/market-share-display-stability";
import { buildOfficialContributionInsight } from "@/lib/official-contribution-insight";
import type { MarketShareRealtimeFlowStore } from "@/lib/market-share-realtime-flow-store";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import {
  resolveOfficialHourSelection,
  restoreOfficialHourSelection,
  selectedOfficialHourState,
} from "@/lib/official-hour-selection";
import {
  formatOfficialHourContribution,
  formatOfficialHourShare,
  formatOfficialHourTimestamp,
} from "@/lib/official-hour-metric-format";
import {
  evidenceStrengthLabel,
  noticeCategoryLabel,
  noticeHistoryStatusLabel,
  rollingBriefingTextLabel,
} from "@/lib/market-share-labels";
import {
  INCOMPLETE_OFFICIAL_CONTRIBUTION_RETRY_MS,
  officialAlignedContributionIsComplete,
} from "@/lib/market-share-official-history";
import {
  buildOfficialHourlyChartSeries,
  mergeOfficialHourlyPoints,
  OFFICIAL_HOURLY_CHART_POINT_COUNT,
  officialHourlyHistoryFromMS,
} from "@/lib/official-hourly-series";
import { realtimeFlowTransportIsLive } from "@/lib/market-share-realtime-flow";
import { selectRollingNoticeEvidence } from "@/lib/rolling-notice-retention";
import { selectRelevantRollingNotices } from "@/lib/rolling-notice-visibility";
import { dedupeNoticeRows } from "@/lib/market-share-timeline";
import {
  buildNoticeTimeline,
  NOTICE_EXCHANGES,
  NOTICE_PREVIEW_LIMIT,
  NoticeTimeline,
  noticeExchangeLabel,
} from "@/components/NoticeTimeline";
import type {
  MarketShareAnalysis,
  MarketShareClosedMinuteComparison,
  MarketShareNoticeImageConsistency,
  MarketShareOfficialAlignedContribution,
  MarketShareOfficialDomestic24h,
  MarketShareOfficialMovementDriver,
  MarketShareOfficialMovementState,
  MarketShareOfficialTicker1mPoint,
  MarketShareOfficialTicker1mState,
  MarketShareReactionSummary,
  MarketShareRolling24h,
  MarketShareRollingBridge,
  MarketShareRollingDriver,
  MarketShareRollingObservedFlowDriver,
  MarketShareRollingNotice,
} from "@/lib/market-share-types";

const DOMESTIC_EXCHANGE_IDS = new Set<string>([
  "bithumb",
  "upbit",
  "coinone",
  "korbit",
  "gopax",
]);

const OBSERVED_DRIVER_PREVIEW_LIMIT = 30;
const LazyRollingTrendChart = lazy(
  () => import("@/components/RollingTrendChart")
);
const LazyOfficialTrendCharts = lazy(
  () => import("@/components/OfficialTrendCharts")
);

const OFFICIAL_HOUR_SELECTION_KEY = "market-share:official-hour-selection:v1";
const OFFICIAL_HOUR_CONTRIBUTION_KEY =
  "market-share:official-hour-contribution:v1";
const retainedOfficialContributionByHour = new Map<
  number,
  MarketShareOfficialAlignedContribution
>();
const retainedOfficialContributionFullHours = new Set<number>();

function restoreOfficialContribution(
  selectedEndMS: number
): MarketShareOfficialAlignedContribution | null {
  if (selectedEndMS <= 0 || typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(OFFICIAL_HOUR_CONTRIBUTION_KEY);
    if (!raw) return null;
    const value = JSON.parse(raw) as MarketShareOfficialAlignedContribution;
    return officialAlignedContributionIsComplete(value, selectedEndMS)
      ? value
      : null;
  } catch {
    return null;
  }
}

function rememberOfficialContribution(
  selectedEndMS: number,
  contribution: MarketShareOfficialAlignedContribution
) {
  if (selectedEndMS <= 0 || typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      OFFICIAL_HOUR_CONTRIBUTION_KEY,
      JSON.stringify(contribution)
    );
  } catch {
    // Large or disabled storage must not interrupt the live page.
  }
}

function finite(value: number | null | undefined): value is number {
  return value !== null && value !== undefined && Number.isFinite(value);
}

function rollingWindowIsComplete(
  current: MarketShareRolling24h["rolling_24h"]
): boolean {
  return (
    current.status === "CONFIRMED" &&
    current.coverage.coverage_sec >= current.coverage.required_sec &&
    current.coverage.gap_seconds === 0
  );
}

function percent(value: number | null | undefined, digits = 2): string {
  return formatSharePercent(value, digits);
}

function signedPercent(value: number | null | undefined, digits = 1): string {
  return finite(value)
    ? (value >= 0 ? "+" : "") + value.toFixed(digits) + "%"
    : "—";
}

function points(value: number | null | undefined, digits = 2): string {
  if (!finite(value)) return "—";
  const precision = Math.min(3, Math.max(0, Math.trunc(digits)));
  return formatSignedPercentagePoints(value, precision);
}

function displayWindowLabel(value: string | null | undefined): string {
  if (!value) return "—";
  const minutes = /^(\d+)m$/i.exec(value);
  if (minutes) return `${minutes[1]}분`;
  const hours = /^(\d+)h$/i.exec(value);
  if (hours) return `${hours[1]}시간`;
  return value;
}

function money(value: number | null | undefined): string {
  return moneyHundredMillion(value);
}

const HUNDRED_MILLION_KRW = 100_000_000;

function moneyHundredMillion(value: number | null | undefined): string {
  if (!finite(value)) return "—";
  if (value === 0) return "0억";
  const rounded = Math.round(Math.abs(value) / HUNDRED_MILLION_KRW);
  const sign = value < 0 ? "-" : "";
  return rounded > 0
    ? sign + rounded.toLocaleString("ko-KR") + "억"
    : sign + "1억 미만";
}

function signedMoney(value: number | null | undefined): string {
  if (!finite(value)) return "—";
  if (value === 0) return "변화 없음";
  // "-1억 미만" reads as "down by more than 1억"; say the small change in words.
  if (Math.round(Math.abs(value) / HUNDRED_MILLION_KRW) === 0) {
    return `1억 미만 ${value > 0 ? "증가" : "감소"}`;
  }
  return (value > 0 ? "+" : "-") + money(Math.abs(value));
}

const INTEGER_FORMAT = new Intl.NumberFormat("ko-KR", {
  maximumFractionDigits: 0,
});

function integerLabel(value: number | null | undefined): string {
  return finite(value) ? INTEGER_FORMAT.format(value) + "회" : "—";
}

function flowMoney(
  share: number | null | undefined,
  volume: number | null | undefined
): string {
  if (!finite(share)) return "관측 없음";
  return finite(volume) ? money(volume) : "금액 정보 없음";
}

function basisPoints(value: number | null | undefined): string {
  return finite(value)
    ? (value >= 0 ? "+" : "") + (value / 100).toFixed(3) + "%"
    : "—";
}

function ageSeconds(value: number | null | undefined): string {
  return finite(value) ? Math.round(value) + "초" : "—";
}

function coverageLabel(
  coverageSeconds: number,
  requiredSeconds: number,
  gapSeconds: number
): string {
  return `관측 ${coverageSeconds.toLocaleString("ko-KR")}초/${requiredSeconds.toLocaleString("ko-KR")}초 · 공백 ${gapSeconds.toLocaleString("ko-KR")}초`;
}

function segmentCoverageLabel(
  label: string,
  coverage:
    | {
        status: string;
        coverage_sec: number;
        required_sec: number;
        gap_seconds: number;
      }
    | undefined
): string {
  if (!coverage) return `${label} 관측 없음`;
  return `${label} ${coverage.coverage_sec.toLocaleString("ko-KR")}/${coverage.required_sec.toLocaleString("ko-KR")}초 · ${statusLabel(coverage.status)}`;
}

function qualityTone(status: string): "live" | "warn" | "muted" | "error" {
  if (["CONFIRMED", "READY", "OK", "MEASURED"].includes(status)) return "live";
  if (["UNAVAILABLE", "NOT_POLLED"].includes(status)) return "muted";
  if (["ERROR", "MISMATCH"].includes(status)) return "error";
  return "warn";
}

type SortValue = string | number | null | undefined;
type SortState = { key: string | null; direction: "asc" | "desc" };

function compareSortValues(left: SortValue, right: SortValue): number {
  const leftMissing =
    left === null ||
    left === undefined ||
    (typeof left === "number" && !Number.isFinite(left));
  const rightMissing =
    right === null ||
    right === undefined ||
    (typeof right === "number" && !Number.isFinite(right));
  if (leftMissing || rightMissing) {
    if (leftMissing && rightMissing) return 0;
    return leftMissing ? 1 : -1;
  }
  if (typeof left === "number" && typeof right === "number")
    return left - right;
  return String(left).localeCompare(String(right), "ko");
}

function sortRows<T>(
  rows: T[],
  sort: SortState,
  valueFor: (row: T, key: string) => SortValue
): T[] {
  if (!sort.key) return rows;
  return [...rows].sort((left, right) => {
    const leftValue = valueFor(left, sort.key as string);
    const rightValue = valueFor(right, sort.key as string);
    const leftMissing =
      leftValue === null ||
      leftValue === undefined ||
      (typeof leftValue === "number" && !Number.isFinite(leftValue));
    const rightMissing =
      rightValue === null ||
      rightValue === undefined ||
      (typeof rightValue === "number" && !Number.isFinite(rightValue));
    if (leftMissing || rightMissing) {
      if (leftMissing && rightMissing) return 0;
      return leftMissing ? 1 : -1;
    }
    const result = compareSortValues(leftValue, rightValue);
    return sort.direction === "asc" ? result : -result;
  });
}

function SortHeader({
  label,
  sortKey,
  sort,
  onSort,
  align = "left",
}: {
  label: string;
  sortKey: string;
  sort: SortState;
  onSort: (key: string) => void;
  align?: "left" | "right";
}) {
  const active = sort.key === sortKey;
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
      className={align === "right" ? "text-right" : undefined}
    >
      <button
        type="button"
        onClick={() => onSort(sortKey)}
        className={
          (align === "right" ? "ml-auto justify-end " : "") +
          "inline-flex min-h-7 w-full items-center gap-1 text-inherit hover:text-foreground"
        }
      >
        <span>{label}</span>
        <SortIcon
          aria-hidden="true"
          className={`size-3.5 ${active ? "text-foreground" : "text-muted-foreground/60"}`}
        />
      </button>
    </th>
  );
}

function toggleSort(
  setSort: Dispatch<SetStateAction<SortState>>,
  key: string
): void {
  setSort(current =>
    current.key === key
      ? { key, direction: current.direction === "desc" ? "asc" : "desc" }
      : { key, direction: "desc" }
  );
}

function timestamp(value: number | null | undefined): string {
  const formatted = formatMarketTimeMS(value);
  return formatted === "—" ? "시각 정보 없음" : formatted;
}

/** "연결 시각 10. 20. 08:00:00", or "연결 시각 정보 없음" without the repeated 시각. */
function labeledTimestamp(label: string, value: number | null | undefined): string {
  const formatted = formatMarketTimeMS(value);
  return formatted === "—" ? `${label} 정보 없음` : `${label} ${formatted}`;
}

const NOTICE_ROW_EXCEPTION_STATES = new Set([
  "ANALYSIS_FAILED",
  "IMAGE_BODY_CONFLICT",
  "DETAIL_ERROR",
]);

function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    CONFIRMED: "전체 관측",
    PROVISIONAL: "확인 중",
    RESTORED: "공식 기록 복구 포함",
    ESTIMATED: "공식 캔들 추정 포함",
    PARTIAL: "일부 누락",
    UNAVAILABLE: "계산 불가",
    BASELINE_WARMING: "기준선 축적 중",
    UNUSUAL: "평소보다 큰 변화",
    WITHIN_BASELINE: "평소 범위",
    ANALYZED: "본문 분석 완료",
    ANALYSIS_NOT_REQUIRED: "공식 원문 검색 준비 완료",
    ANALYSIS_DEFERRED_QUOTA: "본문·일정 분석 대기",
    TRANSLATION_PENDING: "번역 대기",
    ANALYSIS_FAILED: "자동 요약 실패 · 원문 보기",
    IMAGE_BODY_CONFLICT: "본문·이미지 내용 불일치",
    TEXT_ANALYZED_IMAGE_PENDING: "본문 분석·이미지 대기",
    DETAIL_ERROR: "본문 분석 오류",
    DETAIL_PENDING: "본문 분석 대기",
    LIST_ONLY: "목록만 확인",
    PENDING: "첫 수집 대기",
    LIMITED: "수집 범위 제한",
    READY: "판정 가능",
    BLOCKED_BY_DATA_GAP: "비교 이력 부족",
    WARMING: "기준선 축적 중",
  };
  return labels[status] ?? (status || "상태 정보 없음");
}

function imageConsistencyLabel(
  value: MarketShareNoticeImageConsistency | undefined
): string {
  if (!value) return "검증 정보 없음";
  return (
    {
      MATCHED: "본문·이미지 일치",
      PARTIAL: "일부 일치",
      CONFLICT: "본문·이미지 불일치",
      UNVERIFIED: "확인 불가",
    }[value.status] ?? value.status
  );
}

function imageSemanticLabel(value: string): string {
  return (
    {
      CHART: "그래프·차트",
      TABLE: "표",
      BANNER: "이벤트 배너",
      TEXT_WITH_DATA: "수치 포함 문구",
      TEXT: "텍스트 이미지",
      UNKNOWN: "분류 불가",
    }[value] ?? value
  );
}

function imageSignalLabel(value: string): string {
  return (
    {
      date: "날짜·기간",
      time: "시각",
      asset: "자산",
      value: "금액·비율·수량",
      text: "본문 문구",
      percentage: "비율",
      amount: "금액",
      trend: "추이",
      rows: "행·열",
      event: "이벤트 문맥",
    }[value] ?? value
  );
}

function movementIsBlocked(monitor: MarketShareRolling24h): boolean {
  return monitor.data_quality.movement_status === "BLOCKED_BY_DATA_GAP";
}

function movementGapMessage(monitor: MarketShareRolling24h): string {
  return finite(monitor.rolling_24h.current_share_pct)
    ? "현재 24시간 점유율은 관측됐지만, 10·20·30분 단위 비교값이 두 구간 이상 확보되기 전에는 변화 구간의 시작·지속·극값을 판정하지 않습니다."
    : "10·20·30분 단위의 24시간 비교값이 두 구간 이상 확보되기 전에는 변화 구간의 시작·지속·극값을 판정하지 않습니다.";
}

function changeTone(value: number | null | undefined): string {
  if (!finite(value) || Math.abs(value) < 1e-9) return "text-muted-foreground";
  return value > 0 ? "text-profit" : "text-loss";
}

function StatusBadge({
  label,
  tone = "muted",
}: {
  label: string;
  tone?: "live" | "warn" | "muted" | "error";
}) {
  const toneClass = {
    live: "border-profit/40 bg-profit/10 text-profit",
    warn: "border-gold/40 bg-gold/10 text-gold",
    error: "border-loss/40 bg-loss/10 text-loss",
    muted: "border-border bg-secondary text-muted-foreground",
  }[tone];
  return (
    <span
      className={
        "status-badge inline-flex shrink-0 items-center gap-1 whitespace-nowrap border px-2 py-1 text-base " +
        toneClass
      }
    >
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {label}
    </span>
  );
}

function ObservedFlowTable({
  drivers,
  kind,
}: {
  drivers: MarketShareRollingObservedFlowDriver[];
  kind: "asset" | "exchange";
}) {
  const [sort, setSort] = useState<SortState>({ key: null, direction: "desc" });
  const labelFor = (driver: MarketShareRollingObservedFlowDriver): string =>
    kind === "asset"
      ? (driver.base ?? "자산 정보 없음")
      : exchangeLabel(driver.exchange ?? "");
  const volumeChangeFor = (
    driver: MarketShareRollingObservedFlowDriver
  ): number | null =>
    finite(driver.recent_flow_share_pct) &&
    finite(driver.expiring_flow_share_pct) &&
    finite(driver.recent_volume_krw) &&
    finite(driver.expiring_volume_krw)
      ? driver.recent_volume_krw - driver.expiring_volume_krw
      : null;
  const sortedDrivers = useMemo(
    () =>
      sortRows(drivers, sort, (driver, key) => {
        switch (key) {
          case "label":
            return labelFor(driver);
          case "recent_bithumb":
            return driver.recent_bithumb_volume_krw;
          case "recent_competitor":
            return driver.recent_competitor_volume_krw;
          case "expiring_bithumb":
            return driver.expiring_bithumb_volume_krw;
          case "expiring_competitor":
            return driver.expiring_competitor_volume_krw;
          case "recent_share":
            return driver.recent_flow_share_pct;
          case "expiring_share":
            return driver.expiring_flow_share_pct;
          case "volume_change":
            return volumeChangeFor(driver);
          case "volume_change_pct":
            return driver.recent_vs_expiring_volume_change_pct;
          case "replacement_gap":
            return driver.replacement_gap_pp;
          case "reason":
            return driver.reason;
          default:
            return null;
        }
      }),
    [drivers, kind, sort]
  );
  return (
    <div className="border-t border-border bg-inset px-4 py-3">
      {kind === "asset" && (
        <p className="mb-2 text-base leading-6 text-muted-foreground">
          최근 24시간 전체에 미친 영향은 아직 확정하지 않고, 현재 확인된 들어온
          거래와 빠진 거래만 표시합니다. 거래금액 차이는 두 흐름이 모두 확인된
          경우의 금액 차이이며 점유율 변화(%p)가 아닙니다.
        </p>
      )}
      <div className="max-h-[28rem] overflow-auto">
        <table
          className="market-data-table w-full text-left text-xs"
          data-table-schema="label-numeric"
        >
          <thead>
            <tr>
              <SortHeader
                label={kind === "asset" ? "코인" : "거래소"}
                sortKey="label"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
              />
              <SortHeader
                label="새로 들어온 빗썸 거래"
                sortKey="recent_bithumb"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="새로 들어온 경쟁 거래소 거래"
                sortKey="recent_competitor"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="24시간 범위에서 빠진 빗썸 거래"
                sortKey="expiring_bithumb"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="24시간 범위에서 빠진 경쟁 거래소 거래"
                sortKey="expiring_competitor"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="새 거래 중 빗썸 몫"
                sortKey="recent_share"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="빠진 거래 중 빗썸 몫"
                sortKey="expiring_share"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="전체 흐름 금액 차이"
                sortKey="volume_change"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="전체 흐름 규모 증감률"
                sortKey="volume_change_pct"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="교체 점유율 차이"
                sortKey="replacement_gap"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="확인된 흐름"
                sortKey="reason"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
              />
            </tr>
          </thead>
          <tbody>
            {sortedDrivers.map(driver => {
              const label = labelFor(driver);
              const volumeChange = volumeChangeFor(driver);
              return (
                <tr key={kind + "-observed-" + label}>
                  <td className="font-semibold">{label}</td>
                  <td className="text-right">
                    {flowMoney(
                      driver.recent_flow_share_pct,
                      driver.recent_bithumb_volume_krw
                    )}
                  </td>
                  <td className="text-right">
                    {flowMoney(
                      driver.recent_flow_share_pct,
                      driver.recent_competitor_volume_krw
                    )}
                  </td>
                  <td className="text-right">
                    {flowMoney(
                      driver.expiring_flow_share_pct,
                      driver.expiring_bithumb_volume_krw
                    )}
                  </td>
                  <td className="text-right">
                    {flowMoney(
                      driver.expiring_flow_share_pct,
                      driver.expiring_competitor_volume_krw
                    )}
                  </td>
                  <td className="text-right">
                    {percent(driver.recent_flow_share_pct)}
                  </td>
                  <td className="text-right">
                    {percent(driver.expiring_flow_share_pct)}
                  </td>
                  <td className={"text-right " + changeTone(volumeChange)}>
                    {signedMoney(volumeChange)}
                  </td>
                  <td
                    className={
                      "text-right " +
                      changeTone(driver.recent_vs_expiring_volume_change_pct)
                    }
                  >
                    {signedPercent(driver.recent_vs_expiring_volume_change_pct)}
                  </td>
                  <td
                    className={
                      "text-right " + changeTone(driver.replacement_gap_pp)
                    }
                  >
                    {points(driver.replacement_gap_pp)}
                  </td>
                  <td className="text-muted-foreground">{driver.reason}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function DriverTable({
  title,
  drivers,
  observedDrivers,
  observedFallback,
  kind,
}: {
  title: string;
  drivers: MarketShareRollingDriver[];
  observedDrivers: MarketShareRollingObservedFlowDriver[];
  observedFallback?: MarketShareRollingObservedFlowDriver | null;
  kind: "asset" | "exchange";
}) {
  const [showAllObserved, setShowAllObserved] = useState(false);
  const [sort, setSort] = useState<SortState>({ key: null, direction: "desc" });
  useEffect(() => setShowAllObserved(false), [observedDrivers]);
  const labelFor = (driver: MarketShareRollingDriver): string =>
    kind === "asset"
      ? (driver.base ?? "자산 정보 없음")
      : exchangeLabel(driver.exchange ?? "");
  const volumeChangesFor = (
    driver: MarketShareRollingDriver
  ): { bithumb: number | null; domestic: number | null } => {
    const bithumb =
      finite(driver.recent_bithumb_volume_krw) &&
      finite(driver.expiring_bithumb_volume_krw)
        ? driver.recent_bithumb_volume_krw - driver.expiring_bithumb_volume_krw
        : null;
    const domestic =
      finite(driver.recent_bithumb_volume_krw) &&
      finite(driver.recent_competitor_volume_krw) &&
      finite(driver.expiring_bithumb_volume_krw) &&
      finite(driver.expiring_competitor_volume_krw)
        ? driver.recent_bithumb_volume_krw +
          driver.recent_competitor_volume_krw -
          (driver.expiring_bithumb_volume_krw +
            driver.expiring_competitor_volume_krw)
        : null;
    return { bithumb, domestic };
  };
  const sortedDrivers = useMemo(
    () =>
      sortRows(drivers, sort, (driver, key) => {
        const volumeChanges = volumeChangesFor(driver);
        switch (key) {
          case "label":
            return labelFor(driver);
          case "reason":
            return driver.reason;
          case "incoming":
            return driver.incoming_effect_pp;
          case "expiring":
            return driver.expiring_effect_pp;
          case "contribution":
            return driver.contribution_pp;
          case "bithumb_change":
            return volumeChanges.bithumb;
          case "domestic_change":
            return volumeChanges.domestic;
          default:
            return null;
        }
      }),
    [drivers, kind, sort]
  );
  const fallbackDrivers =
    !drivers.length && !observedDrivers.length && observedFallback
      ? [observedFallback]
      : observedDrivers;
  const hasExpandableObservedRows =
    !drivers.length && observedDrivers.length > OBSERVED_DRIVER_PREVIEW_LIMIT;
  const visibleFallbackDrivers =
    hasExpandableObservedRows && !showAllObserved
      ? fallbackDrivers.slice(0, OBSERVED_DRIVER_PREVIEW_LIMIT)
      : fallbackDrivers;
  const visibleCount = drivers.length
    ? "상위 " + drivers.length + "개"
    : observedDrivers.length
      ? "관측 상위 " + observedDrivers.length + "개"
      : observedFallback
        ? "관측 합계"
        : "자료 부족";
  return (
    <Panel
      title={title}
      description={
        kind === "asset"
          ? "각 코인이 점유율 변화에 얼마나 기여했는지 보여줍니다. 새로 들어온 거래와 24시간 범위에서 빠진 거래의 실제 원화 금액도 따로 표시합니다."
          : undefined
      }
      icon={
        kind === "asset" ? (
          <BarChart3 className="h-4 w-4" />
        ) : (
          <TrendingUp className="h-4 w-4" />
        )
      }
      actions={
        <span className="text-xs text-muted-foreground">{visibleCount}</span>
      }
    >
      {drivers.length ? (
        <div className="max-h-[28rem] overflow-auto">
          <table
            className="market-data-table w-full text-left text-xs"
            data-table-schema="label-detail-numeric"
          >
            <thead>
              <tr>
                <SortHeader
                  label={kind === "asset" ? "코인" : "거래소"}
                  sortKey="label"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                />
                <SortHeader
                  label="확인된 흐름"
                  sortKey="reason"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                />
                <SortHeader
                  label="최근 새 거래 기여"
                  sortKey="incoming"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="24시간 범위에서 빠진 거래의 영향"
                  sortKey="expiring"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="점유율 기여(%p)"
                  sortKey="contribution"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="빗썸 거래금액 차이"
                  sortKey="bithumb_change"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="국내 거래금액 차이"
                  sortKey="domestic_change"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
              </tr>
            </thead>
            <tbody>
              {sortedDrivers.map(driver => {
                const label = labelFor(driver);
                const volumeChanges = volumeChangesFor(driver);
                const bithumbVolumeChange = volumeChanges.bithumb;
                const domesticVolumeChange = volumeChanges.domestic;
                return (
                  <tr key={kind + "-" + label}>
                    <td className="font-semibold">{label}</td>
                    <td className="text-muted-foreground">{driver.reason}</td>
                    <td
                      className={
                        "text-right " + changeTone(driver.incoming_effect_pp)
                      }
                    >
                      {points(driver.incoming_effect_pp)}
                    </td>
                    <td
                      className={
                        "text-right " + changeTone(driver.expiring_effect_pp)
                      }
                    >
                      {points(driver.expiring_effect_pp)}
                    </td>
                    <td
                      className={
                        "text-right " +
                        changeTone(driver.contribution_pp)
                      }
                    >
                      {points(driver.contribution_pp)}
                    </td>
                    <td
                      className={
                        "text-right " + changeTone(bithumbVolumeChange)
                      }
                    >
                      {signedMoney(bithumbVolumeChange)}
                    </td>
                    <td
                      className={
                        "text-right " + changeTone(domesticVolumeChange)
                      }
                    >
                      {signedMoney(domesticVolumeChange)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : visibleFallbackDrivers.length ? (
        <>
          <ObservedFlowTable drivers={visibleFallbackDrivers} kind={kind} />
          {hasExpandableObservedRows && (
            <div className="border-t border-border px-4 py-3 text-center">
              <button
                type="button"
                onClick={() => setShowAllObserved(current => !current)}
                className="border border-border px-3 py-1.5 text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary"
              >
                {showAllObserved
                  ? `관측 상위 ${OBSERVED_DRIVER_PREVIEW_LIMIT}개만 표시`
                  : `전체 관측 ${observedDrivers.length}개 보기`}
              </button>
            </div>
          )}
        </>
      ) : (
        <div className="p-5 text-xs text-muted-foreground">
          비교 가능한 거래 구간이 없어 기여 코인·거래소를 계산할 수 없습니다.
        </div>
      )}
    </Panel>
  );
}

function MicrostructureTable({ bridge }: { bridge: MarketShareRollingBridge }) {
  const rows = bridge.microstructure ?? [];
  const [sort, setSort] = useState<SortState>({ key: null, direction: "desc" });
  const sortedRows = useMemo(
    () =>
      sortRows(rows, sort, (row, key) => {
        switch (key) {
          case "asset":
            return row.asset;
          case "contribution":
            return row.contribution_pp;
          case "bithumb_depth":
            return row.bithumb_depth_krw;
          case "competitor_depth":
            return row.competitor_depth_krw;
          case "spread_gap":
            return row.spread_gap_bps;
          case "slippage_gap":
            return row.buy_slippage_gap_bps;
          case "price_gap":
            return row.bithumb_buy_price_gap_bps;
          case "bithumb_age":
            return row.bithumb_orderbook_max_age_sec;
          case "competitor_age":
            return row.competitor_orderbook_max_age_sec;
          case "diagnosis":
            return row.diagnosis;
          default:
            return null;
        }
      }),
    [rows, sort]
  );
  return (
    <Panel
      title={displayWindowLabel(bridge.window) + " 국내 주문 가격 확인"}
      description="점유율 변화의 원인을 정하는 값이 아닙니다. 영향이 큰 코인의 현재 10단계 주문, 매수·매도 가격 차이, 예상 매수 가격 손해를 함께 보여주는 참고 자료입니다."
      icon={<Activity className="h-4 w-4" />}
      actions={
        <span className="text-xs text-muted-foreground">
          {rows.length ? "기여도 상위 " + rows.length + "개 코인" : "자료 부족"}
        </span>
      }
    >
      {rows.length ? (
        <div className="overflow-x-auto">
          <table
            className="market-data-table w-full text-left text-xs"
            data-table-schema="label-detail-numeric"
          >
            <thead>
              <tr>
                <SortHeader
                  label="코인"
                  sortKey="asset"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                />
                <SortHeader
                  label="호가 상태·해석"
                  sortKey="diagnosis"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                />
                <SortHeader
                  label="점유율 기여(%p)"
                  sortKey="contribution"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="빗썸 10단계 호가"
                  sortKey="bithumb_depth"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="경쟁 거래소 10단계 호가"
                  sortKey="competitor_depth"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="매수·매도 가격 차이율 격차 (%p)"
                  sortKey="spread_gap"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="예상 매수 가격 손해율 격차 (%p)"
                  sortKey="slippage_gap"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="가장 좋은 매수·매도 가격 차이 (%)"
                  sortKey="price_gap"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="빗썸 호가 나이"
                  sortKey="bithumb_age"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
                <SortHeader
                  label="경쟁 호가 나이"
                  sortKey="competitor_age"
                  sort={sort}
                  onSort={key => toggleSort(setSort, key)}
                  align="right"
                />
              </tr>
            </thead>
            <tbody>
              {sortedRows.map(row => (
                <tr key={row.asset}>
                  <td className="font-semibold">{row.asset}</td>
                  <td className="max-w-[28rem] leading-6 text-muted-foreground">
                    {row.diagnosis}
                  </td>
                  <td
                    className={
                      "text-right " +
                      changeTone(row.contribution_pp)
                    }
                  >
                    {points(row.contribution_pp)}
                  </td>
                  <td className="text-right">{money(row.bithumb_depth_krw)}</td>
                  <td className="text-right">
                    {money(row.competitor_depth_krw)}
                  </td>
                  <td
                    className={"text-right " + changeTone(row.spread_gap_bps)}
                  >
                    {formatRateDifferenceBps(row.spread_gap_bps)}
                  </td>
                  <td
                    className={
                      "text-right " + changeTone(row.buy_slippage_gap_bps)
                    }
                  >
                    {formatRateDifferenceBps(row.buy_slippage_gap_bps)}
                  </td>
                  <td
                    className={
                      "text-right " + changeTone(row.bithumb_buy_price_gap_bps)
                    }
                  >
                    {basisPoints(row.bithumb_buy_price_gap_bps)}
                  </td>
                  <td className="text-right">
                    {ageSeconds(row.bithumb_orderbook_max_age_sec)}
                  </td>
                  <td className="text-right">
                    {ageSeconds(row.competitor_orderbook_max_age_sec)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="p-5 text-xs text-muted-foreground">
          선택한 비교 시간의 국내 10단계 주문 자료가 없습니다.
        </div>
      )}
      <div className="border-t border-border px-4 py-3 text-base leading-6 text-muted-foreground">
        가격 차이와 예상 가격 손해 차이가 양수면 빗썸에서 거래하기가 경쟁
        거래소보다 불리했다는 뜻입니다. 이는 점유율 변화의 원인을 입증하지
        않습니다.
      </div>
    </Panel>
  );
}

function HorizonTable({ monitor }: { monitor: MarketShareRolling24h }) {
  const [sort, setSort] = useState<SortState>({ key: null, direction: "desc" });
  const sortedHorizons = useMemo(
    () =>
      sortRows(monitor.horizons, sort, (row, key) => {
        switch (key) {
          case "window":
            return row.duration_sec;
          case "delta":
            return row.rolling_24h_delta_pp;
          case "recent_share":
            return row.recent_flow_share_pct;
          case "expiring_share":
            return row.expiring_flow_share_pct;
          case "replacement_gap":
            return row.replacement_gap_pp;
          case "incoming_effect":
            return row.incoming_effect_pp;
          case "expiring_effect":
            return row.expiring_effect_pp;
          case "status":
            return row.status;
          default:
            return null;
        }
      }),
    [monitor.horizons, sort]
  );
  return (
    <Panel
      title="구간별 변화와 거래 교체"
      description="최근 24시간 점유율 변화와, 같은 길이만큼 새로 들어오고 범위에서 빠진 거래 중 빗썸 몫은 서로 다른 값입니다."
      icon={<Repeat2 className="h-4 w-4" />}
    >
      <div className="overflow-x-auto">
        <table
          className="market-data-table w-full text-left text-xs"
          data-table-schema="label-numeric-status"
        >
          <thead>
            <tr>
              <SortHeader
                label="구간"
                sortKey="window"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
              />
              <SortHeader
                label="24시간 실제 변화"
                sortKey="delta"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="최근 신규 구간 · 빗썸 몫 · 전체 금액"
                sortKey="recent_share"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="24시간 범위에서 빠진 거래 · 빗썸 몫 · 전체 금액"
                sortKey="expiring_share"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="교체 점유율 차이"
                sortKey="replacement_gap"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="신규 거래 기여"
                sortKey="incoming_effect"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="24시간 범위에서 빠진 거래의 영향"
                sortKey="expiring_effect"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
                align="right"
              />
              <SortHeader
                label="상태"
                sortKey="status"
                sort={sort}
                onSort={key => toggleSort(setSort, key)}
              />
            </tr>
          </thead>
          <tbody>
            {sortedHorizons.map(row => {
              const ready = row.status === "CONFIRMED";
              return (
                <tr key={row.window}>
                  <td className="font-semibold">
                    {displayWindowLabel(row.window)}
                  </td>
                  <td
                    className={
                      "text-right " +
                      changeTone(row.rolling_24h_delta_pp)
                    }
                  >
                    {points(ready ? row.rolling_24h_delta_pp : null)}
                  </td>
                  <td className="text-right">
                    <span>
                      {percent(ready ? row.recent_flow_share_pct : null)}
                    </span>
                    <br />
                    <span className="text-base text-muted-foreground">
                      {ready
                        ? flowMoney(
                            row.recent_flow_share_pct,
                            row.recent_flow_volume_krw
                          )
                        : statusLabel(row.status)}
                    </span>
                  </td>
                  <td className="text-right">
                    <span>
                      {percent(ready ? row.expiring_flow_share_pct : null)}
                    </span>
                    <br />
                    <span className="text-base text-muted-foreground">
                      {ready
                        ? flowMoney(
                            row.expiring_flow_share_pct,
                            row.expiring_flow_volume_krw
                          )
                        : statusLabel(row.status)}
                    </span>
                  </td>
                  <td
                    className={
                      "text-right " + changeTone(row.replacement_gap_pp)
                    }
                  >
                    {points(ready ? row.replacement_gap_pp : null)}
                  </td>
                  <td
                    className={
                      "text-right " + changeTone(row.incoming_effect_pp)
                    }
                  >
                    {points(ready ? row.incoming_effect_pp : null)}
                  </td>
                  <td
                    className={
                      "text-right " + changeTone(row.expiring_effect_pp)
                    }
                  >
                    {points(ready ? row.expiring_effect_pp : null)}
                  </td>
                  <td>
                    <span
                      className={
                        row.anomaly?.status === "UNUSUAL"
                          ? "text-gold"
                          : "text-muted-foreground"
                      }
                    >
                      {statusLabel(row.status)}
                      {row.anomaly
                        ? " · " + statusLabel(row.anomaly.status)
                        : ""}
                    </span>
                  </td>
                </tr>
              );
            })}
            <tr className="bg-inset">
              <td className="font-semibold">현재 24시간</td>
              <td className="text-right font-semibold">
                {percent(monitor.rolling_24h.current_share_pct)}
              </td>
              <td className="text-right text-muted-foreground">기준</td>
              <td className="text-right text-muted-foreground">—</td>
              <td className="text-right text-muted-foreground">—</td>
              <td className="text-right text-muted-foreground">—</td>
              <td className="text-right text-muted-foreground">—</td>
              <td>{statusLabel(monitor.rolling_24h.status)}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div className="border-t border-border px-4 py-3 text-base leading-6 text-muted-foreground">
        들어오고 빠진 거래의 빗썸 몫은 해당 짧은 흐름 안의 비중이고, 전체 24시간
        24시간 점유율이 아닙니다. 확정 상태인 구간에서만 신규 거래 기여와 만료
        거래 기여의 합을 실제 점유율 변화로 읽습니다.
      </div>
    </Panel>
  );
}

function RollingTrend({ monitor }: { monitor: MarketShareRolling24h }) {
  const fallback = (
    <Panel
      title="24시간 점유율 추이"
      description="최근 24시간 점유율과 최근 10분에 들어오고 빠진 거래 흐름을 함께 표시합니다."
      icon={<Activity className="h-4 w-4" />}
    >
      <div className="min-h-56" aria-hidden="true" />
    </Panel>
  );

  return (
    <DeferredRender fallback={fallback}>
      <Suspense fallback={fallback}>
        <LazyRollingTrendChart monitor={monitor} />
      </Suspense>
    </DeferredRender>
  );
}


const NoticeRow = memo(function NoticeRow({
  notice,
}: {
  notice: MarketShareRollingNotice;
}) {
  const [open, setOpen] = useState(false);
  const brief = notice.brief;
  const relationClass = notice.related
    ? "border-gold/40 bg-gold/10 text-gold"
    : "border-border bg-inset text-muted-foreground";
  return (
    <details
      className="group"
      onToggle={event => setOpen(event.currentTarget.open)}
    >
      <summary className="cursor-pointer list-none px-4 py-3 transition-colors hover:bg-primary/5 focus-visible:bg-primary/10 focus-visible:outline-none">
        <div className="ui-caption flex flex-wrap items-center gap-2">
          <span className="font-semibold">
            {noticeExchangeLabel(notice.exchange)}
          </span>
          <span className={"border px-2 py-1 " + relationClass}>
            {notice.related_label}
          </span>
          <span className="text-muted-foreground">
            {noticeCategoryLabel(notice.category)}
          </span>
          <span className="text-muted-foreground">
            게시 {timestamp(notice.published_ts_ms)}
          </span>
          {notice.anchor_ts_ms &&
            notice.anchor_ts_ms !== notice.published_ts_ms && (
              <span className="text-muted-foreground">
                {labeledTimestamp("반응 기준", notice.anchor_ts_ms)}
              </span>
            )}
          {/* Routine "waiting" and "done" states were on every row; the row
              shows the analysis state only when something went wrong. */}
          {NOTICE_ROW_EXCEPTION_STATES.has(brief.analysis_status) && (
            <span className="text-gold">
              {statusLabel(brief.analysis_status)}
            </span>
          )}
          {notice.image_consistency && (
            <span className="text-muted-foreground">
              이미지·본문 {imageConsistencyLabel(notice.image_consistency)}
            </span>
          )}
          <ChevronDown
            className="ml-auto h-4 w-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-180"
            aria-hidden="true"
          />
        </div>
        <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
          <span className="min-w-0 whitespace-normal break-words text-sm font-semibold text-foreground">
            {notice.title || "제목 없음"}
          </span>
          <span className="text-base text-muted-foreground">내용 보기</span>
        </div>
      </summary>
      {open && (
        <article className="border-t border-border px-4 py-4 transition-colors">
          <div className="flex flex-wrap items-start justify-between gap-2">
            {notice.url ? (
              <a
                href={notice.url}
                target="_blank"
                rel="noreferrer"
                className="text-sm font-semibold hover:underline"
              >
                원문 열기
              </a>
            ) : (
              <span className="text-sm font-semibold">원문 링크 없음</span>
            )}
            <span className="text-base text-muted-foreground">
              연결 근거 {notice.content_basis} · {notice.relation_reason} · 시간
              관계 {notice.timing_label}
            </span>
          </div>
          <div className="mt-2 flex flex-wrap gap-2 text-base text-muted-foreground">
            {notice.assets.map(asset => (
              <span key={asset} className="border border-border px-1.5 py-0.5">
                {asset}
              </span>
            ))}
          </div>
          <div className="mt-3 border border-border/80 bg-inset">
            <div className="flex flex-wrap items-center gap-2 px-3 py-2 text-xs font-semibold">
              <span>공지·이미지·점유율 통합 분석</span>
              <span className="text-muted-foreground">
                {brief.kind === "EVENT"
                  ? "이벤트 일정"
                  : noticeCategoryLabel(notice.category)}
              </span>
              <span className="text-muted-foreground">
                {notice.related_label}
              </span>
            </div>
            <div className="space-y-2 border-t border-border px-3 py-3 text-xs">
              <div className="grid gap-1 sm:grid-cols-[8rem_1fr]">
                <span className="text-muted-foreground">점유율 연결</span>
                <span className="leading-6">
                  {notice.relation_reason} · {notice.timing_label}
                </span>
              </div>
              {brief.lines.slice(0, 5).map(line => (
                <div
                  key={line.label}
                  className="grid gap-1 sm:grid-cols-[8rem_1fr]"
                >
                  <span className="text-muted-foreground">{line.label}</span>
                  <span className="leading-6">
                    {noticeSummaryDisplayText(line.value)}
                  </span>
                </div>
              ))}
              {brief.lines.length === 0 && (
                <p className="leading-6 text-muted-foreground">
                  Hermes가 공지 본문·이벤트 기간·이미지 문구를 분석하고
                  있습니다.
                </p>
              )}
              {notice.image_consistency && (
                <div
                  className={
                    "border px-3 py-3 " +
                    (notice.image_consistency.status === "CONFLICT"
                      ? "border-loss/50 bg-loss/10"
                      : "border-border/70 bg-well")
                  }
                >
                  <div className="flex flex-wrap items-center gap-2 font-semibold">
                    <span>이미지·본문</span>
                    <span className="text-gold">
                      {imageConsistencyLabel(notice.image_consistency)}
                    </span>
                  </div>
                  {notice.image_consistency.semantic_types.length > 0 && (
                    <p className="mt-2 leading-6 text-muted-foreground">
                      이미지 유형:{" "}
                      {notice.image_consistency.semantic_types
                        .map(imageSemanticLabel)
                        .join(" · ")}
                    </p>
                  )}
                  {notice.image_consistency.matched_signals.length > 0 && (
                    <p className="mt-1 leading-6 text-muted-foreground">
                      확인된 신호:{" "}
                      {notice.image_consistency.matched_signals
                        .map(imageSignalLabel)
                        .join(" · ")}
                    </p>
                  )}
                  {notice.image_consistency.conflict_signals.length > 0 && (
                    <p className="mt-1 leading-6 text-loss">
                      충돌 신호:{" "}
                      {notice.image_consistency.conflict_signals
                        .map(imageSignalLabel)
                        .join(" · ")}
                    </p>
                  )}
                  {(notice.image_consistency.unverified_signals?.length ?? 0) >
                    0 && (
                    <p className="mt-1 leading-6 text-gold">
                      직접 확인 필요:{" "}
                      {notice.image_consistency.unverified_signals
                        ?.map(imageSignalLabel)
                        .join(" · ")}
                    </p>
                  )}
                  <p className="mt-2 leading-6 text-muted-foreground">
                    {notice.image_consistency.reason}
                  </p>
                </div>
              )}
              <p className="border-t border-border pt-2 text-base leading-6 text-muted-foreground">
                공지 본문·일정·이미지 신호와 점유율 분석기의 실제 관측 구간을
                함께 보여줍니다. 시간상 연결은 표시하지만 공지가 변화의
                원인이라고 단정하지 않습니다.
              </p>
            </div>
          </div>
        </article>
      )}
    </details>
  );
});


function NoticeEvidence({
  monitor,
  relatedNoticesOnly,
}: {
  monitor: MarketShareRolling24h;
  relatedNoticesOnly: boolean;
}) {
  const retainedEvidenceRef = useRef<MarketShareRolling24h["evidence"] | null>(
    null
  );
  const evidence = selectRollingNoticeEvidence(
    monitor.evidence,
    retainedEvidenceRef.current
  );
  retainedEvidenceRef.current = evidence;
  const [selectedExchanges, setSelectedExchanges] = useState<string[]>(
    NOTICE_EXCHANGES.map(exchange => exchange.id)
  );
  const notices = useMemo(
    () => dedupeNoticeRows(evidence.domestic_notices),
    [evidence.domestic_notices]
  );
  // Keep the restored timeline's selection and date range unchanged; only
  // narrow screens switch to the compact layout (see compactTimeline).
  const timeline = useMemo(
    () =>
      buildNoticeTimeline(
        NOTICE_EXCHANGES.filter(exchange =>
          selectedExchanges.includes(exchange.id)
        ).flatMap(exchange =>
          notices
            .filter(notice => notice.exchange === exchange.id)
            .slice(0, NOTICE_PREVIEW_LIMIT)
        ),
        monitor.as_of_ms
      ),
    [notices, selectedExchanges, monitor.as_of_ms]
  );
  const visibleNotices = useMemo(
    () =>
      relatedNoticesOnly
        ? selectRelevantRollingNotices(
            notices,
            timeline.rows.map(row => row.notice)
          )
        : notices,
    [notices, timeline, relatedNoticesOnly]
  );
  const groups = useMemo(
    () =>
      NOTICE_EXCHANGES.filter(exchange =>
        selectedExchanges.includes(exchange.id)
      ).map(exchange => ({
        ...exchange,
        notices: visibleNotices
          .filter(notice => notice.exchange === exchange.id)
          .slice(0, NOTICE_PREVIEW_LIMIT)
          .sort((left, right) => right.published_ts_ms - left.published_ts_ms),
      })),
    [visibleNotices, selectedExchanges]
  );
  const selectedCount = groups.reduce(
    (total, group) => total + group.notices.length,
    0
  );
  const storedCount = Object.values(
    evidence.domestic_notice_available_counts ?? {}
  ).reduce((total, count) => total + count, 0);
  const storedCountLabel =
    evidence.domestic_notice_available_counts === undefined
      ? "확인 불가"
      : storedCount.toLocaleString("ko-KR") + "건";
  const toggleExchange = (exchange: string) => {
    setSelectedExchanges(current => {
      if (current.length === NOTICE_EXCHANGES.length) return [exchange];
      if (current.includes(exchange)) {
        return current.length === 1
          ? current
          : current.filter(item => item !== exchange);
      }
      return [...current, exchange];
    });
  };
  const selectAll = () =>
    setSelectedExchanges(NOTICE_EXCHANGES.map(exchange => exchange.id));
  // Below this width the three text columns no longer fit beside the dates,
  // so the timeline uses one sticky info column instead.
  const compactTimeline = useMediaQuery("(max-width: 1023px)");
  return (
    <Panel
      title={
        <span className="inline-help-heading">
          관련 공지
          {relatedNoticesOnly ? (
            <InlineHelp text="빗썸 점유율 변화와 자산·시간·일정의 연관 근거가 있거나 위 타임라인에 표시된 공지만 보여줍니다. 빗썸이 올린 공지라는 이유만으로 포함하지 않습니다. 연관성이 있어도 공지가 변화의 원인이라고 확정하지 않습니다." />
          ) : (
            <InlineHelp text="업비트·빗썸 공지 중 분석 시점에 진행 중인 일정만 보여줍니다. 상장·입출금·유의종목은 공지가 게시된 때와 일정의 시작·종료 시점도 반응 기준으로 포함합니다. 시간이나 자산이 겹친다는 사실은 인과관계 확정이 아닙니다." />
          )}
        </span>
      }
      icon={<Bell className="h-4 w-4" />}
      className="notice-evidence-panel"
      actions={
        <span className="text-xs text-muted-foreground">
          선택 {selectedExchanges.length}개 ·{" "}
          {relatedNoticesOnly ? "표시" : "최근 등록"} {selectedCount}건 · 저장{" "}
          {storedCountLabel}
        </span>
      }
    >
      <NoticeTimeline
        timeline={timeline}
        asOfMs={monitor.as_of_ms}
        compact={compactTimeline}
      />
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
        <button
          type="button"
          onClick={selectAll}
          className={
            "border px-3 py-1.5 text-xs font-semibold transition-colors " +
            (selectedExchanges.length === NOTICE_EXCHANGES.length
              ? "border-primary/45 bg-primary/10 text-primary"
              : "border-border text-muted-foreground hover:border-primary/40 hover:text-primary")
          }
        >
          전체
        </button>
        {NOTICE_EXCHANGES.map(exchange => {
          const selected = selectedExchanges.includes(exchange.id);
          const count = relatedNoticesOnly
            ? visibleNotices
                .filter(notice => notice.exchange === exchange.id)
                .slice(0, NOTICE_PREVIEW_LIMIT).length
            : (evidence.domestic_notice_counts?.[exchange.id] ??
              notices.filter(notice => notice.exchange === exchange.id).length);
          return (
            <button
              key={exchange.id}
              type="button"
              aria-pressed={selected}
              onClick={() => toggleExchange(exchange.id)}
              className={
                "border px-3 py-1.5 text-xs transition-colors " +
                (selected
                  ? "border-primary/45 bg-primary/10 text-primary"
                  : "border-border text-muted-foreground hover:border-primary/40 hover:text-primary")
              }
            >
              {exchange.label}{" "}
              <span className="ml-1 text-base opacity-75">{count}</span>
            </button>
          );
        })}
      </div>
      {selectedExchanges.some(
        exchange =>
          evidence.domestic_notice_sources?.[exchange]?.status !== "OK"
      ) && (
        <div className="border-b border-gold/30 bg-gold/10 px-4 py-3 text-base leading-6 text-gold">
          {selectedExchanges.map(exchange => {
            const source = evidence.domestic_notice_sources?.[exchange];
            if (!source || source.status === "OK") return null;
            const statusLabel =
              source.status === "ERROR"
                ? "공지 확인 오류"
                : source.status === "PENDING"
                  ? "공지를 처음 불러오는 중"
                  : `최근 ${source.fetched_count || 0}건 · ${noticeHistoryStatusLabel(source.history_status)}`;
            return (
              <span key={exchange} className="mr-3 inline-block">
                {noticeExchangeLabel(exchange)}: {statusLabel}
              </span>
            );
          })}
        </div>
      )}
      {groups.some(group => group.notices.length) ? (
        <div className="notice-evidence-list max-h-[44rem] space-y-4 overflow-auto p-4">
          {groups
            .filter(group => group.notices.length)
            .map(group => (
              <NoticeEvidenceGroup
                key={group.id}
                label={group.label}
                countLabel={relatedNoticesOnly ? "표시" : "최근 등록"}
                notices={group.notices}
              />
            ))}
        </div>
      ) : (
        <div className="flex items-start gap-3 p-5 text-xs">
          <Info className="mt-0.5 h-4 w-4 shrink-0 text-info" />
          <div>
            <p className="font-semibold">
              {relatedNoticesOnly
                ? "조건에 맞는 공지 없음"
                : "선택한 거래소의 공지 없음"}
            </p>
            <p className="mt-1 leading-6 text-muted-foreground">
              {relatedNoticesOnly
                ? "선택한 거래소의 공지 중 빗썸 점유율과 연관되거나 타임라인에 표시된 공지가 없습니다."
                : "공지 수집이 아직 시작되지 않았거나 선택한 기간에 저장된 공지가 없습니다."}
            </p>
          </div>
        </div>
      )}
    </Panel>
  );
}

function NoticeEvidenceGroup({
  label,
  countLabel,
  notices,
}: {
  label: string;
  countLabel: string;
  notices: MarketShareRollingNotice[];
}) {
  const [expanded, setExpanded] = useState(false);
  return (
    <section className="overflow-hidden border-y border-border">
      <div className="flex items-center justify-between border-b border-border bg-inset px-4 py-3">
        <h4 className="text-xs font-semibold">{label} 공지</h4>
        <span className="text-base text-muted-foreground">
          {countLabel} {notices.length}건
        </span>
      </div>
      <div
        className={`divide-y divide-border${expanded ? "" : " mobile-collapsed"}`}
      >
        {notices.map(notice => (
          <NoticeRow
            key={notice.exchange + "-" + notice.notice_id}
            notice={notice}
          />
        ))}
      </div>
      {notices.length > MOBILE_VISIBLE_ROWS && (
        <MobileMoreToggle
          expanded={expanded}
          onToggle={() => setExpanded(value => !value)}
          moreLabel={`${label} 공지 ${notices.length - MOBILE_VISIBLE_ROWS}건 더 보기`}
        />
      )}
    </section>
  );
}

function ReactionContext({
  reaction,
}: {
  reaction: MarketShareReactionSummary | null;
}) {
  const origin = reaction?.origin;
  const topNotice = reaction?.top_notice;
  return (
    <Panel
      title="국내 시장 반응의 시간상 맥락"
      description="국내 공지와 국내 시장 반응의 시간 관계만 보조로 기록합니다. 시간상 선후는 인과관계가 아닙니다."
      icon={<GitBranch className="h-4 w-4" />}
    >
      {!reaction || !origin ? (
        <div className="p-4 text-xs text-muted-foreground">
          국내 반응 요약 자료를 아직 확보하지 못해 시간상 맥락을 비교하지
          않습니다.
        </div>
      ) : (
        <div className="grid gap-4 p-4 text-xs md:grid-cols-[0.8fr_1.2fr]">
          <div className="rounded-md bg-inset p-3">
            <p className="text-muted-foreground">현재 판단</p>
            <p className="mt-1 text-sm font-semibold">{origin.label}</p>
            <p className="mt-2 leading-6 text-muted-foreground">
              {evidenceStrengthLabel(origin.evidence_level)} ·{" "}
              {labeledTimestamp(
                "국내 반응 시각",
                origin.domestic_reaction_time_ms
              )}
            </p>
          </div>
          <div className="rounded-md bg-inset p-3">
            <p className="text-muted-foreground">연결 우선 공지</p>
            {topNotice ? (
              <>
                <p className="mt-1 font-semibold">
                  {noticeExchangeLabel(topNotice.exchange)} ·{" "}
                  {topNotice.title || "제목 없음"}
                </p>
                <p className="mt-2 leading-6 text-muted-foreground">
                  {topNotice.reaction_label} · 연결 코인{" "}
                  {topNotice.linked_assets.length}개의 현재 구간 기여{" "}
                  {points(
                    topNotice.linked_asset_current_window_contribution_pp
                  )}
                </p>
              </>
            ) : (
              <p className="mt-1 leading-6 text-muted-foreground">
                연결된 국내 공지가 없어 시간상 맥락을 비교하지 않습니다.
              </p>
            )}
          </div>
        </div>
      )}
    </Panel>
  );
}

function BridgeSelector({
  bridges,
  selectedDuration,
  onChange,
}: {
  bridges: MarketShareRolling24h["bridge"][];
  selectedDuration: number;
  onChange: (duration: number) => void;
}) {
  const selectedBridge =
    bridges.find(bridge => bridge.duration_sec === selectedDuration) ??
    bridges[0];
  const selectedStatus = selectedBridge?.status ?? "UNAVAILABLE";
  return (
    <Panel
      title="변화 분해 기준"
      description="비교 시간을 바꾸면 새로 들어온 거래와 24시간 범위에서 빠진 거래의 영향도 달라집니다. 계산이 끝나기 전에는 먼저 확인된 값만 표시합니다."
      icon={<Repeat2 className="h-4 w-4" />}
    >
      <div className="flex flex-wrap items-center gap-3 p-4 text-xs">
        <label
          className="flex items-center gap-2 font-semibold"
          htmlFor="summary2-bridge-window"
        >
          비교 구간
          <select
            id="summary2-bridge-window"
            value={selectedDuration}
            onChange={event => onChange(Number(event.target.value))}
            className="border border-border bg-inset px-3 py-2 font-normal outline-none focus:border-info"
          >
            {bridges.map(bridge => (
              <option
                key={bridge.duration_sec ?? bridge.window}
                value={bridge.duration_sec ?? 0}
              >
                {displayWindowLabel(bridge.window)} ·{" "}
                {statusLabel(bridge.status)}
              </option>
            ))}
          </select>
        </label>
        <StatusBadge
          label={"선택 기준 " + displayWindowLabel(selectedBridge?.window)}
          tone="muted"
        />
        <StatusBadge
          label={statusLabel(selectedStatus)}
          tone={qualityTone(selectedStatus)}
        />
        <span className="text-muted-foreground">
          계산 불가 구간은 공백을 임의로 채우지 않습니다.
        </span>
      </div>
    </Panel>
  );
}

function ClosedMinuteComparisonPanel({
  comparisons,
  selectedDuration,
  onChange,
}: {
  comparisons: MarketShareClosedMinuteComparison[];
  selectedDuration: number;
  onChange: (duration: number) => void;
}) {
  const selected =
    comparisons.find(
      comparison => comparison.duration_sec === selectedDuration
    ) ?? comparisons[0];
  if (!selected) return null;
  const comparisonReady =
    selected.status === "CONFIRMED" &&
    finite(selected.previous_share_pct) &&
    finite(selected.current_share_pct);
  return (
    <Panel
      title="완료된 1분 체결 비교"
      description="현재 진행 중인 1분은 제외합니다. 저장된 실시간 체결을 완전히 종료된 1분 단위로 묶어 최근 구간과 직전 동일 구간을 비교합니다."
      icon={<BarChart3 className="h-4 w-4" />}
      actions={
        <span className="text-xs text-muted-foreground">
          {labeledTimestamp("마감 시각", selected.closed_at_ms)}
        </span>
      }
    >
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3 text-xs">
        <label
          className="flex items-center gap-2 font-semibold"
          htmlFor="summary2-closed-minute-window"
        >
          비교 구간
          <select
            id="summary2-closed-minute-window"
            value={selected.duration_sec}
            onChange={event => onChange(Number(event.target.value))}
            className="border border-border bg-inset px-3 py-2 font-normal outline-none focus:border-info"
          >
            {comparisons.map(comparison => (
              <option
                key={comparison.duration_sec}
                value={comparison.duration_sec}
              >
                {displayWindowLabel(comparison.window)} ·{" "}
                {statusLabel(comparison.status)}
              </option>
            ))}
          </select>
        </label>
        <StatusBadge
          label={statusLabel(selected.status)}
          tone={qualityTone(selected.status)}
        />
        <span className="text-muted-foreground">
          {comparisonReady
            ? `직전 ${displayWindowLabel(selected.window)} ${percent(selected.previous_share_pct)} → 최근 ${displayWindowLabel(selected.window)} ${percent(selected.current_share_pct)} · ${points(selected.share_delta_pp)}`
            : "직전·최근 구간이 모두 확정될 때 비교값을 표시합니다."}
        </span>
      </div>
      {comparisonReady && selected.asset_changes.length ? (
        <div className="max-h-[31rem] overflow-auto">
          <table className="data-table" data-table-schema="label-numeric-label">
            <thead>
              <tr>
                <th>코인</th>
                <th className="text-right">빗썸 이전</th>
                <th className="text-right">빗썸 최근</th>
                <th className="text-right">빗썸 증감</th>
                <th className="text-right">경쟁 거래소 증감</th>
                <th className="text-right">국내 합계 증감</th>
                <th>읽는 법</th>
              </tr>
            </thead>
            <tbody>
              {selected.asset_changes.map(row => (
                <tr key={row.base}>
                  <td className="font-semibold">{row.base}</td>
                  <td className="text-right">
                    {money(row.previous_bithumb_volume_krw)}
                  </td>
                  <td className="text-right">
                    {money(row.current_bithumb_volume_krw)}
                  </td>
                  <td
                    className={
                      "text-right " +
                      changeTone(row.bithumb_volume_change_krw)
                    }
                  >
                    {signedMoney(row.bithumb_volume_change_krw)}
                  </td>
                  <td
                    className={
                      "text-right " +
                      changeTone(row.competitor_volume_change_krw)
                    }
                  >
                    {signedMoney(row.competitor_volume_change_krw)}
                  </td>
                  <td
                    className={
                      "text-right " + changeTone(row.domestic_volume_change_krw)
                    }
                  >
                    {signedMoney(row.domestic_volume_change_krw)}
                  </td>
                  <td className="max-w-72 whitespace-normal text-xs text-muted-foreground">
                    {row.reason}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="p-4 text-xs text-muted-foreground">
          {comparisonReady
            ? "두 비교 구간의 실시간 체결 자료가 아직 없습니다."
            : "일부 누락 구간의 점유율과 코인별 증감은 숨깁니다."}
        </div>
      )}
    </Panel>
  );
}

const SUMMARY_CHAPTERS = {
  current: { step: "01", eyebrow: "현재 수준" },
  hourly: { step: "02", eyebrow: "시간별 변화의 구성" },
  realtime: { step: "03", eyebrow: "실시간 직접 관측" },
  context: { step: "04", eyebrow: "장기 맥락과 신뢰도" },
} as const;
const SUMMARY_CHAPTER_LIST = Object.values(SUMMARY_CHAPTERS);

const chapterID = (step: string) => `summary-chapter-${step}`;

/** The summary is about ten screens long; jump between its four chapters. */
function SummaryChapterNav() {
  const [active, setActive] = useState<string>(SUMMARY_CHAPTER_LIST[0].step);
  // Phones hide the bar while reading downward and bring it back on the
  // first scroll up; the CSS applies this only at phone width.
  const [hidden, setHidden] = useState(false);
  const navRef = useRef<HTMLElement>(null);
  useEffect(() => {
    // The current chapter is the last one whose header is above 30% of the
    // view. Checking on every scroll (not only when a header enters a band)
    // keeps it right after jumps and when scrolling back up.
    let frame = 0;
    const scroller = scrollParent(navRef.current);
    let lastTop = scroller.scrollTop;
    const update = () => {
      frame = 0;
      const line = window.innerHeight * 0.3;
      let current = SUMMARY_CHAPTER_LIST[0].step;
      for (const { step } of SUMMARY_CHAPTER_LIST) {
        const header = document.getElementById(chapterID(step));
        if (header && header.getBoundingClientRect().top <= line) {
          current = step;
        }
      }
      setActive(current);

      const top = scroller.scrollTop;
      const keyboardInside = navRef.current?.contains(document.activeElement);
      if (Math.abs(top - lastTop) >= 8) {
        setHidden(top > lastTop && top > 160 && !keyboardInside);
        lastTop = top;
      }
    };
    const schedule = () => {
      if (!frame) frame = window.requestAnimationFrame(update);
    };
    document.addEventListener("scroll", schedule, {
      capture: true,
      passive: true,
    });
    update();
    return () => {
      document.removeEventListener("scroll", schedule, { capture: true });
      window.cancelAnimationFrame(frame);
    };
  }, []);
  return (
    <nav
      ref={navRef}
      className="summary-chapter-nav"
      aria-label="요약 장 이동"
      data-hidden={hidden ? "true" : undefined}
    >
      {SUMMARY_CHAPTER_LIST.map(({ step, eyebrow }) => (
        <button
          key={step}
          type="button"
          aria-current={active === step ? "true" : undefined}
          onClick={() =>
            document
              .getElementById(chapterID(step))
              ?.scrollIntoView({ behavior: "smooth", block: "start" })
          }
        >
          <b>{step}</b>
          {eyebrow}
        </button>
      ))}
    </nav>
  );
}

/** The nearest vertically scrolling ancestor, or the page itself. */
function scrollParent(element: Element | null): Element {
  for (let node = element?.parentElement; node; node = node.parentElement) {
    if (/(auto|scroll)/.test(getComputedStyle(node).overflowY)) return node;
  }
  return document.scrollingElement ?? document.documentElement;
}

function AnalysisChapterHeader({
  step,
  eyebrow,
  title,
  description,
  helpText,
}: {
  step: string;
  eyebrow: string;
  title: string;
  description?: string;
  helpText?: string;
}) {
  return (
    // The chapter nav already names each chapter; the header shows the step
    // and the question it answers, without a third label above it.
    <header
      className="official-analysis-chapter"
      id={chapterID(step)}
      aria-label={`${step} ${eyebrow}`}
    >
      <span>{step}</span>
      <div>
        <h2 className={helpText ? "inline-help-heading" : undefined}>
          {title}
          {helpText && <InlineHelp text={helpText} />}
        </h2>
        {description && <small>{description}</small>}
      </div>
    </header>
  );
}

function Summary2Page({
  monitor,
  officialMonitor,
  officialTrend,
  official,
  reaction,
  tickStore,
  realtimeFlowStore,
  connection,
  error,
  fallbackRealtimeFlow,
  relatedNoticesOnly,
  preciseHourlyMetrics,
}: {
  monitor: MarketShareRolling24h;
  officialMonitor: MarketShareOfficialMovementState | null;
  officialTrend: MarketShareOfficialTicker1mState | null;
  official: MarketShareOfficialDomestic24h;
  reaction: MarketShareReactionSummary | null;
  tickStore: MarketShareTickStore;
  realtimeFlowStore: MarketShareRealtimeFlowStore;
  connection: MarketShareConnection;
  error: string;
  fallbackRealtimeFlow: MarketShareAnalysis["realtime_flow"] | null;
  relatedNoticesOnly: boolean;
  preciseHourlyMetrics: boolean;
}) {
  const retainedOfficialTrendRef =
    useRef<MarketShareOfficialTicker1mState | null>(
      officialTrendSnapshotIsComplete(officialTrend) ? officialTrend : null
    );
  const displayedOfficialTrend = selectStableOfficialTrend(
    officialTrend,
    retainedOfficialTrendRef.current
  );
  if (officialTrendSnapshotIsComplete(displayedOfficialTrend)) {
    retainedOfficialTrendRef.current = displayedOfficialTrend;
  }
  const bridgeOptions = useMemo(
    () => (monitor.bridges?.length ? monitor.bridges : [monitor.bridge]),
    [monitor.bridges, monitor.bridge]
  );
  const [bridgeDuration, setBridgeDuration] = useState<number>(
    MARKET_SHARE_WINDOW_SEC.TEN_MINUTES
  );
  const closedMinuteComparisons =
    monitor.closed_minute_comparisons?.comparisons ?? [];
  const [closedMinuteDuration, setClosedMinuteDuration] = useState<number>(
    MARKET_SHARE_WINDOW_SEC.THIRTY_MINUTES
  );
  useEffect(() => {
    if (!bridgeOptions.some(bridge => bridge.duration_sec === bridgeDuration)) {
      setBridgeDuration(
        bridgeOptions[0]?.duration_sec ?? MARKET_SHARE_WINDOW_SEC.TEN_MINUTES
      );
    }
  }, [bridgeDuration, bridgeOptions]);
  useEffect(() => {
    if (
      !closedMinuteComparisons.some(
        comparison => comparison.duration_sec === closedMinuteDuration
      )
    ) {
      setClosedMinuteDuration(
        closedMinuteComparisons[0]?.duration_sec ??
          MARKET_SHARE_WINDOW_SEC.THIRTY_MINUTES
      );
    }
  }, [closedMinuteComparisons, closedMinuteDuration]);
  const selectedBridge =
    bridgeOptions.find(bridge => bridge.duration_sec === bridgeDuration) ??
    monitor.bridge;
  const current = monitor.rolling_24h;
  const currentReady = rollingWindowIsComplete(current);
  const movement = monitor.movement;
  const bridgeReady =
    currentReady &&
    selectedBridge.status === "CONFIRMED" &&
    finite(selectedBridge.total_effect_pp);
  const selectedHorizon = monitor.horizons.find(
    row => row.duration_sec === (selectedBridge.duration_sec ?? 0)
  );
  const observedFallback =
    selectedHorizon &&
    (finite(selectedHorizon.recent_flow_share_pct) ||
      finite(selectedHorizon.expiring_flow_share_pct) ||
      finite(selectedHorizon.recent_flow_volume_krw) ||
      finite(selectedHorizon.expiring_flow_volume_krw))
      ? {
          base: "전체 국내 흐름",
          recent_bithumb_volume_krw:
            selectedHorizon.recent_bithumb_volume_krw ?? null,
          recent_competitor_volume_krw:
            selectedHorizon.recent_competitor_volume_krw ?? null,
          recent_volume_krw: selectedHorizon.recent_flow_volume_krw ?? null,
          recent_flow_share_pct: selectedHorizon.recent_flow_share_pct ?? null,
          expiring_bithumb_volume_krw:
            selectedHorizon.expiring_bithumb_volume_krw ?? null,
          expiring_competitor_volume_krw:
            selectedHorizon.expiring_competitor_volume_krw ?? null,
          expiring_volume_krw: selectedHorizon.expiring_flow_volume_krw ?? null,
          expiring_flow_share_pct:
            selectedHorizon.expiring_flow_share_pct ?? null,
          replacement_gap_pp: selectedHorizon.replacement_gap_pp ?? null,
          recent_vs_expiring_volume_change_pct: null,
          reason:
            "코인·거래소별 계산 전, 확인된 들어온 거래와 빠진 거래의 합계만 표시",
        }
      : null;
  const hasObservedBridge = Boolean(
    selectedBridge.observed_asset_drivers?.length ||
    selectedBridge.observed_exchange_drivers?.length ||
    observedFallback
  );
  const movementBlocked = !currentReady || movementIsBlocked(monitor);
  const movementGap = movementGapMessage(monitor);
  return (
    <div className="w-full space-y-6">
      {error && (
        <div className="flex items-start gap-3 border border-gold/30 bg-gold/10 p-3 text-xs text-gold">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          {error}
        </div>
      )}
      <SummaryChapterNav />
      <AnalysisChapterHeader
        {...SUMMARY_CHAPTERS.current}
        title="지금 공식 24시간 점유율"
        helpText="국내 5곳의 최신 공식 24시간 자료를 대표값으로 사용합니다. 아래 시간 비교값이 집계하는 국내시장과 다릅니다."
      />
      {officialMonitor && (
        <OfficialMovementSummary
          monitor={officialMonitor}
          fallbackOfficial={official}
          trend={displayedOfficialTrend}
          tickStore={tickStore}
        />
      )}
      <AnalysisChapterHeader
        {...SUMMARY_CHAPTERS.hourly}
        title="언제 움직였고 어떤 코인이 변화를 만들었는지"
        helpText="시간대를 누르면 해당 1시간의 상승 기여 TOP 20과 하락 기여 TOP 20을 동시에 펼쳐 보여줍니다."
      />
      {displayedOfficialTrend && (
        <OfficialTickerTrendPanel
          trend={displayedOfficialTrend}
          preciseHourlyMetrics={preciseHourlyMetrics}
        />
      )}
      <AnalysisChapterHeader
        {...SUMMARY_CHAPTERS.realtime}
        title="10초 점유율 변화 감시 · 지금도 같은 방향인지"
        helpText="최근 완료된 공식 1시간 방향과 현재 10초 방향의 일치·반대·중립을 먼저 판정하고, 국내 5곳의 거래소 표는 아래 근거로 보여줍니다."
      />
      <RealtimeFlowPanel
        tickStore={tickStore}
        realtimeFlowStore={realtimeFlowStore}
        connection={connection}
        fallbackState={fallbackRealtimeFlow}
        officialHourReference={
          displayedOfficialTrend?.status === "LIVE" &&
          displayedOfficialTrend.current
            ? {
                shareDeltaPP: displayedOfficialTrend.share_delta_pp,
                observedAtMS: displayedOfficialTrend.current.observed_at_ms,
              }
            : null
        }
      />
      <RealtimeObservationPanel
        tickStore={tickStore}
        connection={connection}
        analysisReady
        rollingCoverage={current.coverage}
      />

      <AnalysisChapterHeader
        {...SUMMARY_CHAPTERS.context}
        title="직접 관측한 24시간 중 이 변화가 평소보다 큰지"
        helpText="자료 품질은 한 줄로 요약합니다. 24시간 자료가 아직 다 쌓이지 않았으면 분석 대신 쌓인 정도를 보여줍니다."
      />
      {!currentReady && (
        // The chapter asks a question its panels can only answer with a full
        // observed day; say why they are absent instead of leaving the gap.
        <section
          className="glass-card flex items-start gap-3 p-4 text-sm leading-6"
          role="status"
        >
          <Info className="mt-1 h-4 w-4 shrink-0 text-gold" />
          <div>
            <p className="font-semibold">
              실시간 체결이 24시간 중 일부만 모여 평소 대비 비교는
              보류합니다.
            </p>
            <p className="text-muted-foreground">
              {formatObservedWindowCoverage(
                current.coverage.coverage_sec,
                current.coverage.required_sec,
                current.coverage.gap_seconds
              )}
              . 24시간이 모두 관측되면 변화 크기 비교와 변화 분해가 여기에
              표시됩니다.
            </p>
          </div>
        </section>
      )}
      {currentReady && (
        <>
          <HorizonTable monitor={monitor} />
          <ClosedMinuteComparisonPanel
            comparisons={closedMinuteComparisons}
            selectedDuration={closedMinuteDuration}
            onChange={setClosedMinuteDuration}
          />
        </>
      )}
      {currentReady && (
        <>
          <div className="grid gap-6 xl:grid-cols-[1.2fr_0.8fr]">
            <RollingTrend monitor={monitor} />
            <Panel
              title="변화 구간 요약"
              description="한 번의 짧은 체결이 아니라 여러 구간의 같은 방향을 하나의 변화 구간으로 관리합니다."
              icon={<Clock3 className="h-4 w-4" />}
            >
              <div className="space-y-4 p-4 text-xs">
                <div className="border-b border-border pb-4">
                  <p className="text-muted-foreground">변화 구간 식별자</p>
                  <p className="mt-1 break-all font-mono text-base">
                    {movementBlocked
                      ? "자료 부족으로 판단 보류"
                      : (movement.movement_id ?? "아직 감지된 변화 없음")}
                  </p>
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <p className="text-muted-foreground">시작</p>
                    <p className="mt-1 font-semibold">
                      {timestamp(movement.started_at_ms)}
                    </p>
                  </div>
                  <div>
                    <p className="text-muted-foreground">지속</p>
                    <p className="mt-1 font-semibold">
                      {movement.duration_seconds
                        ? Math.floor(movement.duration_seconds / 60) + "분"
                        : "—"}
                    </p>
                  </div>
                  <div>
                    <p className="text-muted-foreground">시작 점유율</p>
                    <p className="mt-1 font-semibold">
                      {movementBlocked
                        ? "—"
                        : percent(movement.share_at_start_pct)}
                    </p>
                  </div>
                  <div>
                    <p className="text-muted-foreground">극값 점유율</p>
                    <p className="mt-1 font-semibold">
                      {movementBlocked
                        ? "—"
                        : percent(movement.extreme_share_pct)}
                    </p>
                  </div>
                </div>
                <p className="border border-gold/20 bg-gold/10 p-3 leading-6 text-muted-foreground">
                  {movementBlocked
                    ? movementGap
                    : "평소 기준선은 축적된 변화 분포의 중앙값과 편차로 계산합니다. 기준선이 충분히 쌓이기 전에는 ‘평소보다 큰 변화’로 확정하지 않습니다."}
                </p>
              </div>
            </Panel>
          </div>

          <BridgeSelector
            bridges={bridgeOptions}
            selectedDuration={bridgeDuration}
            onChange={setBridgeDuration}
          />
          {!bridgeReady && (
            <div className="border border-gold/30 bg-gold/10 px-4 py-4 text-xs leading-6 text-gold">
              <p className="font-semibold">점유율에 미친 영향은 아직 계산 중</p>
              <p className="mt-1 text-muted-foreground">
                {segmentCoverageLabel(
                  "새로 들어온 거래",
                  selectedHorizon?.coverage?.incoming
                )}
                <span> · </span>
                {segmentCoverageLabel(
                  "24시간 범위에서 빠진 거래",
                  selectedHorizon?.coverage?.expiring
                )}
                <span> · </span>
                전체 24시간{" "}
                {current.coverage.coverage_sec.toLocaleString("ko-KR")}/
                {current.coverage.required_sec.toLocaleString("ko-KR")}초
              </p>
              <p className="mt-1 text-muted-foreground">
                {hasObservedBridge
                  ? "아래 표에는 실제로 확인된 거래만 표시합니다. 빠지는 거래와 최근 24시간 자료가 모두 채워져야 코인별 영향과 10단계 호가를 계산합니다."
                  : "선택한 시간에 들어오고 빠진 거래가 확인되면 먼저 표시합니다."}
              </p>
            </div>
          )}
          <div>
            <DriverTable
              title={
                displayWindowLabel(selectedBridge.window) +
                (bridgeReady
                  ? " 주요 영향 코인"
                  : " 들어오고 빠진 거래가 확인된 코인")
              }
              drivers={bridgeReady ? selectedBridge.asset_drivers : []}
              observedDrivers={selectedBridge.observed_asset_drivers ?? []}
              observedFallback={observedFallback}
              kind="asset"
            />
          </div>
          {bridgeReady && <MicrostructureTable bridge={selectedBridge} />}
        </>
      )}
      {/* A group heading over its own cards; wrapping them in another card
          put a third border around every notice. */}
      <section>
        <header className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <h2 className="text-base font-semibold tracking-tight">
              공지와 시간상 반응
            </h2>
          </div>
        </header>
        <div className="mt-4 space-y-6">
          <NoticeEvidence
            monitor={monitor}
            relatedNoticesOnly={relatedNoticesOnly}
          />
          <ReactionContext reaction={reaction} />
          <Panel
            title="참고 분석"
            icon={<Activity className="h-4 w-4" />}
          >
            <div className="flex items-start gap-3 p-4 text-sm leading-6">
              <StatusBadge
                label={
                  monitor.briefing.status === "MEASURED"
                    ? "측정 기반"
                    : monitor.briefing.status === "DATA_INCOMPLETE"
                      ? "자료 부족"
                      : "계산 불가"
                }
                tone={
                  monitor.briefing.status === "MEASURED"
                    ? "live"
                    : monitor.briefing.status === "DATA_INCOMPLETE"
                      ? "warn"
                      : "muted"
                }
              />
              <p>{rollingBriefingTextLabel(monitor.briefing.text)}</p>
            </div>
          </Panel>
        </div>
      </section>
      <footer className="border-t border-border pt-3 text-base text-muted-foreground">
        <span>
          공지와 시장 변화가 시간상 겹쳐도 원인을 뜻하지는 않습니다.
        </span>
      </footer>
    </div>
  );
}

function latestOfficialContribution(
  trend: MarketShareOfficialTicker1mState
): MarketShareOfficialAlignedContribution {
  const observedSampleCount =
    Number(Boolean(trend.previous)) + Number(Boolean(trend.current));
  return {
    window_sec: trend.interval_sec || MARKET_SHARE_WINDOW_SEC.ONE_HOUR,
    status: trend.status,
    reason: trend.reason,
    previous: trend.previous,
    current: trend.current,
    share_delta_pp: trend.share_delta_pp,
    domestic_volume_delta_krw: trend.domestic_volume_delta_krw,
    bithumb_volume_delta_krw: trend.bithumb_volume_delta_krw,
    asset_drivers: trend.drivers ?? [],
    exchange_drivers: trend.exchange_drivers ?? [],
    observed_sample_count: observedSampleCount,
    expected_sample_count: 2,
    missing_sample_count: 2 - observedSampleCount,
    endpoint_comparison_available: Boolean(trend.previous && trend.current),
  };
}

function officialContributionRows(
  drivers: MarketShareOfficialMovementDriver[],
  direction: "up" | "down"
): MarketShareOfficialMovementDriver[] {
  return [...drivers]
    .filter(driver =>
      direction === "up"
        ? driver.contribution_pp > 0
        : driver.contribution_pp < 0
    )
    .sort((left, right) =>
      direction === "up"
        ? right.contribution_pp - left.contribution_pp
        : left.contribution_pp - right.contribution_pp
    )
    .slice(0, 20);
}

function OfficialContributionRankList({
  title,
  description,
  direction,
  rows,
  animationKey,
  available,
}: {
  title: string;
  description: string;
  direction: "up" | "down";
  rows: MarketShareOfficialMovementDriver[];
  animationKey: number;
  available: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const maximum = Math.max(
    ...rows.map(row => Math.abs(row.contribution_pp)),
    0
  );
  return (
    <section
      className={`official-contribution-rank official-contribution-rank-${direction}`}
      aria-label={title}
    >
      <div className="official-contribution-rank-header">
        <div>
          <p className="official-summary-eyebrow">
            {direction === "up"
              ? "점유율을 끌어올린 쪽"
              : "점유율을 끌어내린 쪽"}
          </p>
          <h3 className="inline-help-heading">
            {title}
            <InlineHelp text={description} />
          </h3>
        </div>
        <span>{available ? `${rows.length}개 표시` : "자료 대기"}</span>
      </div>
      <ol
        key={`${direction}-${animationKey}`}
        className={`official-contribution-rank-list${expanded ? "" : " mobile-collapsed"}`}
      >
        {rows.map((row, index) => {
          const width =
            maximum > 0
              ? Math.max(3, (Math.abs(row.contribution_pp) / maximum) * 100)
              : 0;
          return (
            <li key={`${direction}-${row.asset}`}>
              <span className="official-contribution-rank-number">
                {String(index + 1).padStart(2, "0")}
              </span>
              <strong>{row.asset}</strong>
              <span
                className="official-contribution-rank-bar"
                aria-hidden="true"
              >
                <span
                  className="official-contribution-rank-fill"
                  style={{ width: `${width}%` }}
                />
              </span>
              <span className={changeTone(row.contribution_pp)}>
                {points(row.contribution_pp)}
              </span>
              <small>
                빗썸 {signedMoney(row.bithumb_volume_change_krw)} · 경쟁{" "}
                {signedMoney(row.competitor_volume_change_krw)}
              </small>
            </li>
          );
        })}
      </ol>
      {rows.length > MOBILE_VISIBLE_ROWS && (
        <MobileMoreToggle
          expanded={expanded}
          onToggle={() => setExpanded(value => !value)}
          moreLabel={`나머지 ${rows.length - MOBILE_VISIBLE_ROWS}개 코인 보기`}
        />
      )}
      {rows.length === 0 && (
        <div className="official-contribution-empty">
          {available
            ? `이 시간에는 ${direction === "up" ? "상승" : "하락"} 기여가 확인된 코인이 없습니다.`
            : "코인별 계산 자료를 확인하고 있습니다."}
        </div>
      )}
    </section>
  );
}

function OfficialTickerTrendPanel({
  trend,
  preciseHourlyMetrics,
}: {
  trend: MarketShareOfficialTicker1mState;
  preciseHourlyMetrics: boolean;
}) {
  const latestEndMS =
    trend.current?.bucket_ms ?? trend.trend.at(-1)?.bucket_ms ?? 0;
  const historyFromMS = officialHourlyHistoryFromMS(latestEndMS);
  const previousLatestEndRef = useRef(latestEndMS);
  const [persistedHistory, setPersistedHistory] = useState<
    MarketShareOfficialTicker1mPoint[]
  >([]);
  const [historyIsComplete, setHistoryIsComplete] = useState(false);
  const [historyRefreshVersion, setHistoryRefreshVersion] = useState(0);
  const chartTrend = useMemo(
    () => ({
      ...trend,
      trend: mergeOfficialHourlyPoints(persistedHistory, trend.trend ?? []),
    }),
    [persistedHistory, trend]
  );
  const availableHourMS = useMemo(
    () =>
      new Set(
        buildOfficialHourlyChartSeries(
          chartTrend.trend,
          chartTrend.interval_sec || 3_600,
          OFFICIAL_HOURLY_CHART_POINT_COUNT
        )
          .filter(point => point.available)
          .map(point => point.bucket_ms)
      ),
    [chartTrend.interval_sec, chartTrend.trend]
  );
  const [selectedEndMS, setSelectedEndMS] = useState(() => {
    if (typeof window === "undefined") return latestEndMS;
    try {
      const restored = Number(
        window.sessionStorage.getItem(OFFICIAL_HOUR_SELECTION_KEY)
      );
      return restoreOfficialHourSelection(
        restored,
        availableHourMS,
        latestEndMS,
        chartTrend.interval_sec || 3_600
      );
    } catch {
      return latestEndMS;
    }
  });
  const latestContribution = useMemo(
    () => latestOfficialContribution(trend),
    [trend]
  );
  const [contribution, setContribution] =
    useState<MarketShareOfficialAlignedContribution>(() => {
      const restored = restoreOfficialContribution(selectedEndMS);
      if (restored) {
        retainedOfficialContributionByHour.set(selectedEndMS, restored);
        if (officialAlignedContributionIsComplete(restored, selectedEndMS)) {
          retainedOfficialContributionFullHours.add(selectedEndMS);
        }
        return restored;
      }
      return (
        retainedOfficialContributionByHour.get(selectedEndMS) ??
        latestContribution
      );
    });
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [contributionRefreshVersion, setContributionRefreshVersion] =
    useState(0);

  useEffect(() => {
    if (historyFromMS <= 0 || latestEndMS < historyFromMS) return;
    const controller = new AbortController();
    void getOfficialAlignedHistory(
      historyFromMS,
      latestEndMS,
      controller.signal
    )
      .then(payload => {
        if (controller.signal.aborted) return;
        setPersistedHistory(payload.items);
        setHistoryIsComplete(
          payload.status === "LIVE" &&
            payload.from_ms === historyFromMS &&
            payload.to_ms === latestEndMS &&
            payload.missing_point_count === 0
        );
      })
      .catch(() => {
        if (!controller.signal.aborted) setHistoryIsComplete(false);
      });
    return () => controller.abort();
  }, [historyFromMS, historyRefreshVersion, latestEndMS]);

  useEffect(() => {
    if (historyIsComplete || typeof window === "undefined") return;
    const refreshHistory = () => {
      if (document.visibilityState === "hidden") return;
      setHistoryRefreshVersion(current => current + 1);
    };
    const timer = window.setInterval(
      refreshHistory,
      INCOMPLETE_OFFICIAL_CONTRIBUTION_RETRY_MS
    );
    window.addEventListener("focus", refreshHistory);
    window.addEventListener("pageshow", refreshHistory);
    document.addEventListener("visibilitychange", refreshHistory);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("focus", refreshHistory);
      window.removeEventListener("pageshow", refreshHistory);
      document.removeEventListener("visibilitychange", refreshHistory);
    };
  }, [historyIsComplete]);

  const contributionIsComplete = officialAlignedContributionIsComplete(
    contribution,
    selectedEndMS
  );

  useEffect(() => {
    if (
      selectedEndMS <= 0 ||
      contributionIsComplete ||
      typeof window === "undefined"
    ) {
      return;
    }
    const refreshIncompleteContribution = () => {
      if (document.visibilityState === "hidden") return;
      setContributionRefreshVersion(current => current + 1);
    };
    const timer = window.setInterval(
      refreshIncompleteContribution,
      INCOMPLETE_OFFICIAL_CONTRIBUTION_RETRY_MS
    );
    window.addEventListener("focus", refreshIncompleteContribution);
    window.addEventListener("pageshow", refreshIncompleteContribution);
    document.addEventListener(
      "visibilitychange",
      refreshIncompleteContribution
    );
    return () => {
      window.clearInterval(timer);
      window.removeEventListener("focus", refreshIncompleteContribution);
      window.removeEventListener("pageshow", refreshIncompleteContribution);
      document.removeEventListener(
        "visibilitychange",
        refreshIncompleteContribution
      );
    };
  }, [contributionIsComplete, selectedEndMS]);

  useEffect(() => {
    const next = resolveOfficialHourSelection(
      selectedEndMS,
      availableHourMS,
      latestEndMS,
      previousLatestEndRef.current
    );
    previousLatestEndRef.current = latestEndMS;
    if (selectedEndMS !== next) setSelectedEndMS(next);
  }, [availableHourMS, latestEndMS, selectedEndMS]);

  useEffect(() => {
    if (selectedEndMS <= 0 || typeof window === "undefined") return;
    try {
      window.sessionStorage.setItem(
        OFFICIAL_HOUR_SELECTION_KEY,
        String(selectedEndMS)
      );
    } catch {
      // Storage can be disabled; the in-memory selection still works.
    }
  }, [selectedEndMS]);

  useEffect(() => {
    if (latestEndMS <= 0) return;
    const retained = retainedOfficialContributionByHour.get(latestEndMS);
    if (
      !retainedOfficialContributionFullHours.has(latestEndMS) &&
      (!retained ||
        retained.asset_drivers.length <=
          latestContribution.asset_drivers.length)
    ) {
      retainedOfficialContributionByHour.set(latestEndMS, latestContribution);
    }
  }, [latestContribution, latestEndMS]);

  useEffect(() => {
    if (selectedEndMS <= 0) return;
    const retained = retainedOfficialContributionByHour.get(selectedEndMS);
    if (retained) {
      setContribution(retained);
    } else if (selectedEndMS === latestEndMS) {
      setContribution(latestContribution);
    }
    if (
      retainedOfficialContributionFullHours.has(selectedEndMS) &&
      retained &&
      officialAlignedContributionIsComplete(retained, selectedEndMS)
    ) {
      rememberOfficialContribution(selectedEndMS, retained);
      setLoading(false);
      setLoadError("");
      return;
    }
    retainedOfficialContributionFullHours.delete(selectedEndMS);
    const controller = new AbortController();
    setLoading(true);
    setLoadError("");
    void getOfficialAlignedContribution(selectedEndMS, controller.signal)
      .then(next => {
        if (controller.signal.aborted) return;
        retainedOfficialContributionByHour.set(selectedEndMS, next);
        if (officialAlignedContributionIsComplete(next, selectedEndMS)) {
          retainedOfficialContributionFullHours.add(selectedEndMS);
          rememberOfficialContribution(selectedEndMS, next);
        } else {
          retainedOfficialContributionFullHours.delete(selectedEndMS);
        }
        setContribution(next);
        setLoadError("");
      })
      .catch(error => {
        if (controller.signal.aborted) return;
        setLoadError(
          error instanceof Error
            ? error.message
            : "선택한 시간의 기여 분석을 불러오지 못했습니다."
        );
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [
    contributionRefreshVersion,
    latestContribution,
    latestEndMS,
    selectedEndMS,
  ]);

  const selectedHour = selectedOfficialHourState(contribution, selectedEndMS);
  const ready = selectedHour.comparisonReady;
  const displayedEndMS = contribution.current?.bucket_ms ?? 0;
  // The streamed trend contains only the largest 30 drivers. Whole-list
  // counts, signed sums and top-five concentration require the full detail
  // response for the hour actually being displayed.
  const fullDriverListReady =
    retainedOfficialContributionFullHours.has(displayedEndMS) &&
    officialAlignedContributionIsComplete(contribution, displayedEndMS);
  const contributionInsight = buildOfficialContributionInsight(
    contribution.asset_drivers,
    contribution.share_delta_pp,
    ready && fullDriverListReady
  );
  const upRows = contributionInsight.available
    ? officialContributionRows(contribution.asset_drivers, "up")
    : [];
  const downRows = contributionInsight.available
    ? officialContributionRows(contribution.asset_drivers, "down")
    : [];
  const selectedHourTimestamp = preciseHourlyMetrics
    ? formatOfficialHourTimestamp
    : timestamp;
  const statusText =
    trend.status === "LIVE"
      ? "시간별 공식 비교 정상"
      : trend.status === "GAP"
        ? "일부 시간 구간 누락"
        : trend.status === "WARMING"
          ? "시간별 기준 축적 중"
          : "시간별 공식 자료 대기";

  return (
    <Panel
      title={
        <span className="inline-help-heading">
          시간별 점유율과 기여 코인 지도
          <InlineHelp text="매시간 24시간 범위에 새로 들어오고 빠진 거래가 어떤 코인을 통해 빗썸 점유율을 올리거나 내렸는지 보여줍니다." />
        </span>
      }
      icon={<GitBranch className="h-4 w-4" />}
      className="official-stocks-trend official-hourly-attribution"
      actions={
        <StatusBadge
          label={statusText}
          tone={trend.status === "LIVE" ? "live" : "warn"}
        />
      }
    >
      <div className="official-hourly-contract-note">
        <span>같은 종료 시각의 공식 자료 비교</span>
        <strong>업비트 · 빗썸 · 코인원</strong>
        <p>
          코빗·고팍스는 시간별 원화 거래대금을 공개하지 않아 이 분석은 3곳
          기준입니다.
        </p>
      </div>

      <div className="official-trend-content">
        <DeferredRender
          fallback={
            <div className="official-stable-placeholder" aria-hidden="true" />
          }
        >
          <Suspense
            fallback={
              <div className="official-stable-placeholder" aria-hidden="true" />
            }
          >
            <LazyOfficialTrendCharts
              trend={chartTrend}
              selectedEndMS={selectedEndMS}
              selectedShareDeltaPP={selectedHour.shareDeltaPP}
              onSelectEndMS={setSelectedEndMS}
            />
          </Suspense>
        </DeferredRender>

        <section
          className={`official-selected-hour-story${loading ? " is-refreshing" : ""}`}
          aria-busy={loading}
        >
          <div className="official-selected-hour-heading">
            <div>
              <p className="official-summary-eyebrow">선택한 시간</p>
              <h3>
                {ready
                  ? `${selectedHourTimestamp(contribution.previous?.observed_at_ms)} → ${selectedHourTimestamp(contribution.current?.observed_at_ms)}`
                  : "연속된 공식 시간 자료를 확인하고 있습니다"}
              </h3>
            </div>
            <span>
              {loading
                ? "선택 시간 분석 확인 중"
                : displayedEndMS === selectedEndMS
                  ? "선택 시간 분석"
                  : "마지막 정상 결과"}
            </span>
          </div>
          <p className="sr-only" aria-live="polite" aria-atomic="true">
            {ready
              ? preciseHourlyMetrics
                ? `선택 시간 ${selectedHourTimestamp(contribution.current?.observed_at_ms)}, 빗썸 점유율 ${formatOfficialHourShare(contribution.current?.bithumb_share_pct)}, 한 시간 전 대비 변화 ${formatOfficialHourContribution(contribution.share_delta_pp)}`
                : `선택 시간 ${timestamp(contribution.current?.observed_at_ms)}, 점유율 ${percent(contribution.current?.bithumb_share_pct)}, 1시간 동안 오른·내린 폭 ${points(contribution.share_delta_pp)}`
              : "선택 시간의 연속된 공식 자료를 확인하고 있습니다."}
          </p>
          {!ready ? (
            <p role="status">
              {selectedHourTimestamp(selectedEndMS)}의 점유율 변화와 기여 코인을
              확인하고 있습니다.
            </p>
          ) : preciseHourlyMetrics ? (
            <OfficialSelectedHourMetrics contribution={contribution} />
          ) : (
            <>
              <div className="official-selected-hour-metrics">
                <div className="official-hour-answer">
                  <span>1시간 동안 오른·내린 폭</span>
                  <strong className={changeTone(contribution.share_delta_pp)}>
                    <AnimatedMetricValue
                      key={`share-delta-${displayedEndMS}`}
                      value={contribution.share_delta_pp}
                      digits={3}
                      format={value => points(value)}
                      width="compact"
                    />
                  </strong>
                </div>
                <div>
                  <span>점유율</span>
                  <strong className="official-metric-pair">
                    <AnimatedMetricValue
                      key={`previous-share-${displayedEndMS}`}
                      value={contribution.previous?.bithumb_share_pct}
                      preserveRawValue
                      format={value => percent(value)}
                      width="compact"
                    />
                    <span aria-hidden="true">→</span>
                    <span className="sr-only">에서</span>
                    <AnimatedMetricValue
                      key={`current-share-${displayedEndMS}`}
                      value={contribution.current?.bithumb_share_pct}
                      preserveRawValue
                      format={value => percent(value)}
                      width="compact"
                    />
                  </strong>
                </div>
                <div>
                  <span>국내 3곳 거래금액 변화</span>
                  <strong>
                    <AnimatedMetricValue
                      key={`domestic-delta-${displayedEndMS}`}
                      value={contribution.domestic_volume_delta_krw}
                      digits={0}
                      format={signedMoney}
                      width="wide"
                    />
                  </strong>
                </div>
                <div>
                  <span>빗썸 거래금액 변화</span>
                  <strong>
                    <AnimatedMetricValue
                      key={`bithumb-delta-${displayedEndMS}`}
                      value={contribution.bithumb_volume_delta_krw}
                      digits={0}
                      format={signedMoney}
                      width="wide"
                    />
                  </strong>
                </div>
              </div>
              <div className="official-selected-hour-exchanges">
                {contribution.exchange_drivers.map(row => (
                  <div key={row.exchange}>
                    <span>{exchangeLabel(row.exchange)}</span>
                    <strong className={changeTone(row.contribution_pp)}>
                      <AnimatedMetricValue
                        key={`${displayedEndMS}-${row.exchange}`}
                        value={row.contribution_pp}
                        digits={3}
                        format={value => points(value)}
                        width="compact"
                      />
                    </strong>
                    <small>
                      {signedMoney(row.volume_change_krw)} ·{" "}
                      {row.leading_asset || "대표 코인 없음"}
                    </small>
                  </div>
                ))}
              </div>
            </>
          )}
          {loadError && (
            <p className="official-hour-load-error">
              {selectedHour.matchesSelection
                ? "선택 시간의 추가 자료 확인이 지연되고 있습니다."
                : "선택 시간의 분석 자료를 불러오지 못했습니다. 잠시 뒤 다시 확인합니다."}
            </p>
          )}
        </section>

        <section
          className="official-contribution-insight"
          aria-label="선택 시간 기여 구조 요약"
        >
          <div className="official-contribution-insight-heading">
            <div>
              <h3>전체 코인 요약</h3>
            </div>
            <span>
              {contributionInsight.available
                ? "같은 시각의 3개 거래소 · 전체 코인 계산"
                : "코인별 계산 자료 확인 중"}
            </span>
          </div>
          <div className="official-contribution-insight-grid">
            <article>
              <span className="inline-help-heading">
                오른·내린 코인 수
                <InlineHelp
                  text={
                    contributionInsight.available
                      ? `전체 ${contribution.asset_drivers.length.toLocaleString("ko-KR")}개 코인 중 점유율을 올린 코인과 내린 코인 수`
                      : "양 끝 시각의 코인별 자료가 모두 있어야 계산합니다. 거래소 합계가 있어도 코인별 자료는 부족할 수 있습니다."
                  }
                />
              </span>
              <strong className="official-metric-pair">
                <AnimatedMetricValue
                  key={`positive-count-${displayedEndMS}`}
                  value={contributionInsight.positiveCount}
                  digits={0}
                  format={value =>
                    `+${Math.round(value).toLocaleString("ko-KR")}`
                  }
                  width="auto"
                />
                <span aria-hidden="true">/</span>
                <span className="sr-only">대</span>
                <AnimatedMetricValue
                  key={`negative-count-${displayedEndMS}`}
                  value={contributionInsight.negativeCount}
                  digits={0}
                  format={value =>
                    `-${Math.round(value).toLocaleString("ko-KR")}`
                  }
                  width="auto"
                />
              </strong>
            </article>
            <article>
              <span>오른 값·내린 값 합계</span>
              <strong className="official-metric-pair">
                <AnimatedMetricValue
                  key={`positive-gross-${displayedEndMS}`}
                  value={contributionInsight.positiveGrossPP}
                  digits={3}
                  preserveRawValue
                  format={value => points(value)}
                  width="auto"
                />
                <span aria-hidden="true">/</span>
                <span className="sr-only">대</span>
                <AnimatedMetricValue
                  key={`negative-gross-${displayedEndMS}`}
                  value={
                    contributionInsight.negativeGrossPP === null
                      ? null
                      : -contributionInsight.negativeGrossPP
                  }
                  digits={3}
                  preserveRawValue
                  format={value => points(value)}
                  width="auto"
                />
              </strong>
              <small>
                각 코인이 점유율을 올리거나 내린 값을 방향별로 각각 더한 결과
              </small>
            </article>
            <article>
              <span className="inline-help-heading">
                상위 5개 코인의 영향
                <InlineHelp text="점유율을 올린 쪽과 내린 쪽에서 영향이 큰 5개 코인이 차지한 비중" />
              </span>
              <strong className="official-metric-pair official-metric-pair-wrap">
                <span>상승</span>
                <AnimatedMetricValue
                  key={`positive-top-five-${displayedEndMS}`}
                  value={contributionInsight.positiveTopFiveSharePct}
                  digits={0}
                  preserveRawValue
                  format={value => percent(value, 0)}
                  width="auto"
                />
                <span aria-hidden="true">·</span>
                <span className="sr-only">그리고</span>
                <span>하락</span>
                <AnimatedMetricValue
                  key={`negative-top-five-${displayedEndMS}`}
                  value={contributionInsight.negativeTopFiveSharePct}
                  digits={0}
                  preserveRawValue
                  format={value => percent(value, 0)}
                  width="auto"
                />
              </strong>
            </article>
            <article>
              <span className="inline-help-heading">
                상쇄율
                <InlineHelp
                  text={`올린 값과 내린 값이 서로 상쇄된 비율 · 최종 변화 ${points(selectedHour.shareDeltaPP)}`}
                />
              </span>
              <strong>
                <AnimatedMetricValue
                  key={`cancellation-${displayedEndMS}`}
                  value={contributionInsight.cancellationPct}
                  digits={0}
                  preserveRawValue
                  format={value => percent(value, 0)}
                  width="compact"
                />
              </strong>
            </article>
          </div>
        </section>

        <div
          className={`official-contribution-landscape${loading ? " is-refreshing" : ""}`}
        >
          <OfficialContributionRankList
            title="점유율 상승 기여 코인 TOP 20"
            description="빗썸 쪽 거래 증가와 경쟁 거래소 쪽 감소를 함께 계산한 양(+)의 기여입니다."
            direction="up"
            rows={upRows}
            animationKey={displayedEndMS}
            available={contributionInsight.available}
          />
          <OfficialContributionRankList
            title="점유율 하락 기여 코인 TOP 20"
            description="빗썸 쪽 거래 감소와 경쟁 거래소 쪽 증가를 함께 계산한 음(-)의 기여입니다."
            direction="down"
            rows={downRows}
            animationKey={displayedEndMS}
            available={contributionInsight.available}
          />
        </div>
        {!ready && !loading && (
          <p className="official-hourly-data-note">
            이 시간의 양 끝 공식 자료가 모두 저장되면 상승·하락 기여 코인을
            계산합니다. 값이 없는 상태를 임의로 0으로 채우지 않습니다.
          </p>
        )}
      </div>
    </Panel>
  );
}

function LegacyOfficialTickerTrendPanel({
  trend,
}: {
  trend: MarketShareOfficialTicker1mState;
}) {
  const [detail, setDetail] = useState<"assets" | "exchanges" | "expiration">(
    "assets"
  );
  const ready = Boolean(
    trend.status === "LIVE" && trend.previous && trend.current
  );
  const assetReady = trend.data_quality.asset_details_ready === true;
  const estimate = trend.expiration_estimate;
  const estimateReady = estimate.status === "INFERRED";
  const leadingAssetDriver = trend.drivers[0] ?? null;
  const leadingExchangeDriver = trend.exchange_drivers[0] ?? null;
  const statusText =
    trend.status === "LIVE"
      ? "1시간 비교 가능"
      : trend.status === "GAP"
        ? "1시간 구간 누락"
        : trend.status === "WARMING"
          ? "1시간 기준 축적 중"
          : "공식 자료 대기";
  const intervalLabel = ready
    ? `${timestamp(trend.previous?.observed_at_ms)} → ${timestamp(trend.current?.observed_at_ms)}`
    : "연속된 공식 자료 2개가 필요합니다.";
  const tabs = [
    { id: "assets" as const, label: "코인별 점유율 기여" },
    { id: "exchanges" as const, label: "거래소별 점유율 기여" },
    { id: "expiration" as const, label: "24시간 제외 추정" },
  ];
  return (
    <Panel
      title="공식 24시간 점유율과 최근 1시간 변화"
      description="업비트·빗썸·코인원의 UTC 종료 시각이 같은 1시간봉 24개 합계를 비교합니다. 정확한 원화 거래금액을 제공하지 않는 코빗·고팍스는 이 비교에서 제외합니다."
      icon={<GitBranch className="h-4 w-4" />}
      className="official-stocks-trend"
      actions={
        <StatusBadge
          label={statusText}
          tone={trend.status === "LIVE" ? "live" : "warn"}
        />
      }
    >
      <div className="metric-grid official-trend-metrics border-b border-border sm:grid-cols-2 xl:grid-cols-4">
        <KpiCard
          label="공식 24시간 점유율"
          icon={<BarChart3 className="h-4 w-4" />}
          value={
            ready ? (
              <span className="text-lg">
                {percent(trend.previous?.bithumb_share_pct)} →{" "}
                {percent(trend.current?.bithumb_share_pct)}
              </span>
            ) : (
              "—"
            )
          }
          tone="neutral"
          sub={intervalLabel}
        />
        <KpiCard
          label="1시간 점유율 변화"
          icon={
            finite(trend.share_delta_pp) && trend.share_delta_pp >= 0 ? (
              <ArrowUpRight className="h-4 w-4" />
            ) : (
              <ArrowDownRight className="h-4 w-4" />
            )
          }
          value={points(ready ? trend.share_delta_pp : null)}
          tone={
            finite(trend.share_delta_pp)
              ? trend.share_delta_pp >= 0
                ? "profit"
                : "loss"
              : "muted"
          }
          sub="두 공식 수치의 차이"
        />
        <KpiCard
          label="국내 24시간 거래금액 변화"
          icon={<Activity className="h-4 w-4" />}
          value={signedMoney(ready ? trend.domestic_volume_delta_krw : null)}
          tone="neutral"
          sub="최근 유입 - 24시간 범위 제외"
        />
        <KpiCard
          label="빗썸 24시간 거래금액 변화"
          icon={<TrendingUp className="h-4 w-4" />}
          value={signedMoney(ready ? trend.bithumb_volume_delta_krw : null)}
          tone="neutral"
          sub="빗썸 공식 자료 기준"
        />
      </div>

      <div className="official-trend-content">
        <DeferredRender
          fallback={
            <div className="official-trend-primary">
              <div
                className="official-trend-chart-card min-h-80"
                aria-hidden="true"
              />
            </div>
          }
        >
          <Suspense
            fallback={
              <div className="official-trend-primary">
                <div
                  className="official-trend-chart-card min-h-80"
                  aria-hidden="true"
                />
              </div>
            }
          >
            <LazyOfficialTrendCharts
              trend={trend}
              selectedEndMS={trend.current?.bucket_ms ?? 0}
              selectedShareDeltaPP={ready ? trend.share_delta_pp : null}
              onSelectEndMS={() => undefined}
            />
          </Suspense>
        </DeferredRender>
        <div className="official-trend-detail">
          <div className="official-trend-detail-table">
            <div className="official-trend-tabs flex flex-wrap gap-2 border-b border-border px-4 py-3">
              {tabs.map(tab => (
                <button
                  key={tab.id}
                  type="button"
                  aria-pressed={detail === tab.id}
                  onClick={() => setDetail(tab.id)}
                  className={
                    "border px-3 py-1.5 text-xs transition-colors " +
                    (detail === tab.id
                      ? "border-primary/45 bg-primary/10 text-primary"
                      : "border-border text-muted-foreground hover:bg-secondary hover:text-foreground")
                  }
                >
                  {tab.label}
                </button>
              ))}
            </div>

            <p className="border-b border-border bg-inset px-4 py-3 text-xs leading-5 text-muted-foreground">
              {detail === "assets"
                ? `공식 24시간 점유율의 최근 1시간 변화 ${points(trend.share_delta_pp)}에서 계산상 기여 절댓값이 큰 코인부터 보여줍니다.`
                : detail === "exchanges"
                  ? `공식 24시간 점유율의 최근 1시간 변화 ${points(trend.share_delta_pp)}를 거래소별 계산 기여로 나눈 값입니다.`
                  : "점유율을 다시 계산하는 값이 아닙니다. 최근 1시간 동안 정렬 24시간 범위에서 빠진 금액을 동일 구간 실시간 체결로 역산한 보조 설명입니다."}
            </p>

            {detail === "assets" && (
              <TickerAssetContributionTable trend={trend} ready={assetReady} />
            )}
            {detail === "exchanges" && (
              <TickerExchangeContributionTable trend={trend} />
            )}
            {detail === "expiration" && (
              <TickerExpirationEstimate trend={trend} ready={estimateReady} />
            )}
          </div>

          <aside className="official-trend-inspector" aria-live="polite">
            <p className="official-trend-mini-eyebrow">선택한 분석</p>
            {detail === "assets" && (
              <>
                <h4>{leadingAssetDriver?.asset ?? "코인 자료 축적 중"}</h4>
                <p className="official-trend-inspector-copy">
                  같은 종료 시각의 1시간봉을 비교한 코인별 계산 기여입니다.
                </p>
                <dl>
                  <div>
                    <dt>점유율 기여</dt>
                    <dd
                      className={changeTone(
                        leadingAssetDriver?.contribution_pp
                      )}
                    >
                      {points(leadingAssetDriver?.contribution_pp)}
                    </dd>
                  </div>
                  <div>
                    <dt>빗썸 변화</dt>
                    <dd>
                      {signedMoney(
                        leadingAssetDriver?.bithumb_volume_change_krw
                      )}
                    </dd>
                  </div>
                  <div>
                    <dt>경쟁 변화</dt>
                    <dd>
                      {signedMoney(
                        leadingAssetDriver?.competitor_volume_change_krw
                      )}
                    </dd>
                  </div>
                </dl>
              </>
            )}
            {detail === "exchanges" && (
              <>
                <h4>
                  {leadingExchangeDriver
                    ? exchangeLabel(leadingExchangeDriver.exchange)
                    : "거래소 자료 축적 중"}
                </h4>
                <p className="official-trend-inspector-copy">
                  같은 종료 시각의 1시간봉을 비교한 거래소별 계산 기여입니다.
                </p>
                <dl>
                  <div>
                    <dt>점유율 기여</dt>
                    <dd
                      className={changeTone(
                        leadingExchangeDriver?.contribution_pp
                      )}
                    >
                      {points(leadingExchangeDriver?.contribution_pp)}
                    </dd>
                  </div>
                  <div>
                    <dt>24시간 변화</dt>
                    <dd>
                      {signedMoney(leadingExchangeDriver?.volume_change_krw)}
                    </dd>
                  </div>
                  <div>
                    <dt>대표 코인</dt>
                    <dd>{leadingExchangeDriver?.leading_asset ?? "—"}</dd>
                  </div>
                </dl>
              </>
            )}
            {detail === "expiration" && (
              <>
                <h4>
                  {estimateReady
                    ? "24시간 제외 금액 추정 가능"
                    : "24시간 제외 금액 추정 대기"}
                </h4>
                <p className="official-trend-inspector-copy">
                  같은 시각의 체결 구간이 채워졌을 때만 공식 거래금액 변화에서
                  24시간 범위를 벗어난 거래금액을 역산합니다.
                </p>
                <dl>
                  <div>
                    <dt>관측 범위</dt>
                    <dd>
                      {formatObservationSeconds(
                        estimate.coverage_sec,
                        estimate.required_sec
                      )}
                    </dd>
                  </div>
                  <div>
                    <dt>빗썸 제외 추정</dt>
                    <dd>
                      {money(estimate.totals.estimated_expiring_bithumb_krw)}
                    </dd>
                  </div>
                  <div>
                    <dt>제외 추정 금액 중 빗썸 비중</dt>
                    <dd>
                      {percent(estimate.totals.estimated_expiring_share_pct)}
                    </dd>
                  </div>
                </dl>
              </>
            )}
            <p className="official-trend-inspector-note">
              {detail === "expiration"
                ? "공식 점유율 계산에는 사용하지 않는 보조 추정값입니다."
                : "값과 표시 순서는 같은 종료 시각의 1시간봉 비교 결과를 그대로 사용합니다."}
            </p>
          </aside>
        </div>
      </div>
    </Panel>
  );
}

function TickerAssetContributionTable({
  trend,
  ready,
}: {
  trend: MarketShareOfficialTicker1mState;
  ready: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const rows = trend.drivers.slice(0, 12);
  const visibleRows = expanded ? rows : rows.slice(0, 6);
  if (!ready || trend.drivers.length === 0) {
    return (
      <div className="flex min-h-64 items-center justify-center px-6 text-center text-xs leading-6 text-muted-foreground">
        기존 이력에는 코인별 원본이 없어 점유율 추이만 표시합니다. 신규 1시간
        정렬 자료 2개가 쌓이면 어떤 코인이 24시간 점유율 변화에 기여했는지
        표시됩니다.
      </div>
    );
  }
  return (
    <div>
      <div className="official-trend-table-wrap">
        <table
          className="data-table official-trend-table"
          data-table-schema="label-numeric"
        >
          <colgroup>
            <col className="official-trend-col-asset" />
            <col />
            <col />
            <col />
          </colgroup>
          <thead>
            <tr>
              <th>코인</th>
              <th className="text-right">점유율 변화 기여</th>
              <th className="text-right">빗썸 변화</th>
              <th className="text-right">경쟁 변화</th>
            </tr>
          </thead>
          <tbody>
            {visibleRows.map(row => (
              <tr key={row.asset}>
                <td className="font-semibold">{row.asset}</td>
                <td className={`text-right ${changeTone(row.contribution_pp)}`}>
                  {points(row.contribution_pp)}
                </td>
                <td className="text-right">
                  {signedMoney(row.bithumb_volume_change_krw)}
                </td>
                <td className="text-right">
                  {signedMoney(row.competitor_volume_change_krw)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {rows.length > 6 && (
        <div className="official-trend-table-more-row">
          <button type="button" onClick={() => setExpanded(value => !value)}>
            {expanded ? "접기" : `${rows.length - 6}개 더 보기`}
          </button>
        </div>
      )}
    </div>
  );
}

function TickerExchangeContributionTable({
  trend,
}: {
  trend: MarketShareOfficialTicker1mState;
}) {
  if (trend.exchange_drivers.length === 0) {
    return (
      <div className="flex min-h-64 items-center justify-center px-6 text-center text-xs text-muted-foreground">
        비교 가능한 거래소별 공식 자료를 기다리고 있습니다.
      </div>
    );
  }
  return (
    <div className="official-trend-table-wrap">
      <table
        className="data-table official-trend-table official-trend-exchange-table"
        data-table-schema="label-numeric-label"
      >
        <colgroup>
          <col />
          <col />
          <col />
          <col className="official-trend-col-leading" />
        </colgroup>
        <thead>
          <tr>
            <th>거래소</th>
            <th className="text-right">점유율 변화 기여</th>
            <th className="text-right">24시간 변화</th>
            <th>대표 코인</th>
          </tr>
        </thead>
        <tbody>
          {trend.exchange_drivers.map(row => (
            <tr key={row.exchange}>
              <td className="font-semibold">{exchangeLabel(row.exchange)}</td>
              <td className={`text-right ${changeTone(row.contribution_pp)}`}>
                {points(row.contribution_pp)}
              </td>
              <td className="text-right">
                {signedMoney(row.volume_change_krw)}
              </td>
              <td>
                {row.leading_asset
                  ? `${row.leading_asset} · ${points(row.leading_asset_contribution_pp)}`
                  : "코인 원본 축적 전"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function TickerExpirationEstimate({
  trend,
  ready,
}: {
  trend: MarketShareOfficialTicker1mState;
  ready: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const estimate = trend.expiration_estimate;
  const rows = estimate.assets.slice(0, 12);
  const visibleRows = expanded ? rows : rows.slice(0, 6);
  if (!ready) {
    return (
      <div className="min-h-64 p-5 text-xs leading-6 text-muted-foreground">
        <p className="font-semibold text-gold">
          24시간 제외 추정은 아직 표시하지 않습니다.
        </p>
        <p className="mt-2">
          공식 자료 두 시점과 정확히 같은 실시간 체결 구간이 완전히 채워져야
          역산합니다. 현재 관측{" "}
          {formatObservationSeconds(
            estimate.coverage_sec,
            estimate.required_sec
          )}{" "}
          · {statusLabel(estimate.coverage_status)}
        </p>
        <p className="mt-2">
          공식 24시간 거래금액 변화 = 최근 유입 - 24시간 범위 제외 금액 식을
          사용하며, 제외 금액은 직접 관측값이 아니라 추정값입니다.
        </p>
      </div>
    );
  }
  const totals = estimate.totals;
  return (
    <div>
      <div className="grid gap-px border-b border-border bg-border sm:grid-cols-3">
        <div className="bg-card p-3">
          <p className="text-base text-muted-foreground">최근 유입 · 빗썸</p>
          <p className="mt-1 font-semibold">
            {money(totals.incoming_bithumb_volume_krw)}
          </p>
        </div>
        <div className="bg-card p-3">
          <p className="text-base text-muted-foreground">
            24시간 범위 제외 추정 · 빗썸
          </p>
          <p className="mt-1 font-semibold">
            {money(totals.estimated_expiring_bithumb_krw)}
          </p>
        </div>
        <div className="bg-card p-3">
          <p className="text-base text-muted-foreground">
            제외 추정 금액 중 빗썸 비중
          </p>
          <p className="mt-1 font-semibold">
            {percent(totals.estimated_expiring_share_pct)}
          </p>
        </div>
      </div>
      <div className="official-trend-table-wrap">
        <table
          className="data-table official-trend-table official-trend-expiration-table"
          data-table-schema="label-numeric"
        >
          <colgroup>
            <col className="official-trend-col-asset" />
            <col />
            <col />
            <col />
          </colgroup>
          <thead>
            <tr>
              <th>코인</th>
              <th className="text-right">최근 유입</th>
              <th className="text-right">빗썸 제외 추정</th>
              <th className="text-right">경쟁 제외 추정</th>
            </tr>
          </thead>
          <tbody>
            {visibleRows.map(row => (
              <tr key={row.asset}>
                <td>
                  <span className="font-semibold">{row.asset}</span>
                  <span
                    className={
                      "official-trend-row-status " +
                      (row.status === "INFERRED" ? "text-gold" : "text-loss")
                    }
                  >
                    {row.status === "INFERRED"
                      ? "계산 가능"
                      : "자료 시각 불일치"}
                  </span>
                </td>
                <td className="text-right">
                  {money(row.incoming_bithumb_volume_krw)}
                </td>
                <td className="text-right">
                  {money(row.estimated_expiring_bithumb_krw)}
                </td>
                <td className="text-right">
                  {money(row.estimated_expiring_competitor_krw)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {rows.length > 6 && (
        <div className="official-trend-table-more-row">
          <button type="button" onClick={() => setExpanded(value => !value)}>
            {expanded ? "접기" : `${rows.length - 6}개 더 보기`}
          </button>
        </div>
      )}
      <p className="border-t border-border px-4 py-3 text-base leading-6 text-muted-foreground">
        공식 24시간 거래금액 변화에서 같은 구간의 최근 유입을 대조해, 24시간
        범위에서 제외된 거래금액을 추정합니다.
      </p>
    </div>
  );
}

function OfficialMovementSummary({
  monitor,
  fallbackOfficial,
  trend,
  tickStore,
}: {
  monitor: MarketShareOfficialMovementState;
  fallbackOfficial: MarketShareOfficialDomestic24h;
  trend: MarketShareOfficialTicker1mState | null;
  tickStore: MarketShareTickStore;
}) {
  const currentTick = useSyncExternalStore(
    tickStore.subscribe,
    tickStore.getSnapshot,
    tickStore.getSnapshot
  );
  const retainedOfficialRef = useRef(fallbackOfficial);
  const retainedWithFallback = selectStableOfficialDomestic(
    fallbackOfficial,
    retainedOfficialRef.current
  );
  const official = selectStableOfficialDomestic(
    currentTick?.official_domestic_24h,
    retainedWithFallback
  );
  if (officialDomesticSnapshotIsComplete(official)) {
    retainedOfficialRef.current = official;
  }

  return (
    <OfficialMovementPanel
      monitor={monitor}
      official={official}
      trend={trend}
    />
  );
}

function RealtimeObservationPanel({
  tickStore,
  connection,
  analysisReady,
  rollingCoverage,
}: {
  tickStore: MarketShareTickStore;
  connection: MarketShareConnection;
  analysisReady: boolean;
  rollingCoverage?: MarketShareRolling24h["rolling_24h"]["coverage"];
}) {
  const currentTick = useSyncExternalStore(
    tickStore.subscribe,
    tickStore.getSnapshot,
    tickStore.getSnapshot
  );
  const readiness = currentTick?.readiness;
  const retainedExchangesRef = useRef<StableExchangeObservation[]>([]);
  const currentExchangeTransportIsLive = realtimeFlowTransportIsLive(
    currentTick,
    connection
  );
  const currentExchangeFrameIsComplete =
    currentExchangeTransportIsLive &&
    domesticExchangeFrameIsComplete(
      currentTick?.exchanges,
      readiness?.ready_exchanges
    );
  const observedExchanges = selectStableExchangeFrame(
    currentTick?.exchanges,
    retainedExchangesRef.current,
    readiness?.ready_exchanges,
    currentExchangeTransportIsLive
  );
  if (currentExchangeFrameIsComplete) {
    retainedExchangesRef.current = observedExchanges;
  }
  const coverageSeconds = currentExchangeFrameIsComplete
    ? currentTick?.coverage_sec
    : (rollingCoverage?.coverage_sec ?? currentTick?.coverage_sec);
  const requiredSeconds = currentExchangeFrameIsComplete
    ? currentTick?.window_sec
    : (rollingCoverage?.required_sec ?? currentTick?.window_sec);
  const gapSeconds = currentExchangeFrameIsComplete
    ? currentTick?.gap_seconds
    : (rollingCoverage?.gap_seconds ?? currentTick?.gap_seconds);
  const domesticReadyExchanges = readiness
    ? readiness.ready_exchanges.filter(exchange =>
        DOMESTIC_EXCHANGE_IDS.has(exchange)
      )
    : observedExchanges
        .map(exchange => exchange.exchange)
        .filter(exchange => DOMESTIC_EXCHANGE_IDS.has(exchange));
  const domesticReadyCount = new Set(domesticReadyExchanges).size;
  const tickLive =
    realtimeFlowTransportIsLive(currentTick, connection) &&
    domesticReadyCount === DOMESTIC_EXCHANGE_IDS.size;
  const effectiveConnection: MarketShareConnection = tickLive
    ? "live"
    : connection === "live"
      ? "stale"
      : connection;
  const [sort, setSort] = useState<SortState>({ key: null, direction: "desc" });
  const [selectedExchange, setSelectedExchange] = useState("bithumb");
  const sortedExchanges = useMemo(
    () =>
      sortRows(observedExchanges, sort, (row, key) => {
        switch (key) {
          case "exchange":
            return exchangeLabel(row.exchange);
          case "volume":
            return row.volume_krw;
          case "share":
            return row.share_pct;
          case "trade_count":
            return row.trade_count;
          case "trade_count_share":
            return row.trade_count_share_pct;
          default:
            return null;
        }
      }),
    [observedExchanges, sort]
  );
  useEffect(() => {
    setSelectedExchange(current =>
      sortedExchanges.some(row => row.exchange === current)
        ? current
        : (sortedExchanges[0]?.exchange ?? "")
    );
  }, [sortedExchanges]);
  const selectedExchangeRow =
    sortedExchanges.find(row => row.exchange === selectedExchange) ??
    sortedExchanges[0] ??
    null;
  return (
    <div className="space-y-6">
      <Panel
        title="실시간 체결 · 점유율 변화 세부 근거"
        descriptionRole="status"
        description={
          analysisReady
            ? undefined
            : "상단 공식 점유율과 별도로, 같은 기간의 코인별 실시간 체결 흐름을 수집 중입니다."
        }
        icon={<Radio className="h-4 w-4" />}
        className="official-exchange-table-panel"
        actions={
          <div className="official-exchange-header-actions">
            <StatusBadge
              label={
                tickLive
                  ? "체결 정상"
                  : effectiveConnection === "offline"
                    ? "체결 연결 끊김"
                    : effectiveConnection === "stale"
                      ? "체결 지연"
                      : "체결 관측 중"
              }
              tone={
                tickLive
                  ? "live"
                  : effectiveConnection === "offline"
                    ? "error"
                    : "warn"
              }
            />
          </div>
        }
      >
        <div
          className="official-exchange-observation flex items-center gap-2 border-b border-border px-4 py-3 text-xs leading-6"
          data-ready={analysisReady}
        >
          <AlertTriangle
            className="h-4 w-4 shrink-0 text-gold"
            aria-hidden="true"
          />
          <div>
            <p className="ui-caption official-exchange-observation-title font-semibold">
              {analysisReady && tickLive
                ? "실시간 체결을 관측하고 있습니다."
                : analysisReady
                  ? "마지막 정상 체결값을 유지하고 새 관측을 기다립니다."
                  : "24시간 분석을 계산하고 있습니다."}
            </p>
            <p className="ui-caption official-exchange-observation-copy mt-1 text-muted-foreground">
              {formatObservedWindowCoverage(
                coverageSeconds,
                requiredSeconds,
                gapSeconds
              )}
            </p>
          </div>
        </div>
        <div className="official-exchange-comparison">
          <div className="official-exchange-table-scroll">
            <table
              className="market-data-table official-exchange-table text-left text-xs"
              data-table-schema="label-numeric"
            >
              <colgroup>
                <col className="official-exchange-col-name" />
                <col className="official-exchange-col-volume" />
                <col />
                <col className="official-exchange-col-count" />
                <col />
              </colgroup>
              <thead>
                <tr>
                  <SortHeader
                    label="거래소"
                    sortKey="exchange"
                    sort={sort}
                    onSort={key => toggleSort(setSort, key)}
                  />
                  <SortHeader
                    label="거래금액(억 원)"
                    sortKey="volume"
                    sort={sort}
                    onSort={key => toggleSort(setSort, key)}
                    align="right"
                  />
                  <SortHeader
                    label="거래금액 점유율"
                    sortKey="share"
                    sort={sort}
                    onSort={key => toggleSort(setSort, key)}
                    align="right"
                  />
                  <SortHeader
                    label="체결 건수"
                    sortKey="trade_count"
                    sort={sort}
                    onSort={key => toggleSort(setSort, key)}
                    align="right"
                  />
                  <SortHeader
                    label="체결 건수 점유율"
                    sortKey="trade_count_share"
                    sort={sort}
                    onSort={key => toggleSort(setSort, key)}
                    align="right"
                  />
                </tr>
              </thead>
              <tbody>
                {sortedExchanges.map(row => (
                  <tr
                    key={row.exchange}
                    data-selected={
                      row.exchange === selectedExchangeRow?.exchange
                    }
                  >
                    <td className="font-semibold">
                      <button
                        type="button"
                        className="exchange-row-selector"
                        aria-pressed={
                          row.exchange === selectedExchangeRow?.exchange
                        }
                        onClick={() => setSelectedExchange(row.exchange)}
                      >
                        {exchangeLabel(row.exchange)}
                      </button>
                    </td>
                    <td className="text-right">
                      {moneyHundredMillion(row.volume_krw)}
                    </td>
                    <td className="text-right">{percent(row.share_pct)}</td>
                    <td className="text-right">
                      {integerLabel(row.trade_count)}
                    </td>
                    <td className="text-right">
                      {percent(row.trade_count_share_pct)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <aside
            className="official-exchange-inspector"
            aria-label="선택 거래소 요약"
          >
            <p className="official-summary-eyebrow">선택 거래소</p>
            <div className="official-exchange-inspector-title">
              <h4>
                {selectedExchangeRow
                  ? exchangeLabel(selectedExchangeRow.exchange)
                  : "거래소 대기"}
              </h4>
              <span>
                {selectedExchangeRow
                  ? currentExchangeFrameIsComplete
                    ? "최신 수신값"
                    : "마지막 수신값"
                  : "자료 대기"}
              </span>
            </div>
            <dl>
              <div>
                <dt>최근 거래금액</dt>
                <dd>
                  <AnimatedMetricValue
                    resetKey={selectedExchangeRow?.exchange ?? "none"}
                    value={selectedExchangeRow?.volume_krw}
                    digits={0}
                    format={moneyHundredMillion}
                    width="wide"
                  />
                </dd>
              </div>
              <div>
                <dt>거래금액 점유율</dt>
                <dd>
                  <AnimatedMetricValue
                    resetKey={selectedExchangeRow?.exchange ?? "none"}
                    value={selectedExchangeRow?.share_pct}
                    preserveRawValue
                    format={value => percent(value)}
                    width="compact"
                  />
                </dd>
              </div>
              <div>
                <dt>체결 건수</dt>
                <dd>
                  <AnimatedMetricValue
                    resetKey={selectedExchangeRow?.exchange ?? "none"}
                    value={selectedExchangeRow?.trade_count}
                    digits={0}
                    format={integerLabel}
                    width="wide"
                  />
                </dd>
              </div>
              <div>
                <dt>체결 건수 점유율</dt>
                <dd>
                  <AnimatedMetricValue
                    resetKey={selectedExchangeRow?.exchange ?? "none"}
                    value={selectedExchangeRow?.trade_count_share_pct}
                    preserveRawValue
                    format={value => percent(value)}
                    width="compact"
                  />
                </dd>
              </div>
            </dl>
            <p className="official-exchange-inspector-note">
              표의 거래소 이름을 선택하면 같은 실시간 체결 관측의 핵심 값만 모아
              보여줍니다.
            </p>
          </aside>
        </div>
      </Panel>
    </div>
  );
}

function RollingWarmup({
  connection,
  error,
}: {
  connection: MarketShareConnection;
  error: string;
}) {
  return (
    <div className="w-full space-y-6">
      {error && (
        <div className="flex items-start gap-3 border border-gold/30 bg-gold/10 p-3 text-xs text-gold">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          {error}
        </div>
      )}
      <div
        className="official-page-skeleton"
        aria-live="polite"
        aria-busy="true"
      >
        <section>
          <span>01 · 현재 수준</span>
          <strong>마지막 정상 공식값을 복원하고 있습니다</strong>
          <div aria-hidden="true" />
        </section>
        <section>
          <span>02 · 시간별 변화의 구성</span>
          <strong>시간별 점유율과 기여 코인 지도를 준비하고 있습니다</strong>
          <div aria-hidden="true" />
        </section>
        <section>
          <span>03 · 실시간 직접 관측</span>
          <strong>10초 점유율 변화 감시는 연결 상태를 유지합니다</strong>
          <div aria-hidden="true" />
        </section>
      </div>
      <p className="official-page-skeleton-note">
        마지막 정상 화면을 우선 복원하고 새 값은 준비되는 순서대로 교체합니다 ·{" "}
        {connection}
      </p>
    </div>
  );
}

export default function RollingShareSummary2({
  relatedNoticesOnly = false,
  preciseHourlyMetrics = false,
}: {
  relatedNoticesOnly?: boolean;
  preciseHourlyMetrics?: boolean;
}) {
  const { data, tickStore, realtimeFlowStore, connection, error } =
    // The 10-second panel does not draw the one-second share series.
    useMarketShare(
      MARKET_SHARE_WINDOW_SEC.ONE_DAY,
      null,
      "KRW",
      "summary2",
      "compact"
    );
  const monitor = data?.analysis.rolling_24h;
  if (!monitor) {
    return <RollingWarmup connection={connection} error={error} />;
  }
  return (
    <Summary2Page
      monitor={monitor}
      relatedNoticesOnly={relatedNoticesOnly}
      preciseHourlyMetrics={preciseHourlyMetrics}
      officialMonitor={data?.analysis.official_share_monitor ?? null}
      officialTrend={data?.analysis.official_ticker_1m ?? null}
      official={data.analysis.official_domestic_24h}
      reaction={data?.analysis.reaction_summary ?? null}
      tickStore={tickStore}
      realtimeFlowStore={realtimeFlowStore}
      connection={connection}
      error={error}
      fallbackRealtimeFlow={data.analysis.realtime_flow ?? null}
    />
  );
}
