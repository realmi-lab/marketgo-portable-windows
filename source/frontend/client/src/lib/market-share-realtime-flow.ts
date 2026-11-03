import type {
  MarketShareRealtimeFlowHistoryResponse,
  MarketShareRealtimeFlowAttribution,
  MarketShareRealtimeFlowCoverageSegment,
  MarketShareRealtimeFlowDriver,
  MarketShareRealtimeFlowEpisode,
  MarketShareRealtimeFlowPeriodSummary,
  MarketShareRealtimeFlowState,
  MarketShareRealtimeFlowStatus,
  MarketShareRealtimeFlowWindow,
  MarketShareRealtimeOrderbook,
  MarketShareRealtimeTick,
} from "./market-share-types";

export const REALTIME_FLOW_CONTRACT_VERSION = "REALTIME_FLOW_V2";

/** Describe the server's evidence; these reasons do not assert socket state. */
export function realtimeFlowCoverageLabel(status: string, reason?: string): string {
  const normalized = status.toUpperCase();
  if (normalized.includes("ATTRIBUT")) return "코인 기여 확인";
  if (normalized.includes("MISSING") || normalized.includes("GAP") || normalized.includes("UNAVAILABLE")) {
    switch (reason) {
      case "NO_SAMPLE": return "비교 기록 없음";
      case "OBSERVATION_INCOMPLETE": return "관측 확인 불완전";
      case "COMPARISON_INCOMPLETE": return "비교 구간 자료 부족";
      case "SAMPLE_UNUSABLE": return "비교값 확인 불가";
      default: return "실시간 비교 불완전";
    }
  }
  if (normalized.includes("PARTIAL")) return "부분 관측";
  return "직접 관측";
}
// A 30-minute range contains 180 ten-second observations. Keep a small margin
// so the requested range and the retained detail rows cannot silently diverge.
export const REALTIME_FLOW_HISTORY_FETCH_LIMIT = 200;
export const REALTIME_FLOW_HISTORY_MS = 30 * 60 * 1_000;
export const REALTIME_FLOW_HISTORY_RENDER_LIMIT = 200;
export const REALTIME_FLOW_EPISODE_PREVIEW_LIMIT = 10;
export const REALTIME_FLOW_PANEL_SECTION_ORDER = ["events", "detail"] as const;
export const REALTIME_FLOW_EPISODE_MAX_GAP_MS = 15_000;
export const REALTIME_FLOW_ONE_SECOND_MAX_GAP_MS = 1_500;

const DISPLAYABLE_STATUSES = new Set<MarketShareRealtimeFlowStatus>([
  "NORMAL",
  "WATCH",
  "ALERT",
]);
const KNOWN_STATUSES = new Set<MarketShareRealtimeFlowStatus>([
  "WARMING",
  "NORMAL",
  "WATCH",
  "ALERT",
  "DEGRADED",
]);
const QUALITY_KEYS = [
  "source",
  "frame_status",
  "complete_frame",
  "retained_frame_count",
  "history_span_sec",
  "missing_frame_count",
  "required_history_sec",
  "official_24h_reconciled",
] as const;

export interface RealtimeFlowHistoryFilter {
  fromMS?: number;
  toMS?: number;
  status?: MarketShareRealtimeFlowStatus;
  asset?: string;
  exchange?: string;
  limit?: number;
}

export type RealtimeFlowTenSecondDirection = "UP" | "DOWN";

export type RealtimeFlowDirectionAlignment =
  "SAME" | "OPPOSITE" | "NEUTRAL" | "UNAVAILABLE";

export function compareOfficialHourToRealtimeDirection(
  officialHourDeltaPP: number | null | undefined,
  realtimeDirection: MarketShareRealtimeFlowState["direction"],
  hasRealtimeSample: boolean
): RealtimeFlowDirectionAlignment {
  if (
    !hasRealtimeSample ||
    !finite(officialHourDeltaPP) ||
    realtimeDirection === "UNKNOWN"
  ) {
    return "UNAVAILABLE";
  }
  if (Math.abs(officialHourDeltaPP) < 1e-9 || realtimeDirection === "STABLE") {
    return "NEUTRAL";
  }
  const officialDirection = officialHourDeltaPP > 0 ? "UP" : "DOWN";
  return officialDirection === realtimeDirection ? "SAME" : "OPPOSITE";
}

