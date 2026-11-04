import {
  indexDaySource,
  normalizeDomesticGrid,
  normalizeIndexAvailability,
} from "@/lib/index-day";
import {
  buildRealtimeFlowHistoryPath,
  type RealtimeFlowHistoryFilter,
} from "@/lib/market-share-realtime-flow";
import {
  buildMarketRegistryHistoryPath,
  MARKET_REGISTRY_HISTORY_LIMIT,
  normalizeMarketRegistryHistory,
} from "@/lib/market-registry-history";
import {
  buildOfficialAlignedContributionPath,
  buildOfficialAlignedHistoryPath,
} from "@/lib/market-share-official-history";
import type { MarketShareNetworkWindowSec } from "@/lib/market-share-config";
import {
  noticeArchivePath,
  normalizeNoticeArchivePage,
  normalizeNoticeHeadlinePage,
  type NoticeArchivePage,
  type NoticeArchiveRequest,
  type NoticeHeadline,
} from "@/lib/notice-archive";
import type {
  MarketRegistryHistoryResponse,
  MarketScope,
  MarketShareClosedSeconds,
  MarketShareSeries,
  MarketShareSeriesBucketSec,
  MarketShareHermesNoticeQuestionAnswer,
  GroundedMarketQuestionResponse,
  GroundedMarketQuestionConversationScope,
  GroundedMarketQuestionStageEvent,
  MarketShareActiveNoticeNetwork,
  MarketShareRealtimeFlowHistoryResponse,
  MarketShareRealtimeFlowPeriodSummary,
  MarketShareOfficialAlignedHistoryResponse,
  MarketShareOfficialAlignedContribution,
  MarketShareAnalysis,
  MarketShareAsset,
  MarketShareRollingNotice,
  MarketShareWindowChanges,
  NoticeQuestionPersona,
} from "@/lib/market-share-types";
import { readQuestionRequestError } from "./question-request";
import { fetchRead } from "./http-read";
import {
  isGroundedMarketQuestionResponse,
  readGroundedMarketQuestionStageStream,
  type GroundedMarketQuestionStreamResult,
} from "./market-question-stream";

const configuredAPIBase = (
  import.meta.env.VITE_API_URL as string | undefined
)?.replace(/\/$/, "");

export const MAX_MARKET_QUESTION_HISTORY_TURNS = 15;

