import {
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  Clock3,
  Database,
  Radio,
  RefreshCw,
} from "lucide-react";

import { AnimatedMetricValue } from "@/components/common/AnimatedMetricValue";
import { InlineHelp } from "@/components/common/InlineHelp";
import type {
  MarketShareConnection,
  MarketShareTickStore,
} from "@/hooks/useMarketShare";
import { ToggleChip } from "@/components/common/SelectionControl";
import { useNearViewport } from "@/hooks/useNearViewport";
import {
  getMarketShareRealtimeHistory,
  getMarketShareRealtimeSummary,
} from "@/lib/api-client";
import { exchangeLabel } from "@/lib/exchanges";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import {
  formatKRW,
  formatKrwCompact,
  formatSharePercent,
  formatSignedPercentagePoints,
} from "@/lib/formatters";
import { formatSlippageRate } from "@/lib/market-numeric-presentation";
import { selectStableRealtimeFlowState } from "@/lib/market-share-display-stability";
import type { MarketShareRealtimeFlowStore } from "@/lib/market-share-realtime-flow-store";
import {
  compareOfficialHourToRealtimeDirection,
  previewRealtimeFlowEpisodes,
  REALTIME_FLOW_CONTRACT_VERSION,
  REALTIME_FLOW_HISTORY_MS,
  REALTIME_FLOW_PANEL_SECTION_ORDER,
  realtimeFlowOrderbookValuesAreVisible,
  realtimeFlowHistoryRange,
  realtimeFlowEpisodeDirectionTone,
  realtimeFlowEpisodeStatusLabel,
  selectRealtimeFlowEpisodes,
  selectRealtimeFlowMissingIntervals,
  realtimeFlowTenSecondAttribution,
  realtimeFlowTenSecondSample,
  realtimeFlowSignalHeadline,
  realtimeFlowTenSecondWindow,
  realtimeFlowTransportIsLive,
  type RealtimeFlowTenSecondEpisode,
} from "@/lib/market-share-realtime-flow";
import type {
  MarketShareRealtimeFlowDriver,
  MarketShareRealtimeFlowPeriodSummary,
  MarketShareRealtimeFlowState,
  MarketShareRealtimeFlowStatus,
  MarketShareRealtimeOrderbook,
} from "@/lib/market-share-types";

type HistoryStatusFilter = "ALL" | "WATCH" | "ALERT";
// The episode list covers the 30-minute history range, which follows the
// tick clock; the summary follows the flow clock. Ask for a few minutes more
// so a small gap between the two clocks cannot drop an episode.
const SUMMARY_EPISODE_MARGIN_MS = 5 * 60 * 1_000;
type MarketSignalStatus = "NORMAL" | "WATCH" | "ALERT";

export interface RealtimeFlowOfficialHourReference {
  shareDeltaPP: number | null;
  observedAtMS: number | null;
}

const STATUS_FILTERS: Array<{
  value: HistoryStatusFilter;
  label: string;
}> = [
  { value: "ALL", label: "전체" },
  { value: "ALERT", label: "경고" },
  { value: "WATCH", label: "관찰" },
];

const FLOW_STATUS: Record<
  MarketShareRealtimeFlowStatus,
  { label: string; description: string; className: string }
> = {
  WARMING: {
    label: "준비 중 · 비교 구간 축적",
    description: "직전 동일 길이 구간을 만들 체결을 모으는 중입니다.",
    className: "border-border bg-secondary text-muted-foreground",
  },
  NORMAL: {
    label: "정상 · 평소 범위",
    description: "거래금액과 변화 폭이 현재 경보 기준 안에 있습니다.",
    className: "border-profit/35 bg-profit/10 text-profit",
  },
  WATCH: {
    label: "관찰 중 · 1개 구간 기준 초과",
    description: "10·30·60초 비교 중 1개 구간에서 변화 폭이 기준을 넘었습니다.",
    className: "border-gold/40 bg-gold/10 text-gold",
  },
  ALERT: {
    label: "경고 · 다중 구간 방향 일치",
    description:
      "10·30·60초 비교 중 같은 방향의 기준 초과가 2개 이상 확인됐습니다.",
    className: "border-loss/40 bg-loss/10 text-loss",
  },
  DEGRADED: {
    label: "판단 보류 · 일부 자료 누락",
    description: "새 판정은 보류하고 마지막 정상 관측값을 유지합니다.",
    className: "border-gold/40 bg-gold/10 text-gold",
  },
};

const EPISODE_DIRECTION_CLASS = {
  profit:
    "border-border/80 border-l-2 border-l-profit bg-card text-foreground hover:border-profit/50",
  loss: "border-border/80 border-l-2 border-l-loss bg-card text-foreground hover:border-loss/50",
} as const;

// Stored ten-second states are fetched one at a time, when an episode is
// selected (or when there is no live state to show). Loading the whole
// 30-minute history up front was about 180 states, 1.4 MB gzip / 8 MB JSON,
// on every summary page load. The cache keeps states already shown.
const retainedRealtimeFlowStates = new Map<
  number,
  MarketShareRealtimeFlowState
>();
const RETAINED_REALTIME_FLOW_STATE_LIMIT = 64;
let retainedLatestStoredRealtimeFlow: MarketShareRealtimeFlowState | null =
  null;
let retainedRealtimeFlowSummary: MarketShareRealtimeFlowPeriodSummary | null =
  null;

function retainRealtimeFlowState(state: MarketShareRealtimeFlowState) {
  retainedRealtimeFlowStates.delete(state.as_of_ms);
  retainedRealtimeFlowStates.set(state.as_of_ms, state);
  while (retainedRealtimeFlowStates.size > RETAINED_REALTIME_FLOW_STATE_LIMIT) {
    const oldest = retainedRealtimeFlowStates.keys().next().value;
    if (oldest === undefined) break;
    retainedRealtimeFlowStates.delete(oldest);
  }
}

async function fetchStoredRealtimeFlowState(
  filter: { fromMS: number; toMS: number },
  signal: AbortSignal
): Promise<MarketShareRealtimeFlowState | null> {
  const response = await getMarketShareRealtimeHistory(
    { ...filter, limit: 1 },
    signal
  );
  if (response.contract_version !== REALTIME_FLOW_CONTRACT_VERSION) {
    throw new Error("지원하지 않는 실시간 흐름 계약입니다.");
  }
  return response.items[0] ?? null;
}