export interface RealtimeFlowTenSecondSample {
  state: MarketShareRealtimeFlowState;
  window: MarketShareRealtimeFlowWindow;
  direction: RealtimeFlowTenSecondDirection | "STABLE";
}

/** The server's multi-window signal can point opposite to the latest 10 seconds. */
export function realtimeFlowSignalHeadline(
  state: MarketShareRealtimeFlowState | null | undefined,
  status: MarketShareRealtimeFlowStatus,
  tenSecondDirection: MarketShareRealtimeFlowState["direction"]
): string {
  if (status === "DEGRADED") {
    return "실시간 연결을 확인하는 중이며 새 판정은 보류합니다.";
  }
  if (tenSecondDirection === "UNKNOWN") {
    return "직전·최근 10초 비교 구간을 준비하는 중입니다.";
  }
  const labels = {
    UP: "상승",
    DOWN: "하락",
    STABLE: "안정",
    UNKNOWN: "판단 보류",
  };
  if (status === "ALERT" || status === "WATCH") {
    const direction = state?.direction;
    if (direction !== "UP" && direction !== "DOWN") {
      return "10·30·60초 종합 판정의 방향을 확인하는 중입니다.";
    }
    return status === "ALERT"
      ? `경고: 10·30·60초 중 2개 이상 구간에서 ${labels[direction]} 폭이 기준을 넘었습니다.`
      : `관찰: 10·30·60초 중 1개 구간에서 ${labels[direction]} 폭이 기준을 넘었습니다.`;
  }
  return tenSecondDirection === "STABLE"
    ? "현재 10초 흐름은 안정적이며 기준 초과 변화가 없습니다."
    : `현재 10초 흐름은 ${labels[tenSecondDirection]} 방향이지만 기준 초과는 아닙니다.`;
}

export type RealtimeFlowTenSecondEpisode = MarketShareRealtimeFlowEpisode & {
  direction: RealtimeFlowTenSecondDirection;
  status: "WATCH" | "ALERT";
};

export function realtimeFlowEpisodeStatusLabel(
  status: RealtimeFlowTenSecondEpisode["status"]
): "관찰" | "경고" {
  return status === "ALERT" ? "경고" : "관찰";
}

export function realtimeFlowEpisodeDirectionTone(
  direction: RealtimeFlowTenSecondEpisode["direction"]
): "profit" | "loss" {
  return direction === "UP" ? "profit" : "loss";
}

export function previewRealtimeFlowEpisodes<T>(
  episodes: readonly T[],
  expanded: boolean
): readonly T[] {
  return expanded
    ? episodes
    : episodes.slice(0, REALTIME_FLOW_EPISODE_PREVIEW_LIMIT);
}

export interface RealtimeFlowDataInterval {
  start_ms: number;
  end_ms: number;
  status: "MISSING";
}

export interface RealtimeFlowShareChartPoint {
  as_of_ms: number;
  current_share_pct: number;
  exchange_shares_pct?: Record<string, number>;
  segment_id: string;
}

export interface RealtimeFlowShareChartSeries {
  points: RealtimeFlowShareChartPoint[];
  total_sample_count: number;
  used_sample_count: number;
  excluded_sample_count: number;
}

/**
 * Selects the backend's live one-second share points without recalculating a
 * share in the browser. Missing or incomplete seconds start a new segment.
 */