export async function getNoticeArchive(
  request: NoticeArchiveRequest & { fields: "headline" },
  signal?: AbortSignal
): Promise<NoticeArchivePage<NoticeHeadline>>;
export async function getNoticeArchive(
  request: NoticeArchiveRequest,
  signal?: AbortSignal
): Promise<NoticeArchivePage>;
export async function getNoticeArchive(
  request: NoticeArchiveRequest,
  signal?: AbortSignal
) {
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}${noticeArchivePath(request)}`,
    { signal, headers: { Accept: "application/json" } }
  );
  if (response.status === 409) throw new Error("NOTICE_ARCHIVE_CHANGED");
  if (!response.ok)
    throw new Error(`Notice archive request failed (${response.status})`);
  const page: unknown = await response.json();
  return request.fields === "headline"
    ? normalizeNoticeHeadlinePage(page)
    : normalizeNoticeArchivePage(page);
}

export interface MarketQuestionConversationTurn {
  question: string;
  answer: string;
}

export type MarketQuestionFeedbackVerdict =
  | "CORRECT"
  | "WRONG_PERIOD"
  | "WRONG_TARGET"
  | "WRONG_METRIC"
  | "WRONG_ANALYSIS";

// Which once-a-second realtime flow a screen reads: the whole flow; the flow
// without its one-second share series; the flow as the main screen's live
// strip and window cards read it ("strip": its windows, status and leading
// coin only; a server without it sends the whole flow); or none (screens that
// never read it).
export type MarketShareRealtimeFlowStream =
  | "full"
  | "compact"
  | "strip"
  | "none";

export function getMarketShareWebSocketUrl(
  windowSec: number,
  notionalKrw: number | null,
  marketScope: MarketScope,
  view: string,
  realtimeFlow: MarketShareRealtimeFlowStream = "full",
  // The coins' live shares, sent every second (코인 인덱스 2).
  coinShares = false
): string {
  const base = (configuredAPIBase || window.location.origin).replace(
    /^http/,
    "ws"
  );
  const query = new URLSearchParams({
    window: String(windowSec),
    market_scope: marketScope,
    view: view === "brief" ? "overview" : view === "shell" ? "events" : view,
  });
  if (view === "brief") {
    // The main screen's briefing: the overview with only what it reads. A
    // server without this projection sends the whole compact overview.
    query.set("projection", "dashboard_brief_v1");
  } else if (view === "shell" || view === "drivers") {
    query.set("projection", "dashboard_screen_v1");
  } else if (
    ["events", "summary", "overview", "summary2", "full"].includes(view)
  ) {
    query.set("projection", "dashboard_v1");
  } else if (view === "quality") {
    // Only the fields these screens read (MarketShareDriversAnalysis); a
    // server without this projection sends the whole analysis instead.
    query.set("projection", "dashboard_detail_v1");
  }
  if (realtimeFlow !== "full") query.set("realtime_flow", realtimeFlow);
  // The notice workspace consumes analysis.notices. Requesting the legacy
  // warmup snapshot as well sends the same items twice on every new version.
  if (view === "events") query.set("notice_snapshot", "none");
  if (coinShares) query.set("coin_shares", "live");
  if (notionalKrw !== null) query.set("notional_krw", String(notionalKrw));
  return `${base}/api/ws/market-share?${query}`;
}

export async function askGroundedMarketQuestion(
  question: string,
  history: MarketQuestionConversationTurn[] = [],
  signal?: AbortSignal
): Promise<GroundedMarketQuestionResponse> {
  const response = await fetch(
    `${configuredAPIBase ?? ""}/api/market-share/grounded-question`,
    {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        question,
        history: history.slice(-MAX_MARKET_QUESTION_HISTORY_TURNS),
      }),
      signal,
    }
  );
  if (!response.ok) {
    throw await readQuestionRequestError(response);
  }
  const payload = (await response.json()) as GroundedMarketQuestionResponse;
  if (!isGroundedMarketQuestionResponse(payload)) {
    throw new Error("Grounded question response is invalid");
  }
  return payload;
}

// A calculator without the staged route answers every attempt the same way,
// and nginx charges both the probe and the fallback to one question budget.
// Remember the verdict so only the first question of a session pays twice.
let stagedQuestionStreamUnavailable = false;

export async function askGroundedMarketQuestionStream(
  question: string,
  history: MarketQuestionConversationTurn[] = [],
  conversationScope: GroundedMarketQuestionConversationScope | null = null,
  onEvent: (event: GroundedMarketQuestionStageEvent) => void,
  signal?: AbortSignal
): Promise<GroundedMarketQuestionStreamResult & { legacy: boolean }> {
  if (stagedQuestionStreamUnavailable) {
    const legacy = await askGroundedMarketQuestion(question, history, signal);
    return { response: legacy, status: "ACCEPTED", legacy: true };
  }
  const body = JSON.stringify({
    question,
    history: history.slice(-MAX_MARKET_QUESTION_HISTORY_TURNS),
    ...(conversationScope ? { conversation_scope: conversationScope } : {}),
  });
  const response = await fetch(
    `${configuredAPIBase ?? ""}/api/market-share/grounded-question/stream`,
    {
      method: "POST",
      headers: {
        Accept: "application/x-ndjson",
        "Content-Type": "application/json",
      },
      body,
      signal,
    }
  );
  if (response.status === 404 || response.status === 405) {
    stagedQuestionStreamUnavailable = true;
    if (!signal?.aborted) {
      const legacy = await askGroundedMarketQuestion(question, history, signal);
      return { response: legacy, status: "ACCEPTED", legacy: true };
    }
  }
  if (!response.ok) throw await readQuestionRequestError(response);
  const result = await readGroundedMarketQuestionStageStream(response, onEvent);
  return { ...result, legacy: false };
}

export async function submitMarketQuestionFeedback(
  requestId: string,
  verdict: MarketQuestionFeedbackVerdict,
  correctedQuestion = ""
): Promise<void> {
  const response = await fetch(
    `${configuredAPIBase ?? ""}/api/market-share/grounded-question-feedback`,
    {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        request_id: requestId,
        verdict,
        ...(correctedQuestion.trim()
          ? { corrected_question: correctedQuestion.trim() }
          : {}),
      }),
    }
  );
  if (!response.ok) {
    throw new Error(`Market question feedback failed (${response.status})`);
  }
}

export async function askHermesNoticeQuestion(
  persona: NoticeQuestionPersona,
  question: string,
  contextSourceIDs: string[] = [],
  signal?: AbortSignal
): Promise<MarketShareHermesNoticeQuestionAnswer> {
  const response = await fetch(
    `${configuredAPIBase ?? ""}/api/market-share/hermes-notice-question`,
    {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        persona,
        question,
        // Deterministic aggregate answers can expose up to 50 server-verified
        // official notices. Preserve that verified set for a follow-up; the
        // backend still ranks and bounds the rows sent to the language model.
        context_source_ids: contextSourceIDs.slice(0, 50),
      }),
      signal,
    }
  );
  if (!response.ok) {
    throw await readQuestionRequestError(response);
  }
  const payload =
    (await response.json()) as MarketShareHermesNoticeQuestionAnswer;
  if (
    typeof payload?.answer !== "string" ||
    !Array.isArray(payload.key_points) ||
    typeof payload.caveat !== "string" ||
    !Array.isArray(payload.sources) ||
    typeof payload.persona !== "string"
  ) {
    throw new Error("Hermes notice question response is invalid");
  }
  return payload;
}

export async function getMarketShareRealtimeHistory(
  filter: RealtimeFlowHistoryFilter = {},
  signal?: AbortSignal
): Promise<MarketShareRealtimeFlowHistoryResponse> {
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}${buildRealtimeFlowHistoryPath(filter)}`,
    {
      method: "GET",
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw new Error(
      `Realtime-flow history request failed (${response.status})`
    );
  }
  const payload =
    (await response.json()) as MarketShareRealtimeFlowHistoryResponse;
  if (!Array.isArray(payload?.items)) {
    throw new Error("Realtime-flow history response is invalid");
  }
  return payload;
}