function finite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function percent(value: number | null | undefined, digits = 2): string {
  return formatSharePercent(value, digits);
}

function points(value: number | null | undefined): string {
  return finite(value) ? formatSignedPercentagePoints(value, 2) : "—";
}

function bpsPercent(value: number | null | undefined): string {
  return formatSlippageRate(value);
}

function compactKRW(value: number | null | undefined, signed = false): string {
  if (!finite(value)) return "—";
  return signed ? formatKrwCompact(value) : formatKRW(value);
}

function RealtimeFlowEmptyState({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <div className="realtime-flow-empty-state">
      <span className="realtime-flow-empty-icon" aria-hidden="true">
        <Database className="h-4 w-4" />
      </span>
      <div className="min-w-0">
        <p className="realtime-flow-empty-title">{title}</p>
        <p className="realtime-flow-empty-description">{description}</p>
      </div>
    </div>
  );
}

function timestamp(value: number | null | undefined): string {
  const formatted = formatMarketTimeMS(value);
  return formatted === "—" ? "시각 정보 없음" : formatted;
}

function shortTime(value: number): string {
  return formatMarketTimeMS(value, "time-seconds");
}

function directionLabel(
  direction: MarketShareRealtimeFlowState["direction"]
): string {
  return (
    {
      UP: "상승",
      DOWN: "하락",
      STABLE: "안정",
      UNKNOWN: "판단 보류",
    }[direction] ?? direction
  );
}

function toneForDelta(value: number | null | undefined): string {
  if (!finite(value) || Math.abs(value) < 1e-9) return "text-muted-foreground";
  return value > 0 ? "text-profit" : "text-loss";
}

function thresholdUseLabel(
  delta: number | null | undefined,
  threshold: number | null | undefined
): string {
  if (!finite(delta) || !finite(threshold) || threshold <= 0) {
    return "판정 기준 확인 중";
  }
  return `기준 대비 ${Math.round((Math.abs(delta) / threshold) * 100).toLocaleString("ko-KR")}%`;
}

function officialDirectionLabel(value: number | null | undefined): string {
  if (!finite(value)) return "판단 대기";
  if (Math.abs(value) < 1e-9) return "중립";
  return value > 0 ? "상승" : "하락";
}

function imbalanceLabel(value: number | null | undefined): string {
  if (!finite(value)) return "—";
  if (value >= 0.1) return `${value.toFixed(2)} · 매수 잔량 우위`;
  if (value <= -0.1) return `${value.toFixed(2)} · 매도 잔량 우위`;
  return `${value.toFixed(2)} · 균형권`;
}

function StatusPill({ status }: { status: MarketShareRealtimeFlowStatus }) {
  const item = FLOW_STATUS[status];
  return (
    <span
      className={`status-badge inline-flex items-center gap-1 border px-2 py-1 text-base font-semibold ${item.className}`}
    >
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {item.label}
    </span>
  );
}

function OrderbookSummary({
  orderbook,
}: {
  orderbook: MarketShareRealtimeOrderbook | null | undefined;
}) {
  const visible = realtimeFlowOrderbookValuesAreVisible(orderbook);
  if (!visible) {
    return (
      <div className="border border-border bg-inset p-3 text-xs text-muted-foreground">
        10단계 호가를 받지 못해 잔량과 예상 슬리피지를 표시하지 않습니다.
      </div>
    );
  }
  return (
    <div className="grid gap-px overflow-hidden border border-border bg-border sm:grid-cols-2 xl:grid-cols-4">
      <div className="bg-card p-3">
        <p className="text-base text-muted-foreground">
          호가 잔량 불균형 (10단계)
        </p>
        <p className="mt-1 text-sm font-semibold">
          {imbalanceLabel(orderbook.imbalance)}
        </p>
      </div>
      <div className="bg-card p-3">
        <p className="text-base text-muted-foreground">매수·매도 잔량</p>
        <p className="mt-1 text-sm font-semibold">
          {compactKRW(orderbook.bid_depth_krw)} /{" "}
          {compactKRW(orderbook.ask_depth_krw)}
        </p>
      </div>
      <div className="bg-card p-3">
        <p className="text-base text-muted-foreground">매수·매도 가격 차이</p>
        <p className="mt-1 text-sm font-semibold">
          {bpsPercent(orderbook.spread_bps)}
        </p>
      </div>
      <div className="bg-card p-3">
        <p className="text-base text-muted-foreground">
          예상 슬리피지 (매수 / 매도)
        </p>
        <p className="mt-1 text-sm font-semibold">
          {bpsPercent(orderbook.buy_slippage_bps)} /{" "}
          {bpsPercent(orderbook.sell_slippage_bps)}
        </p>
      </div>
    </div>
  );
}