export function buildRealtimeFlowOneSecondShareChartSeries(
  state: MarketShareRealtimeFlowState | null | undefined,
  fromMS: number,
  toMS: number
): RealtimeFlowShareChartSeries {
  const items = Array.isArray(state?.recent_share_1s)
    ? state.recent_share_1s
    : [];
  const totalSampleCount = items.length;
  if (
    state?.version !== REALTIME_FLOW_CONTRACT_VERSION ||
    !finite(fromMS) ||
    !finite(toMS) ||
    fromMS > toMS
  ) {
    return {
      points: [],
      total_sample_count: totalSampleCount,
      used_sample_count: 0,
      excluded_sample_count: totalSampleCount,
    };
  }
  const candidates = items
    .filter(
      point =>
        finite(point.as_of_ms) &&
        point.as_of_ms >= fromMS &&
        point.as_of_ms <= toMS &&
        point.complete === true &&
        point.status === "LIVE" &&
        finite(point.bithumb_share_1s_pct)
    )
    .map(point => {
      const exchangeShares = Object.fromEntries(
        Object.entries(point.exchange_shares_1s_pct ?? {}).filter(([, value]) =>
          finite(value)
        )
      ) as Record<string, number>;
      if (!finite(exchangeShares.bithumb)) {
        exchangeShares.bithumb = point.bithumb_share_1s_pct as number;
      }
      return {
        as_of_ms: point.as_of_ms,
        current_share_pct: point.bithumb_share_1s_pct as number,
        exchange_shares_pct: exchangeShares,
      };
    })
    .sort((left, right) => left.as_of_ms - right.as_of_ms);
  const seen = new Set<number>();
  const unique = candidates.filter(point => {
    if (seen.has(point.as_of_ms)) return false;
    seen.add(point.as_of_ms);
    return true;
  });
  let segmentStartMS: number | null = null;
  let previousMS: number | null = null;
  const points = unique.map(point => {
    if (
      previousMS === null ||
      point.as_of_ms - previousMS > REALTIME_FLOW_ONE_SECOND_MAX_GAP_MS
    ) {
      segmentStartMS = point.as_of_ms;
    }
    previousMS = point.as_of_ms;
    return { ...point, segment_id: `segment-${segmentStartMS}` };
  });
  return {
    points,
    total_sample_count: totalSampleCount,
    used_sample_count: points.length,
    excluded_sample_count: totalSampleCount - points.length,
  };
}

/**
 * Realtime-flow completeness is independent from the selected rolling window.
 * In particular, a partial 24h window must not hide a complete 10s WS frame.
 */
export function realtimeFlowTransportIsLive(
  tick:
    | Pick<
        MarketShareRealtimeTick,
        "ws_realtime_complete" | "ws_status" | "coverage_status"
      >
    | null
    | undefined,
  connection: string
): boolean {
  return Boolean(
    connection === "live" &&
    tick?.ws_realtime_complete &&
    tick.ws_status === "LIVE"
  );
}