export interface IndexFilterAsset {
  asset: string;
  listed_on_bithumb: boolean;
  exclusive_on_bithumb: boolean;
  shared_bithumb_upbit: boolean;
}

export interface IndexFiltersResponse {
  contract_version: string;
  window_sec: number;
  market_scope: string;
  generated_at: number;
  assets: IndexFilterAsset[];
  notice_assets: string[];
}

// The index screen needs each asset's listing shape and the assets named by
// notices in the window. Reading the whole analysis for that transferred
// megabytes of liquidity and rolling evidence the screen discards.
export async function getIndexFilters(
  signal?: AbortSignal
): Promise<IndexFiltersResponse> {
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}/api/market-share/index-filters?window=86400&market_scope=ALL_DOMESTIC_CONVERTED`,
    { signal, headers: { Accept: "application/json" } }
  );
  if (!response.ok)
    throw new Error("상장·공지 필터 자료를 불러오지 못했습니다.");
  const payload = (await response.json()) as IndexFiltersResponse;
  if (
    payload?.contract_version !== "INDEX_FILTERS_V1" ||
    !Array.isArray(payload.assets) ||
    !Array.isArray(payload.notice_assets)
  )
    throw new Error("상장·공지 필터 자료를 확인하지 못했습니다.");
  return payload;
}

export async function getIndexDay(
  day: string,
  signal?: AbortSignal,
  filter?: { assets: string; quote: string; venue: string },
  source: "hourly_rolling" | "ticker_24h" | undefined = indexDaySource(day)
): Promise<import("./index-day").IndexDayResponse> {
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}/api/market-share/index-day?day=${encodeURIComponent(day)}${filter ? `&${new URLSearchParams(filter)}` : ""}${source ? `&source=${source}` : ""}`,
    { signal, headers: { Accept: "application/json" } }
  );
  if (!response.ok && (import.meta.env?.VITE_PORTABLE_PYTHON === "true" || import.meta.env?.VITE_PORTABLE_POWERSHELL === "true")) {
    throw new Error("로컬 수집 자료를 불러오지 못했습니다. 실행 창과 logs/runtime.log를 확인해 주세요.");
  }
  if (!response.ok)
    throw new Error(
      response.status === 404
        ? "날짜별 시간봉 조회 기능이 서버에 아직 반영되지 않았습니다."
        : "시간별 자료를 불러오지 못했습니다. 다시 시도해 주세요."
    );
  const payload = await response.json();
  if (
    payload?.contract_version !== (source === "ticker_24h" ? "INDEX_TICKER_V1" : "INDEX_DAY_HOURLY_V1") ||
    payload.day !== day ||
    !Array.isArray(payload.rows) ||
    !Array.isArray(payload.exchanges)
  )
    throw new Error("선택한 날짜의 자료를 확인할 수 없습니다.");
  return normalizeIndexAvailability(payload);
}