function AssetDriverDetail({
  driver,
  valuesVisible,
}: {
  driver: MarketShareRealtimeFlowDriver | undefined;
  valuesVisible: boolean;
}) {
  if (!driver || !valuesVisible) {
    return (
      <div className="border border-border bg-inset p-4 text-xs text-muted-foreground">
        이 관측에는 코인·거래소별 10초 변화와 세부 호가 자료가 저장되지
        않았습니다.
      </div>
    );
  }
  return (
    <div className="space-y-4">
      <div className="grid gap-px overflow-hidden border border-border bg-border sm:grid-cols-2 xl:grid-cols-4">
        <div className="bg-card p-3">
          <p className="text-base text-muted-foreground">기여(%p)</p>
          <p
            className={`mt-1 text-base font-bold ${toneForDelta(driver.contribution_pp)}`}
          >
            {points(driver.contribution_pp)}
          </p>
        </div>
        <div className="bg-card p-3">
          <p className="text-base text-muted-foreground">빗썸 거래금액 변화</p>
          <p className="mt-1 text-sm font-semibold">
            {compactKRW(driver.bithumb_volume_change_krw, true)}
          </p>
        </div>
        <div className="bg-card p-3">
          <p className="text-base text-muted-foreground">
            경쟁 거래소 합계 변화
          </p>
          <p className="mt-1 text-sm font-semibold">
            {compactKRW(driver.competitor_volume_change_krw, true)}
          </p>
        </div>
        <div className="bg-card p-3">
          <p className="text-base text-muted-foreground">빗썸 매수 체결 비중</p>
          <p className="mt-1 text-sm font-semibold">
            {percent(driver.bithumb_buy_pct, 1)}
          </p>
        </div>
      </div>
      <OrderbookSummary orderbook={driver.orderbook} />
      <div className="overflow-x-auto border border-border">
        <table
          className="market-data-table market-data-table-compact w-full text-left text-xs"
          data-table-schema="label-numeric"
        >
          <thead>
            <tr>
              <th>거래소</th>
              <th className="text-right">기여(%p)</th>
              <th className="text-right">거래금액 변화</th>
              <th className="text-right">매수 체결 비중</th>
              <th className="text-right">10단계 호가</th>
            </tr>
          </thead>
          <tbody>
            {driver.exchange_impacts.slice(0, 5).map(impact => (
              <tr key={impact.exchange}>
                <td className="font-semibold">
                  {exchangeLabel(impact.exchange)}
                </td>
                <td
                  className={`text-right ${toneForDelta(impact.contribution_pp)}`}
                >
                  {points(impact.contribution_pp)}
                </td>
                <td className="text-right">
                  {compactKRW(impact.volume_change_krw, true)}
                </td>
                <td className="text-right">{percent(impact.buy_pct, 1)}</td>
                <td className="text-right">
                  {realtimeFlowOrderbookValuesAreVisible(impact.orderbook)
                    ? imbalanceLabel(impact.orderbook.imbalance)
                    : "최신 호가 아님 · 숨김"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function RealtimeFlowEpisodeCard({
  item,
  selected,
  onSelect,
}: {
  item: RealtimeFlowTenSecondEpisode;
  selected: boolean;
  onSelect: (endMS: number) => void;
}) {
  const directionTone = toneForDelta(item.net_share_change_pp);
  return (
    <button
      type="button"
      aria-pressed={selected}
      onClick={() => onSelect(item.end_ms)}
      data-direction={item.direction.toLowerCase()}
      data-status={item.status.toLowerCase()}
      className={`realtime-flow-episode-item min-h-[84px] rounded-md border px-3 py-2 text-left transition-[border-color,background-color,box-shadow,transform] duration-150 ease-out hover:-translate-y-px hover:shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary active:translate-y-0 active:shadow-none motion-reduce:transition-none motion-reduce:transform-none ${
        EPISODE_DIRECTION_CLASS[
          realtimeFlowEpisodeDirectionTone(item.direction)
        ]
      } ${selected ? "bg-primary/10 ring-1 ring-inset ring-primary/70" : ""}`}
      title={`${timestamp(item.start_ms)} ~ ${timestamp(item.end_ms)} · ${FLOW_STATUS[item.status].label}`}
    >
      <span className="flex items-center justify-between gap-3">
        <span className="font-mono text-xs font-medium tabular-nums text-muted-foreground">
          {shortTime(item.start_ms)}
          {item.end_ms !== item.start_ms ? `–${shortTime(item.end_ms)}` : ""}
        </span>
        <span className="rounded-full border border-border bg-secondary px-2 py-0.5 text-base font-medium tracking-tight text-muted-foreground">
          {realtimeFlowEpisodeStatusLabel(item.status)}
        </span>
      </span>
      <span className="mt-1.5 flex items-baseline justify-between gap-3">
        {/* The change value carries the weight; the label stays regular. */}
        <span className={`text-xs ${directionTone}`}>
          {directionLabel(item.direction)}
          <span className="ml-1.5 font-normal text-muted-foreground">
            {item.observation_count}회
          </span>
        </span>
        <span
          className={`text-base font-bold leading-none tracking-tight tabular-nums ${directionTone}`}
        >
          {points(item.net_share_change_pp)}
        </span>
      </span>
      <span className="realtime-flow-episode-drivers mt-1.5 line-clamp-2 border-t border-border/70 pt-1.5 text-base leading-6 text-muted-foreground">
        {item.drivers.length
          ? item.drivers
              .slice(0, 3)
              .map(
                driver =>
                  `${driver.asset} ${points(driver.net_contribution_pp)}`
              )
              .join(" · ")
          : "10초 코인 기여 상세 없음"}
      </span>
    </button>
  );
}

export function RealtimeFlowPanel({
  tickStore,
  realtimeFlowStore,
  connection,
  officialHourReference = null,
  fallbackState = null,
}: {
  tickStore: MarketShareTickStore;
  realtimeFlowStore: MarketShareRealtimeFlowStore;
  connection: MarketShareConnection;
  officialHourReference?: RealtimeFlowOfficialHourReference | null;
  fallbackState?: MarketShareRealtimeFlowState | null;
}) {
  const currentTick = useSyncExternalStore(
    tickStore.subscribe,
    tickStore.getSnapshot,
    tickStore.getSnapshot
  );
  const streamedState = useSyncExternalStore(
    realtimeFlowStore.subscribe,
    realtimeFlowStore.getSnapshot,
    realtimeFlowStore.getSnapshot
  );
  // Dedicated observations can arrive before the next market tick.
  const tickState = streamedState ?? currentTick?.realtime_flow ?? null;
  const retainedStateRef = useRef<MarketShareRealtimeFlowState | null>(
    fallbackState
  );
  const retainedWithFallback = selectStableRealtimeFlowState(
    fallbackState,
    retainedStateRef.current
  );
  const currentState = selectStableRealtimeFlowState(
    tickState,
    retainedWithFallback
  );
  if (realtimeFlowTenSecondSample(currentState)) {
    retainedStateRef.current = currentState;
  }
  const summaryToMS = currentState?.as_of_ms
    ? Math.floor(currentState.as_of_ms / 10_000) * 10_000
    : 0;
  const historyAnchorSeconds =
    currentTick?.generated_at ?? (currentState?.as_of_ms ?? 0) / 1_000;
  const historyRange = realtimeFlowHistoryRange(historyAnchorSeconds);
  const historyRangeKey = historyRange
    ? `${historyRange.fromMS}:${historyRange.toMS}`
    : "";
  const transportIsLive = realtimeFlowTransportIsLive(currentTick, connection);
  const currentTenSecondSample = realtimeFlowTenSecondSample(currentState);
  const currentSampleIsLive = Boolean(
    transportIsLive && tickState && currentState === tickState
  );
  const currentTenSecondWindow = currentTenSecondSample?.window ?? null;
  const currentDataStatus: MarketShareRealtimeFlowStatus = !currentSampleIsLive
    ? "DEGRADED"
    : currentTenSecondSample
      ? "NORMAL"
      : currentState?.status === "DEGRADED"
        ? "DEGRADED"
        : "WARMING";
  const [selectedStored, setSelectedStored] =
    useState<MarketShareRealtimeFlowState | null>(null);
  const [latestStored, setLatestStored] =
    useState<MarketShareRealtimeFlowState | null>(
      retainedLatestStoredRealtimeFlow
    );
  const [daySummary, setDaySummary] =
    useState<MarketShareRealtimeFlowPeriodSummary | null>(
      retainedRealtimeFlowSummary
    );
  const [daySummaryError, setDaySummaryError] = useState("");
  const [historyError, setHistoryError] = useState("");
  const [historyLoading, setHistoryLoading] = useState(false);
  const [refreshToken, setRefreshToken] = useState(0);
  const [historyStatus, setHistoryStatus] =
    useState<HistoryStatusFilter>("ALL");
  const [olderEpisodesOpen, setOlderEpisodesOpen] = useState(false);
  const [selectedAsOfMS, setSelectedAsOfMS] = useState<number | null>(null);
  const [selectedAsset, setSelectedAsset] = useState("");

  // The selected episode's stored state, fetched when it is selected.
  useEffect(() => {
    if (selectedAsOfMS === null) {
      setSelectedStored(null);
      setHistoryError("");
      return;
    }
    const cached = retainedRealtimeFlowStates.get(selectedAsOfMS);
    if (cached && refreshToken === 0) {
      setSelectedStored(cached);
      setHistoryError("");
      return;
    }
    const controller = new AbortController();
    setHistoryLoading(true);
    void fetchStoredRealtimeFlowState(
      { fromMS: selectedAsOfMS, toMS: selectedAsOfMS },
      controller.signal
    )
      .then(state => {
        if (controller.signal.aborted) return;
        const exact = state?.as_of_ms === selectedAsOfMS ? state : null;
        if (exact) retainRealtimeFlowState(exact);
        setSelectedStored(exact);
        setHistoryError(
          exact ? "" : "선택한 시각의 저장된 관측을 찾지 못했습니다."
        );
      })
      .catch(caughtError => {
        if ((caughtError as { name?: string }).name === "AbortError") return;
        setHistoryError("저장된 실시간 흐름 이력을 불러오지 못했습니다.");
      })
      .finally(() => {
        if (!controller.signal.aborted) setHistoryLoading(false);
      });
    return () => controller.abort();
  }, [selectedAsOfMS, refreshToken]);

  // Without a live state the detail shows the latest stored one, as before.
  const needsLatestStored = !currentState && historyRange !== null;
  useEffect(() => {
    if (!needsLatestStored || !historyRange) return;
    if (document.visibilityState === "hidden") return;
    const controller = new AbortController();
    void fetchStoredRealtimeFlowState(historyRange, controller.signal)
      .then(state => {
        if (controller.signal.aborted) return;
        retainedLatestStoredRealtimeFlow = state;
        setLatestStored(state);
      })
      .catch(caughtError => {
        if ((caughtError as { name?: string }).name === "AbortError") return;
        setHistoryError("저장된 실시간 흐름 이력을 불러오지 못했습니다.");
      });
    return () => controller.abort();
  }, [needsLatestStored, historyRangeKey, refreshToken]);

  // The 24-hour summary is fetched every 10 seconds; skip it while the panel
  // is scrolled far away and fetch again as soon as it comes back.
  const panelRef = useRef<HTMLElement>(null);
  const panelNearViewport = useNearViewport(panelRef);
  useEffect(() => {
    if (
      !panelNearViewport ||
      summaryToMS <= 0 ||
      document.visibilityState === "hidden"
    ) {
      return;
    }
    const controller = new AbortController();
    void getMarketShareRealtimeSummary(summaryToMS, controller.signal, {
      fromMS:
        summaryToMS - REALTIME_FLOW_HISTORY_MS - SUMMARY_EPISODE_MARGIN_MS,
    })
      .then(value => {
        retainedRealtimeFlowSummary = value;
        setDaySummary(value);
        setDaySummaryError("");
      })
      .catch(caughtError => {
        if ((caughtError as { name?: string }).name !== "AbortError") {
          setDaySummaryError("지난 24시간 요약을 불러오지 못했습니다.");
        }
      });
    return () => controller.abort();
  }, [panelNearViewport, refreshToken, summaryToMS]);

  const episodes = useMemo(
    () =>
      historyRange
        ? selectRealtimeFlowEpisodes(
            daySummary,
            historyRange.fromMS,
            historyRange.toMS
          )
        : [],
    [daySummary, historyRangeKey]
  );
  // An episode that has left the 30-minute list is deselected, as when the
  // stored history used to drop it.
  useEffect(() => {
    if (
      selectedAsOfMS !== null &&
      daySummary &&
      !episodes.some(item => item.end_ms === selectedAsOfMS)
    )
      setSelectedAsOfMS(null);
  }, [episodes, daySummary, selectedAsOfMS]);
  const filteredEpisodes = useMemo(
    () =>
      historyStatus === "ALL"
        ? episodes
        : episodes.filter(item => item.status === historyStatus),
    [episodes, historyStatus]
  );
  // Each filter chip shows how many episodes it holds, so an empty filter is
  // visible before it is chosen. Before the history arrives there is no count.
  const episodeCounts = useMemo(
    () =>
      daySummary && historyRange
        ? {
            ALL: episodes.length,
            ALERT: episodes.filter(item => item.status === "ALERT").length,
            WATCH: episodes.filter(item => item.status === "WATCH").length,
          }
        : null,
    [daySummary, historyRangeKey, episodes]
  );
  const recentEpisodes = previewRealtimeFlowEpisodes(filteredEpisodes, false);
  const olderEpisodes = filteredEpisodes.slice(recentEpisodes.length);
  const dataIntervals = useMemo(
    () =>
      historyRange
        ? selectRealtimeFlowMissingIntervals(
            daySummary,
            historyRange.fromMS,
            historyRange.toMS
          )
        : [],
    [daySummary, historyRangeKey]
  );
  const currentDisplayStatus: MarketSignalStatus | null = currentTenSecondSample
    ? currentState?.status === "WATCH" || currentState?.status === "ALERT"
      ? currentState.status
      : "NORMAL"
    : null;
  const currentReferenceStatus = currentSampleIsLive
    ? (currentDisplayStatus ?? currentDataStatus)
    : "DEGRADED";
  const currentReferenceDirection =
    currentTenSecondSample?.direction ?? "UNKNOWN";
  const currentReferenceAttribution = currentTenSecondSample
    ? realtimeFlowTenSecondAttribution(currentState)
    : null;
  const currentLeadingAsset = currentReferenceAttribution?.drivers[0] ?? null;
  const currentLeadingExchange =
    currentReferenceAttribution?.exchange_drivers[0] ?? null;
  const currentSignalHeadline = realtimeFlowSignalHeadline(
    currentState,
    currentReferenceStatus,
    currentReferenceDirection
  );
  const realtimeComparisonLabel = currentSampleIsLive
    ? "현재 10초"
    : "마지막 정상 10초";
  const directionAlignment = compareOfficialHourToRealtimeDirection(
    officialHourReference?.shareDeltaPP,
    currentReferenceDirection,
    Boolean(currentTenSecondSample)
  );

  useEffect(() => setOlderEpisodesOpen(false), [historyStatus]);
  const currentReferenceHeadline =
    directionAlignment === "SAME"
      ? `지금도 1시간 추세와 같은 방향 · 공식 1시간 ${officialDirectionLabel(officialHourReference?.shareDeltaPP)}, ${realtimeComparisonLabel} ${directionLabel(currentReferenceDirection)}`
      : directionAlignment === "OPPOSITE"
        ? `지금은 1시간 추세와 반대 · 공식 1시간 ${officialDirectionLabel(officialHourReference?.shareDeltaPP)}, ${realtimeComparisonLabel} ${directionLabel(currentReferenceDirection)}`
        : directionAlignment === "NEUTRAL"
          ? "한쪽이 중립이어서 지속·반전을 아직 구분하지 않습니다."
          : currentSignalHeadline;
  const conclusionTone =
    directionAlignment === "SAME"
      ? "border-profit/35 bg-profit/10"
      : directionAlignment === "OPPOSITE"
        ? "border-loss/35 bg-loss/10"
        : directionAlignment === "NEUTRAL"
          ? "border-gold/35 bg-gold/10"
          : "border-info/30 bg-info/10";
  const selectedHistory =
    selectedStored && selectedStored.as_of_ms === selectedAsOfMS
      ? selectedStored
      : null;
  const detailState = selectedHistory ?? currentState ?? latestStored ?? null;
  const detailIsCurrent = Boolean(currentState && detailState === currentState);
  const detailTenSecondWindow = detailIsCurrent
    ? currentTenSecondWindow
    : realtimeFlowTenSecondWindow(detailState);
  const detailValuesVisible = Boolean(detailTenSecondWindow);
  const detailSample = realtimeFlowTenSecondSample(detailState);
  const detailStatus: MarketShareRealtimeFlowStatus = detailIsCurrent
    ? currentSampleIsLive
      ? (currentDisplayStatus ?? currentDataStatus)
      : "DEGRADED"
    : detailSample &&
        (detailState?.status === "NORMAL" ||
          detailState?.status === "WATCH" ||
          detailState?.status === "ALERT")
      ? detailState.status
      : (detailState?.status ?? "WARMING");
  const detailAttribution = detailValuesVisible
    ? realtimeFlowTenSecondAttribution(detailState)
    : null;
  const detailHasTenSecondDrivers = Boolean(detailAttribution);
  const coverageSummaryAvailable = Boolean(
    historyRange &&
    daySummary &&
    daySummary.from_ms <= historyRange.fromMS &&
    daySummary.to_ms >= historyRange.toMS - 10_000
  );
  const drivers = detailAttribution?.drivers ?? [];
  const activeDriver =
    drivers.find(driver => driver.asset === selectedAsset) ?? drivers[0];
  // The page footer carries the one causation note; only say why details are
  // missing when they are.
  const preStorageNote =
    !detailHasTenSecondDrivers && detailValuesVisible
      ? "이 관측은 10초 기여 저장을 시작하기 전에 기록되어 코인별 상세값이 없습니다."
      : null;

  return (
    <section
      ref={panelRef}
      className="glass-card realtime-flow-panel overflow-hidden"
    >
      <div className="realtime-flow-panel-header flex flex-wrap items-start justify-between gap-3 border-b border-border px-4 py-4">
        <div className="realtime-flow-panel-heading min-w-0">
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <Radio className="h-4 w-4 text-muted-foreground" />
            10초 점유율 변화 감시
            <InlineHelp text="최근 완료된 같은 시각의 3개 거래소 공식 1시간 방향과 국내 5개 거래소의 실제 10초 방향을 비교합니다. 두 값은 별도로 계산합니다." />
          </h3>
        </div>
        <StatusPill status={currentReferenceStatus} />
      </div>

      <div className="realtime-flow-reference-layer border-b border-border bg-inset p-4 sm:p-5">
        <div className="realtime-flow-reference-layout grid gap-3 xl:grid-cols-[minmax(18rem,0.9fr)_minmax(0,1.6fr)]">
          <article
            className={`realtime-flow-reference-conclusion flex min-h-40 min-w-0 flex-col justify-between border p-4 sm:p-5 ${conclusionTone}`}
            data-reference-status={currentReferenceStatus.toLowerCase()}
            data-direction-alignment={directionAlignment.toLowerCase()}
          >
            <div>
              <p className="realtime-flow-reference-kicker text-xs font-semibold text-info">
                공식 1시간 vs {realtimeComparisonLabel}
              </p>
              <h4
                className="realtime-flow-reference-headline mt-2 text-lg font-semibold leading-7 tracking-tight"
                aria-live="polite"
                aria-atomic="true"
              >
                {currentReferenceHeadline}
              </h4>
              <p className="realtime-flow-reference-copy mt-2 text-xs leading-6 text-muted-foreground">
                {currentSignalHeadline}
              </p>
            </div>
            <p className="realtime-flow-reference-meta mt-4 border-t border-info/20 pt-3 text-xs text-muted-foreground">
              {officialHourReference?.observedAtMS
                ? `공식 1시간 종료 ${timestamp(officialHourReference.observedAtMS)} · `
                : "공식 1시간 비교 대기 · "}
              {currentTenSecondSample
                ? `${transportIsLive ? "10초 관측" : "마지막 정상 관측"} ${timestamp(currentState?.as_of_ms)}`
                : "정상 10초 관측 대기"}
            </p>
          </article>

          <div className="realtime-flow-reference-flow grid min-w-0 gap-3 sm:grid-cols-2">
            <article className="realtime-flow-reference-card realtime-flow-reference-direction flex min-h-32 min-w-0 flex-col border border-border bg-card p-4">
              <p className="realtime-flow-reference-card-label flex items-center gap-2 text-xs text-muted-foreground">
                <Radio className="h-3.5 w-3.5" />
                공식 1시간 → {realtimeComparisonLabel}
              </p>
              <p
                className={`realtime-flow-reference-card-value mt-2 text-xl font-semibold ${toneForDelta(currentTenSecondWindow?.share_delta_pp)}`}
              >
                {officialHourReference && currentTenSecondSample
                  ? `${officialDirectionLabel(officialHourReference.shareDeltaPP)} → ${directionLabel(currentReferenceDirection)}`
                  : "비교 대기"}
              </p>
              <p className="realtime-flow-reference-card-detail mt-auto pt-3 text-xs leading-5 text-muted-foreground">
                {currentTenSecondWindow
                  ? `공식 ${points(officialHourReference?.shareDeltaPP)} · 10초 ${points(currentTenSecondWindow.share_delta_pp)}`
                  : "완전한 직전·최근 10초 비교 자료 대기"}
              </p>
            </article>

            <article className="realtime-flow-reference-card realtime-flow-reference-strength flex min-h-32 min-w-0 flex-col border border-border bg-card p-4">
              <p className="realtime-flow-reference-card-label flex items-center gap-2 text-xs text-muted-foreground">
                <Activity className="h-3.5 w-3.5" />
                10초 변화폭
              </p>
              <p
                className={`realtime-flow-reference-card-value mt-2 text-xl font-semibold ${toneForDelta(currentTenSecondWindow?.share_delta_pp)}`}
              >
                <AnimatedMetricValue
                  preserveRawValue
                  value={currentTenSecondWindow?.share_delta_pp}
                  format={value => points(value)}
                  width="compact"
                />
              </p>
              <p className="realtime-flow-reference-card-detail mt-auto pt-3 text-xs leading-5 text-muted-foreground">
                {currentTenSecondWindow
                  ? finite(currentTenSecondWindow.threshold_pp)
                    ? `${thresholdUseLabel(currentTenSecondWindow.share_delta_pp, currentTenSecondWindow.threshold_pp)} · 기준 ±${currentTenSecondWindow.threshold_pp.toFixed(2)}%p`
                    : "변화는 관측했지만 판정 기준은 확인 중"
                  : FLOW_STATUS[currentReferenceStatus].description}
              </p>
            </article>

            <article className="realtime-flow-reference-card realtime-flow-reference-asset flex min-h-32 min-w-0 flex-col border border-border bg-card p-4">
              <p className="realtime-flow-reference-card-label flex items-center gap-2 text-xs text-muted-foreground">
                <Database className="h-3.5 w-3.5" />
                주요 기여 코인
              </p>
              <p className="realtime-flow-reference-card-value mt-2 flex items-baseline justify-between gap-3 text-lg font-semibold">
                <span>{currentLeadingAsset?.asset ?? "기여 자료 대기"}</span>
                <AnimatedMetricValue
                  preserveRawValue
                  resetKey={currentLeadingAsset?.asset ?? "none"}
                  value={currentLeadingAsset?.contribution_pp}
                  format={value => points(value)}
                  width="compact"
                  className={`shrink-0 text-base ${toneForDelta(currentLeadingAsset?.contribution_pp)}`}
                />
              </p>
              <p className="realtime-flow-reference-card-detail mt-auto pt-3 text-xs leading-5 text-muted-foreground">
                {currentLeadingAsset
                  ? `빗썸 ${compactKRW(currentLeadingAsset.bithumb_volume_change_krw, true)} · 경쟁 ${compactKRW(currentLeadingAsset.competitor_volume_change_krw, true)}`
                  : "전용 10초 코인 기여가 저장되면 표시"}
              </p>
            </article>

            <article className="realtime-flow-reference-card realtime-flow-reference-exchange flex min-h-32 min-w-0 flex-col border border-border bg-card p-4">
              <p className="realtime-flow-reference-card-label flex items-center gap-2 text-xs text-muted-foreground">
                <Activity className="h-3.5 w-3.5" />
                주요 기여 거래소
              </p>
              <p className="realtime-flow-reference-card-value mt-2 flex items-baseline justify-between gap-3 text-lg font-semibold">
                <span>
                  {currentLeadingExchange
                    ? exchangeLabel(currentLeadingExchange.exchange)
                    : "기여 자료 대기"}
                </span>
                <AnimatedMetricValue
                  preserveRawValue
                  resetKey={currentLeadingExchange?.exchange ?? "none"}
                  value={currentLeadingExchange?.contribution_pp}
                  format={value => points(value)}
                  width="compact"
                  className={`shrink-0 text-base ${toneForDelta(currentLeadingExchange?.contribution_pp)}`}
                />
              </p>
              <p className="realtime-flow-reference-card-detail mt-auto pt-3 text-xs leading-5 text-muted-foreground">
                {currentLeadingExchange
                  ? `${compactKRW(currentLeadingExchange.volume_change_krw, true)} · ${currentLeadingExchange.leading_asset || "주요 코인 없음"}`
                  : "전용 10초 거래소 기여가 저장되면 표시"}
              </p>
            </article>
          </div>
        </div>
      </div>

      <div className="realtime-flow-workspace">
        <div
          className="realtime-flow-events border-b border-border p-4 sm:p-5"
          data-flow-section={REALTIME_FLOW_PANEL_SECTION_ORDER[0]}
        >
          <div className="realtime-flow-section-header flex flex-wrap items-start justify-between gap-3">
            <div>
              <h4 className="flex items-center gap-2 text-sm font-semibold tracking-tight">
                <Clock3 className="h-4 w-4 text-muted-foreground" />
                최근 30분 점유율 변화 기록
                <InlineHelp text="최근 30분 동안 서버가 기준을 넘었다고 판정한 변화(관찰·경고)를 표시합니다. 경고는 10·30·60초 비교 중 2개 이상이 같은 방향으로 기준을 넘은 경우이며, 같은 방향으로 이어진 관측은 한 사건으로 묶습니다." />
              </h4>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-xs text-muted-foreground">
                {daySummary?.to_ms ? timestamp(daySummary.to_ms) : "이력 대기"}
              </span>
              <button
                type="button"
                onClick={() => {
                  if (!historyLoading) setRefreshToken(value => value + 1);
                }}
                className="inline-flex min-h-8 items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-muted-foreground transition-[color,background-color,border-color,transform] duration-150 hover:border-primary/40 hover:bg-primary/[0.05] hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary active:translate-y-px disabled:pointer-events-none disabled:opacity-50 motion-reduce:transition-none motion-reduce:transform-none"
              >
                <RefreshCw className="h-3 w-3" />
                새로고침
              </button>
            </div>
          </div>
          <div className="realtime-flow-event-toolbar mt-4 flex flex-wrap gap-2">
            {STATUS_FILTERS.map(filter => (
              <ToggleChip
                key={filter.value}
                pressed={historyStatus === filter.value}
                onClick={() => {
                  setHistoryStatus(filter.value);
                }}
              >
                {filter.label}
                {episodeCounts ? (
                  <span className="ml-1.5 tabular-nums opacity-75">
                    {episodeCounts[filter.value].toLocaleString("ko-KR")}
                  </span>
                ) : null}
              </ToggleChip>
            ))}
            <ToggleChip
              pressed={selectedAsOfMS === null}
              onClick={() => setSelectedAsOfMS(null)}
              className="ml-auto"
            >
              현재로 돌아가기
            </ToggleChip>
          </div>
          {daySummaryError || historyError ? (
            <div className="realtime-flow-inline-warning mt-3 flex items-start gap-2 border border-gold/30 bg-gold/10 p-3 text-xs text-gold">
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
              {daySummaryError || historyError} 현재 실시간 진단은 계속
              표시하며, 마지막 정상 사건 이력을 유지합니다.
            </div>
          ) : null}
          {filteredEpisodes.length ? (
            <>
              <div
                className="realtime-flow-episode-list mt-4 grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3"
                aria-label="10초 기준 초과 시장 사건"
              >
                {recentEpisodes.map(item => (
                  <RealtimeFlowEpisodeCard
                    key={item.id}
                    item={item}
                    selected={item.end_ms === selectedAsOfMS}
                    onSelect={setSelectedAsOfMS}
                  />
                ))}
              </div>
              {olderEpisodes.length > 0 && (
                <details
                  open={olderEpisodesOpen}
                  onToggle={event =>
                    setOlderEpisodesOpen(event.currentTarget.open)
                  }
                  className="realtime-flow-older-episodes mt-3 overflow-hidden rounded-md border border-border bg-inset"
                >
                  <summary className="flex cursor-pointer list-none items-center justify-between gap-3 px-3 py-2.5 text-xs font-semibold transition-colors hover:bg-primary/5 focus-visible:bg-primary/10 focus-visible:outline-none">
                    <span>
                      최근 {recentEpisodes.length.toLocaleString("ko-KR")}건
                      표시 · 전체{" "}
                      {filteredEpisodes.length.toLocaleString("ko-KR")}건
                    </span>
                    <span className="inline-flex items-center gap-2 text-muted-foreground">
                      이전 {olderEpisodes.length.toLocaleString("ko-KR")}건{" "}
                      {olderEpisodesOpen ? "접기" : "펼쳐보기"}
                      <ChevronDown
                        className={`h-4 w-4 transition-transform ${
                          olderEpisodesOpen ? "rotate-180" : ""
                        }`}
                        aria-hidden="true"
                      />
                    </span>
                  </summary>
                  {olderEpisodesOpen && (
                    <div className="realtime-flow-episode-list grid gap-2.5 border-t border-border p-3 sm:grid-cols-2 xl:grid-cols-3">
                      {olderEpisodes.map(item => (
                        <RealtimeFlowEpisodeCard
                          key={item.id}
                          item={item}
                          selected={item.end_ms === selectedAsOfMS}
                          onSelect={setSelectedAsOfMS}
                        />
                      ))}
                    </div>
                  )}
                </details>
              )}
            </>
          ) : (
            <RealtimeFlowEmptyState
              title="선택한 조건의 시장 사건이 없습니다"
              description="기준을 넘는 변화가 발생하면 시간순 사건으로 이 자리에 표시됩니다."
            />
          )}

          <div className="realtime-flow-data-status mt-4 border-t border-border pt-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <h5 className="text-xs font-semibold">데이터 수집 상태</h5>
              </div>
              <span className="text-base text-muted-foreground">
                최근 30분{" "}
                {coverageSummaryAvailable
                  ? `${dataIntervals.length}개 구간`
                  : "저장 이력 확인 중"}
              </span>
            </div>
            {dataIntervals.length ? (
              <div
                className="realtime-flow-status-track mt-2"
                aria-label={`최근 30분 수집 불완전 ${dataIntervals.length}개 구간`}
              >
                {dataIntervals.slice(0, 3).map(interval => (
                  <div
                    key={`${interval.start_ms}-${interval.end_ms}`}
                    className="realtime-flow-status-segment realtime-flow-status-segment-degraded border border-border bg-inset px-3 py-2 text-base text-muted-foreground"
                  >
                    <span className="font-mono text-foreground">
                      {shortTime(interval.start_ms)}
                      {interval.end_ms !== interval.start_ms
                        ? `–${shortTime(interval.end_ms)}`
                        : ""}
                    </span>
                    <span className="ml-2 font-semibold text-gold">
                      저장 이력 누락
                    </span>
                  </div>
                ))}
              </div>
            ) : coverageSummaryAvailable ? (
              <div className="realtime-flow-status-track realtime-flow-status-track-complete mt-2">
                <span className="realtime-flow-status-segment realtime-flow-status-segment-complete">
                  <CheckCircle2 className="h-3.5 w-3.5" />
                  최근 30분 저장 이력에 수집 불완전 구간이 없습니다.
                </span>
              </div>
            ) : (
              <div className="realtime-flow-status-track mt-2">
                <span className="realtime-flow-status-segment border border-border bg-inset text-muted-foreground">
                  최근 30분 저장 이력을 확인하고 있습니다.
                </span>
              </div>
            )}
          </div>
        </div>

        <div
          className="realtime-flow-detail space-y-4 p-4"
          data-flow-section={REALTIME_FLOW_PANEL_SECTION_ORDER[1]}
        >
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <div className="flex flex-wrap items-center gap-2">
                <h4 className="text-sm font-semibold">
                  {detailIsCurrent
                    ? currentSampleIsLive
                      ? "현재"
                      : "마지막 정상"
                    : timestamp(detailState?.as_of_ms)}{" "}
                  10초 관측 상세
                </h4>
                <StatusPill status={detailStatus} />
              </div>
              <p className="mt-1 text-xs leading-6 text-muted-foreground">
                {detailTenSecondWindow
                  ? `직전 ${percent(detailTenSecondWindow.previous_share_pct)} → 최근 ${percent(detailTenSecondWindow.current_share_pct)} · ${points(detailTenSecondWindow.share_delta_pp)}`
                  : (detailState?.reason ??
                    "10초 비교 자료를 축적하고 있습니다.")}
              </p>
            </div>
            <div className="text-left text-base text-muted-foreground sm:text-right">
              <p>
                {detailHasTenSecondDrivers
                  ? drivers.length === detailAttribution?.stored_driver_count
                    ? `10초 기여 ${detailAttribution.stored_driver_count}/${detailAttribution.total_driver_count}개`
                    : `10초 기여 화면 ${drivers.length}개 · 저장 ${detailAttribution?.stored_driver_count ?? 0}/${detailAttribution?.total_driver_count ?? 0}개`
                  : "10초 코인 기여 미저장"}
              </p>
              <p>
                기타 코인 합계{" "}
                {detailHasTenSecondDrivers
                  ? points(detailAttribution?.residual_contribution_pp)
                  : "—"}
              </p>
            </div>
          </div>

          {preStorageNote && (
            <div className="realtime-detail-method-static border border-info/25 bg-info/10 p-3 text-xs leading-6 text-muted-foreground">
              {preStorageNote}
            </div>
          )}

          {detailHasTenSecondDrivers ? (
            <div className="realtime-flow-detail-grid realtime-flow-contribution-flow grid gap-4 xl:grid-cols-[1.15fr_0.85fr]">
              <div className="space-y-3">
                <div className="realtime-flow-driver-card-list flex flex-wrap gap-2">
                  {drivers.map(driver => (
                    <button
                      key={driver.asset}
                      type="button"
                      aria-pressed={activeDriver?.asset === driver.asset}
                      onClick={() => setSelectedAsset(driver.asset)}
                      className={`realtime-flow-driver-card border px-3 py-2 text-left text-xs ${
                        activeDriver?.asset === driver.asset
                          ? "border-info bg-info/10"
                          : "border-border hover:border-info/60"
                      }`}
                    >
                      <span className="font-semibold">{driver.asset}</span>
                      <span
                        className={`ml-2 ${toneForDelta(driver.contribution_pp)}`}
                      >
                        {points(driver.contribution_pp)}
                      </span>
                    </button>
                  ))}
                </div>
                <AssetDriverDetail
                  driver={activeDriver}
                  valuesVisible={detailHasTenSecondDrivers}
                />
              </div>

              <div className="realtime-flow-exchange-contribution overflow-hidden border border-border">
                <div className="flex items-center gap-2 border-b border-border px-3 py-3 text-xs font-semibold">
                  <Activity className="h-4 w-4 text-muted-foreground" />
                  거래소별 기여(%p)
                </div>
                {detailHasTenSecondDrivers &&
                detailAttribution?.exchange_drivers?.length ? (
                  <div className="overflow-x-auto">
                    <table
                      className="market-data-table market-data-table-compact w-full text-left text-xs"
                      data-table-schema="label-numeric-label"
                    >
                      <thead>
                        <tr>
                          <th>거래소</th>
                          <th className="text-right">기여</th>
                          <th className="text-right">거래금액 변화</th>
                          <th>주요 코인</th>
                        </tr>
                      </thead>
                      <tbody>
                        {detailAttribution.exchange_drivers
                          .slice(0, 5)
                          .map(driver => (
                            <tr key={driver.exchange}>
                              <td className="font-semibold">
                                {exchangeLabel(driver.exchange)}
                              </td>
                              <td
                                className={`text-right ${toneForDelta(driver.contribution_pp)}`}
                              >
                                {points(driver.contribution_pp)}
                              </td>
                              <td className="text-right">
                                {compactKRW(driver.volume_change_krw, true)}
                              </td>
                              <td>
                                {driver.leading_asset} ·{" "}
                                {points(driver.leading_asset_contribution_pp)}
                              </td>
                            </tr>
                          ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <div className="p-4 text-xs leading-6 text-muted-foreground">
                    이 관측에는 전용 10초 거래소 기여값이 저장되지 않았습니다.
                  </div>
                )}
              </div>
            </div>
          ) : (
            <RealtimeFlowEmptyState
              title="세부 기여를 준비하고 있습니다"
              description="완전한 10초 비교 자료가 저장되면 코인·거래소별 변화와 세부 호가 자료가 이 자리에 함께 표시됩니다."
            />
          )}
        </div>
      </div>
    </section>
  );
}