function finite(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function qualityIsLive(state: MarketShareRealtimeFlowState): boolean {
  return (
    state.data_quality?.complete_frame === true &&
    state.data_quality?.frame_status === "LIVE"
  );
}

/** Values are diagnostic only when the state was built from a complete frame. */
export function realtimeFlowValuesAreVisible(
  state: MarketShareRealtimeFlowState | null | undefined
): state is MarketShareRealtimeFlowState {
  return Boolean(
    state &&
    state.version === REALTIME_FLOW_CONTRACT_VERSION &&
    DISPLAYABLE_STATUSES.has(state.status) &&
    qualityIsLive(state)
  );
}

export function realtimeFlowWindowValuesAreVisible(
  state: MarketShareRealtimeFlowState | null | undefined,
  window: MarketShareRealtimeFlowWindow | null | undefined
): window is MarketShareRealtimeFlowWindow {
  return Boolean(
    realtimeFlowValuesAreVisible(state) &&
    window?.status === "COMPLETE" &&
    finite(window.current_share_pct) &&
    finite(window.previous_share_pct) &&
    finite(window.share_delta_pp) &&
    finite(window.current_volume_krw) &&
    finite(window.previous_volume_krw)
  );
}

/**
 * A window with the share the server measured over the seconds it observed.
 * A window missing some seconds (an exchange dropped out) still has one: it
 * is shown as observed-range only (관측범위기준) rather than hidden, while
 * its change and drivers come only from complete windows.
 */
export function realtimeFlowWindowObserved(
  state: MarketShareRealtimeFlowState | null | undefined,
  window: MarketShareRealtimeFlowWindow | null | undefined
): window is MarketShareRealtimeFlowWindow {
  return Boolean(
    state &&
    state.version === REALTIME_FLOW_CONTRACT_VERSION &&
    finite(window?.current_share_pct)
  );
}

/** Summary 2 deliberately uses one comparable definition: latest 10s vs prior 10s. */
export function realtimeFlowTenSecondWindow(
  state: MarketShareRealtimeFlowState | null | undefined
): MarketShareRealtimeFlowWindow | null {
  const window = state?.windows?.find(item => item.duration_sec === 10);
  return realtimeFlowWindowValuesAreVisible(state, window) ? window : null;
}

export function realtimeFlowTenSecondSample(
  state: MarketShareRealtimeFlowState | null | undefined
): RealtimeFlowTenSecondSample | null {
  const window = realtimeFlowTenSecondWindow(state);
  if (!state || !window) return null;
  const delta = window.share_delta_pp as number;
  return {
    state,
    window,
    direction: delta > 0 ? "UP" : delta < 0 ? "DOWN" : "STABLE",
  };
}

/**
 * Selects server-stored, complete 10-second share observations for a chart.
 * Values are never recalculated here: each point preserves the window's
 * current_share_pct. Segment IDs prevent a chart from bridging a data gap.
 */
export function buildRealtimeFlowShareChartSeries(
  response: Pick<MarketShareRealtimeFlowHistoryResponse, "items">,
  fromMS: number,
  toMS: number
): RealtimeFlowShareChartSeries {
  const items = Array.isArray(response.items) ? response.items : [];
  const totalSampleCount = items.length;
  if (!finite(fromMS) || !finite(toMS) || fromMS > toMS) {
    return {
      points: [],
      total_sample_count: totalSampleCount,
      used_sample_count: 0,
      excluded_sample_count: totalSampleCount,
    };
  }

  const candidates = items
    .flatMap(state => {
      if (
        !finite(state?.as_of_ms) ||
        state.as_of_ms < fromMS ||
        state.as_of_ms > toMS
      ) {
        return [];
      }
      const window = realtimeFlowTenSecondWindow(state);
      if (!window) return [];
      return [
        {
          as_of_ms: state.as_of_ms,
          current_share_pct: window.current_share_pct as number,
        },
      ];
    })
    .sort((left, right) => left.as_of_ms - right.as_of_ms);

  const seen = new Set<number>();
  const unique = candidates.filter(point => {
    if (seen.has(point.as_of_ms)) return false;
    seen.add(point.as_of_ms);
    return true;
  });

  let segmentStartMS: number | null = null;
  let previousMS: number | null = null;
  const points = unique.map(point => {
    if (
      previousMS === null ||
      point.as_of_ms - previousMS > REALTIME_FLOW_EPISODE_MAX_GAP_MS
    ) {
      segmentStartMS = point.as_of_ms;
    }
    previousMS = point.as_of_ms;
    return {
      ...point,
      segment_id: `segment-${segmentStartMS}`,
    };
  });

  return {
    points,
    total_sample_count: totalSampleCount,
    used_sample_count: points.length,
    excluded_sample_count: totalSampleCount - points.length,
  };
}

/** Uses the dedicated 10s evidence, with a V2 fallback for older rows. */
export function realtimeFlowTenSecondAttribution(
  state: MarketShareRealtimeFlowState | null | undefined
): MarketShareRealtimeFlowAttribution | null {
  const attribution = state?.ten_second_attribution;
  if (
    attribution?.window_sec === 10 &&
    Array.isArray(attribution.drivers) &&
    Array.isArray(attribution.exchange_drivers)
  ) {
    return attribution;
  }
  if (!state || state.primary_window_sec !== 10) return null;
  return {
    window_sec: 10,
    stored_driver_count: state.stored_driver_count,
    total_driver_count: state.total_driver_count,
    residual_contribution_pp: state.residual_contribution_pp,
    drivers: state.drivers ?? [],
    exchange_drivers: state.exchange_drivers ?? [],
  };
}

function isServerTenSecondEpisode(
  episode: MarketShareRealtimeFlowEpisode
): episode is RealtimeFlowTenSecondEpisode {
  return (
    (episode.direction === "UP" || episode.direction === "DOWN") &&
    (episode.status === "WATCH" || episode.status === "ALERT")
  );
}

/** Filters the server's immutable episode projection without reclassifying it. */
export function selectRealtimeFlowEpisodes(
  summary: Pick<MarketShareRealtimeFlowPeriodSummary, "episodes"> | null,
  fromMS: number,
  toMS: number
): RealtimeFlowTenSecondEpisode[] {
  if (!summary || !finite(fromMS) || !finite(toMS) || fromMS > toMS) return [];
  return summary.episodes
    .filter(isServerTenSecondEpisode)
    .filter(episode => episode.end_ms >= fromMS && episode.start_ms <= toMS)
    .sort((left, right) => right.end_ms - left.end_ms);
}

/** Selects only server-declared missing coverage; the browser never infers gaps. */
export function selectRealtimeFlowMissingIntervals(
  summary: Pick<
    MarketShareRealtimeFlowPeriodSummary,
    "coverage_segments"
  > | null,
  fromMS: number,
  toMS: number
): RealtimeFlowDataInterval[] {
  if (!summary || !finite(fromMS) || !finite(toMS) || fromMS > toMS) return [];
  return summary.coverage_segments
    .filter(
      (segment: MarketShareRealtimeFlowCoverageSegment) =>
        segment.status === "MISSING" &&
        segment.end_ms > fromMS &&
        segment.start_ms < toMS
    )
    .map(segment => ({
      start_ms: Math.max(fromMS, segment.start_ms),
      end_ms: Math.min(toMS, segment.end_ms),
      status: "MISSING" as const,
    }))
    .sort((left, right) => right.end_ms - left.end_ms);
}

/** STALE, WAITING and unavailable order books never expose derived values. */
export function realtimeFlowOrderbookValuesAreVisible(
  orderbook: MarketShareRealtimeOrderbook | null | undefined
): orderbook is MarketShareRealtimeOrderbook {
  return orderbook?.status === "LIVE";
}

function compactQuality(
  source: Record<string, unknown> | null | undefined
): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  if (!source) return result;
  for (const key of QUALITY_KEYS) {
    if (key in source) result[key] = source[key];
  }
  return result;
}