export async function getIndexGrid(
  day: string,
  signal: AbortSignal,
  filter: { assets: string; quote: string; venue: string },
  source: "hourly_rolling" | "ticker_24h" | undefined = indexDaySource(day)
) {
  const query = new URLSearchParams({
    day,
    ...filter,
    projection: "domestic_grid",
  });
  if (source) query.set("source", source);
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}/api/market-share/index-day?${query}`,
    {
      signal,
      headers: { Accept: "application/json" },
    }
  );
  if (!response.ok) throw new Error("필터 합계를 불러오지 못했습니다.");
  const payload = await response.json();
  // A previous server may ignore the additive projection parameter. Its full
  // response contains the same grid and remains usable during a rollout.
  if (
    !["INDEX_DAY_GRID_V1", "INDEX_DAY_HOURLY_V1"].includes(
      payload?.contract_version
    ) ||
    payload.day !== day ||
    !payload.domestic?.totals ||
    !Array.isArray(payload.domestic.coins)
  ) {
    throw new Error("선택한 날짜의 집계 자료를 확인할 수 없습니다.");
  }
  return { domestic: normalizeDomesticGrid(payload.domestic) };
}

export async function getOfficialAlignedHistory(
  fromMS: number,
  toMS: number,
  signal?: AbortSignal
): Promise<MarketShareOfficialAlignedHistoryResponse> {
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}${buildOfficialAlignedHistoryPath(fromMS, toMS)}`,
    { method: "GET", headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw new Error(
      `Official aligned history request failed (${response.status})`
    );
  }
  const payload =
    (await response.json()) as MarketShareOfficialAlignedHistoryResponse;
  if (
    payload?.contract_version !== "OFFICIAL_ALIGNED_1H_HISTORY_V1" ||
    payload.interval_sec !== 3600 ||
    !Array.isArray(payload.items)
  ) {
    throw new Error("Official aligned history response is invalid");
  }
  return payload;
}