function compactState(
  state: MarketShareRealtimeFlowState
): MarketShareRealtimeFlowState {
  const compactDrivers = (drivers: MarketShareRealtimeFlowDriver[]) =>
    drivers.slice(0, 8).map(driver => ({
      ...driver,
      exchange_impacts: Array.isArray(driver.exchange_impacts)
        ? driver.exchange_impacts.slice(0, 5)
        : [],
    }));
  return {
    ...state,
    windows: Array.isArray(state.windows) ? state.windows.slice(0, 3) : [],
    drivers: Array.isArray(state.drivers) ? compactDrivers(state.drivers) : [],
    exchange_drivers: Array.isArray(state.exchange_drivers)
      ? state.exchange_drivers.slice(0, 5)
      : [],
    ten_second_attribution: state.ten_second_attribution
      ? {
          ...state.ten_second_attribution,
          drivers: Array.isArray(state.ten_second_attribution.drivers)
            ? compactDrivers(state.ten_second_attribution.drivers)
            : [],
          exchange_drivers: Array.isArray(
            state.ten_second_attribution.exchange_drivers
          )
            ? state.ten_second_attribution.exchange_drivers.slice(0, 5)
            : [],
        }
      : state.ten_second_attribution,
    current_assets: Array.isArray(state.current_assets)
      ? state.current_assets.slice(0, 8)
      : [],
    data_quality: compactQuality(state.data_quality),
  };
}

/**
 * The endpoint returns newest-first observations. Re-sort and de-duplicate at
 * the boundary, then retain only a small, bounded diagnostic projection.
 */
export function compactRealtimeFlowHistory(
  response: Pick<MarketShareRealtimeFlowHistoryResponse, "items">,
  limit = REALTIME_FLOW_HISTORY_RENDER_LIMIT
): MarketShareRealtimeFlowState[] {
  const safeLimit = Math.max(
    1,
    Math.min(REALTIME_FLOW_HISTORY_RENDER_LIMIT, Math.trunc(limit) || 1)
  );
  const seen = new Set<number>();
  const items = Array.isArray(response.items) ? response.items : [];
  return [...items]
    .filter(
      state =>
        state?.version === REALTIME_FLOW_CONTRACT_VERSION &&
        finite(state.as_of_ms) &&
        state.as_of_ms > 0 &&
        KNOWN_STATUSES.has(state.status)
    )
    .sort((left, right) => right.as_of_ms - left.as_of_ms)
    .filter(state => {
      if (seen.has(state.as_of_ms)) return false;
      seen.add(state.as_of_ms);
      return true;
    })
    .slice(0, safeLimit)
    .map(compactState);
}