export async function getOfficialAlignedContribution(
  toMS: number,
  signal?: AbortSignal
): Promise<MarketShareOfficialAlignedContribution> {
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}${buildOfficialAlignedContributionPath(toMS)}`,
    { method: "GET", headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw new Error(
      `Official aligned contribution request failed (${response.status})`
    );
  }
  const payload =
    (await response.json()) as MarketShareOfficialAlignedContribution;
  if (
    typeof payload?.status !== "string" ||
    !Array.isArray(payload.asset_drivers) ||
    !Array.isArray(payload.exchange_drivers)
  ) {
    throw new Error("Official aligned contribution response is invalid");
  }
  return payload;
}

// The day holds about 5,000 episodes (7 MB of JSON). Screens show the last
// minutes or the latest few, so they ask the server for only those episodes;
// totals, drivers and coverage still describe the whole 24 hours.
export async function getMarketShareRealtimeSummary(
  toMS: number,
  signal?: AbortSignal,
  episodes?: { fromMS?: number; limit?: number }
): Promise<MarketShareRealtimeFlowPeriodSummary> {
  const query = new URLSearchParams({
    to_ms: String(Math.floor(toMS / 10_000) * 10_000),
    window_sec: "86400",
  });
  if (episodes?.fromMS && episodes.fromMS > 0)
    query.set("episodes_from_ms", String(Math.floor(episodes.fromMS)));
  if (episodes?.limit && episodes.limit > 0)
    query.set("episode_limit", String(Math.floor(episodes.limit)));
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}/api/market-share/realtime-summary?${query}`,
    { method: "GET", headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw new Error(
      `Realtime-flow summary request failed (${response.status})`
    );
  }
  const payload =
    (await response.json()) as MarketShareRealtimeFlowPeriodSummary;
  if (
    payload?.contract_version !== "REALTIME_FLOW_V2" ||
    !Array.isArray(payload.drivers)
  ) {
    throw new Error("Realtime-flow summary response is invalid");
  }
  return payload;
}

/** Every rolling window compared with the one before it (cached ~5 s). */
export async function getWindowChanges(
  marketScope: MarketScope,
  signal?: AbortSignal
): Promise<MarketShareWindowChanges> {
  const query = new URLSearchParams({ market_scope: marketScope });
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}/api/market-share/window-changes?${query}`,
    { method: "GET", headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw new Error(`Window changes request failed (${response.status})`);
  }
  const payload = (await response.json()) as MarketShareWindowChanges;
  if (!Array.isArray(payload?.windows)) {
    throw new Error("Window changes response is invalid");
  }
  return payload;
}

/** The live window ending at each of the last 120 closed seconds (10, 30 or 60 s). */
export async function getClosedSeconds(
  windowSec: number,
  marketScope: MarketScope,
  signal?: AbortSignal
): Promise<MarketShareClosedSeconds> {
  const query = new URLSearchParams({
    window: String(windowSec),
    market_scope: marketScope,
  });
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}/api/market-share/closed-seconds?${query}`,
    { method: "GET", headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw new Error(`Closed seconds request failed (${response.status})`);
  }
  const payload = (await response.json()) as MarketShareClosedSeconds;
  if (!Array.isArray(payload?.seconds)) {
    throw new Error("Closed seconds response is invalid");
  }
  return payload;
}

/** Bithumb's share in each recent minute or ten minutes (direct observations). */
export async function getShareSeries(
  bucketSec: MarketShareSeriesBucketSec,
  marketScope: MarketScope,
  signal?: AbortSignal
): Promise<MarketShareSeries> {
  const query = new URLSearchParams({
    bucket_sec: String(bucketSec),
    market_scope: marketScope,
  });
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}/api/market-share/share-series?${query}`,
    { method: "GET", headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw new Error(`Share series request failed (${response.status})`);
  }
  const payload = (await response.json()) as MarketShareSeries;
  if (!Array.isArray(payload?.points)) {
    throw new Error("Share series response is invalid");
  }
  return payload;
}

export async function getActiveNoticeNetwork(
  windowSec: MarketShareNetworkWindowSec,
  signal?: AbortSignal
): Promise<MarketShareActiveNoticeNetwork> {
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}/api/market-share/active-notice-network?window_sec=${windowSec}`,
    { method: "GET", headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw new Error(
      `Active notice network request failed (${response.status})`
    );
  }
  const payload = (await response.json()) as MarketShareActiveNoticeNetwork;
  if (
    payload?.version !== "ACTIVE_NOTICE_SHARE_NETWORK_V1" ||
    !Array.isArray(payload.active_notices) ||
    !Array.isArray(payload.assets) ||
    !Array.isArray(payload.edges)
  ) {
    throw new Error("Active notice network response is invalid");
  }
  return payload;
}