export function buildRealtimeFlowHistoryPath(
  filter: RealtimeFlowHistoryFilter = {}
): string {
  const query = new URLSearchParams();
  if (finite(filter.fromMS) && filter.fromMS >= 0)
    query.set("from_ms", String(Math.trunc(filter.fromMS)));
  if (finite(filter.toMS) && filter.toMS >= 0)
    query.set("to_ms", String(Math.trunc(filter.toMS)));
  if (filter.status && KNOWN_STATUSES.has(filter.status))
    query.set("status", filter.status);
  const asset = filter.asset?.trim().toUpperCase();
  if (asset) query.set("asset", asset.slice(0, 64));
  const exchange = filter.exchange?.trim().toLowerCase();
  if (exchange) query.set("exchange", exchange.slice(0, 64));
  const limit = finite(filter.limit)
    ? Math.max(1, Math.min(1_000, Math.trunc(filter.limit)))
    : REALTIME_FLOW_HISTORY_FETCH_LIMIT;
  query.set("limit", String(limit));
  return `/api/market-share/realtime-history?${query.toString()}`;
}

export function realtimeFlowHistoryRange(
  generatedAtSeconds: number,
  durationMS = REALTIME_FLOW_HISTORY_MS
): { fromMS: number; toMS: number } | null {
  if (!finite(generatedAtSeconds) || generatedAtSeconds <= 0) return null;
  const safeDurationMS = finite(durationMS)
    ? Math.max(10_000, Math.trunc(durationMS))
    : REALTIME_FLOW_HISTORY_MS;
  const toMS = Math.floor((generatedAtSeconds * 1_000) / 10_000) * 10_000;
  return { fromMS: Math.max(0, toMS - safeDurationMS), toMS };
}

export function realtimeFlowHistoryRequest(
  retained: readonly MarketShareRealtimeFlowState[],
  range: { fromMS: number; toMS: number },
  updatedAtMS: number
): RealtimeFlowHistoryFilter & { incremental: boolean } {
  const newestMS = retained.reduce(
    (latest, item) =>
      finite(item?.as_of_ms) ? Math.max(latest, item.as_of_ms) : latest,
    0
  );
  const updateLagMS = range.toMS - updatedAtMS;
  const incremental =
    retained.length > 0 &&
    newestMS >= range.fromMS &&
    newestMS <= range.toMS &&
    finite(updatedAtMS) &&
    updatedAtMS > 0 &&
    updateLagMS >= 0 &&
    updateLagMS <= 30_000;
  if (!incremental) {
    return {
      fromMS: range.fromMS,
      toMS: range.toMS,
      limit: REALTIME_FLOW_HISTORY_FETCH_LIMIT,
      incremental: false,
    };
  }
  const missingSlots = Math.max(
    1,
    Math.ceil((range.toMS - newestMS) / 10_000) + 2
  );
  return {
    // Re-fetch the boundary row when the clock has not advanced yet.
    // The API range is inclusive; newestMS + 1 would exceed toMS.
    fromMS: Math.min(newestMS + 1, range.toMS),
    toMS: range.toMS,
    limit: Math.min(REALTIME_FLOW_HISTORY_FETCH_LIMIT, missingSlots),
    incremental: true,
  };
}

export function mergeRealtimeFlowHistory(
  response: Pick<MarketShareRealtimeFlowHistoryResponse, "items">,
  retained: readonly MarketShareRealtimeFlowState[],
  range: { fromMS: number; toMS: number },
  incremental: boolean
): MarketShareRealtimeFlowState[] {
  const incoming = Array.isArray(response.items) ? response.items : [];
  const items = incremental ? [...incoming, ...retained] : incoming;
  return compactRealtimeFlowHistory({
    items: items.filter(
      item => item.as_of_ms >= range.fromMS && item.as_of_ms <= range.toMS
    ),
  });
}