export async function getMarketRegistryHistory(
  signal?: AbortSignal
): Promise<MarketRegistryHistoryResponse> {
  const response = await fetchRead(
    `${configuredAPIBase ?? ""}${buildMarketRegistryHistoryPath(MARKET_REGISTRY_HISTORY_LIMIT)}`,
    {
      method: "GET",
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw new Error(
      `Market registry history request failed (${response.status})`
    );
  }
  return normalizeMarketRegistryHistory(await response.json());
}

/** The fields of the 10-minute KRW analysis the notice portal reads. */
export interface NoticePortalData {
  data_source?: "PORTABLE_OBSERVED_KRW_TRADES";
  assets: Array<{
    asset: string;
    domestic_volume_krw: number | null;
    domestic_trade_count: number;
    domestic_base_quantity: number | null;
  }>;
  context: Pick<
    MarketShareAnalysis["context"],
    | "selected_window_complete"
    | "selected_window_coverage_sec"
    | "selected_window_required_sec"
    | "current_interval_start"
    | "current_interval_end"
  >;
  rolling_24h: {
    as_of_ms: number;
    evidence: { domestic_notices: MarketShareRollingNotice[] };
  };
}

export async function getNoticePortal(
  signal?: AbortSignal
): Promise<NoticePortalData> {
  const base = configuredAPIBase ?? "";
  const init = { signal, headers: { Accept: "application/json" } };
  // The calculator's small projection (a few percent of the full analysis). A
  // calculator released before it answers 404; read the full analysis then.
  let response = await fetchRead(
    `${base}/api/market-share/notice-portal`,
    init
  );
  if (response.status === 404)
    response = await fetchRead(
      `${base}/api/market-share/analysis?window=600&market_scope=KRW`,
      init
    );
  if (!response.ok)
    throw new Error(`Notice portal request failed (${response.status})`);
  const value = (await response.json()) as NoticePortalData;
  if (
    !Array.isArray(value.assets) ||
    !value.context ||
    !Array.isArray(value.rolling_24h?.evidence?.domestic_notices)
  )
    throw new Error("Notice portal response incomplete");
  return {
    data_source: value.data_source,
    assets: value.assets,
    context: value.context,
    rolling_24h: value.rolling_24h,
  };
}

export async function getRealtimeIndex(windowSec: number, signal: AbortSignal, filter?: {assets: string; quote: string; venue: string}): Promise<import("./index-day").IndexDayResponse> {
  const query = new URLSearchParams({window: String(windowSec), ...(filter ?? {})});
  const response = await fetchRead(`${configuredAPIBase ?? ""}/api/market-share/realtime-index?${query}`, {signal, headers:{Accept:"application/json"}}, 4000);
  if (!response.ok) throw new Error("실시간 코인 자료를 불러오지 못했습니다.");
  const payload = await response.json();
  if (payload?.contract_version !== "REALTIME_INDEX_V1" || payload.window_sec !== windowSec || !Number.isFinite(payload.as_of_second) || !payload.domestic?.totals || !Array.isArray(payload.domestic.coins))
    throw new Error("실시간 코인 자료 형식을 확인하지 못했습니다.");
  return normalizeIndexAvailability(payload);
}
