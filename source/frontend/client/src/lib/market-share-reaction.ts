import type {
  ExchangeNotice,
  MarketShareReactionEvent,
  MarketShareReactionSummary,
} from "./market-share-types";
import { formatNoticeTime } from "./notice-time.ts";

const DOMESTIC_NOTICE_EXCHANGES = new Set([
  "bithumb",
  "upbit",
  "coinone",
  "korbit",
  "gopax",
]);
const SLIPPAGE_NOTICE_EXCHANGES = ["bithumb", "upbit"] as const;
const NOTICE_QUOTE_ASSETS = new Set(["KRW", "BTC", "USDT"]);
const RELEVANT_REACTION_STUDY_STATUSES = new Set([
  "MATURE",
  "LIVE_ESTIMATE",
  "INCOMPLETE_CONTIGUOUS_COVERAGE",
  "INCOMPLETE_FX_CONVERSION",
]);

export const CURRENT_WINDOW_CONTRIBUTION_SCOPE =
  "CURRENT_SELECTED_WINDOW_NOT_EVENT_CAUSAL" as const;

const EMPTY_NOTICE_ITEMS: ExchangeNotice[] = [];

function noticeTitleMentionsAsset(title: string, asset: string): boolean {
  const escaped = asset.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`(^|[^A-Z0-9])${escaped}([^A-Z0-9]|$)`, "i").test(title);
}

/**
 * Dashboard projections may intentionally omit the notice bundle. Treat that
 * projection shape as an empty list instead of letting optional context crash
 * unrelated charts.
 */
export function marketShareNoticeItems(
  notices: { items?: unknown } | null | undefined
): ExchangeNotice[] {
  if (!Array.isArray(notices?.items)) return EMPTY_NOTICE_ITEMS;
  return notices.items.filter(
    (item): item is ExchangeNotice => item !== null && typeof item === "object"
  );
}

type NoticeListChronology = Pick<
  ExchangeNotice,
  | "exchange"
  | "notice_id"
  | "published_ts_ms"
  | "detected_ts_ms"
  | "updated_ts_ms"
>;

function validNoticeTime(value: number): number {
  return Number.isFinite(value) && value > 0 ? value : 0;
}

/**
 * Sort notice lists by the official publication time, newest first.
 * Collection time is only a fallback, so a late analysis update cannot
 * promote an older notice above a newly published one.
 */
export function sortNoticesNewestFirst<T extends NoticeListChronology>(
  notices: readonly T[]
): T[] {
  return [...notices].sort((left, right) => {
    const leftPublished = validNoticeTime(left.published_ts_ms);
    const rightPublished = validNoticeTime(right.published_ts_ms);
    const leftPrimary = leftPublished || validNoticeTime(left.detected_ts_ms);
    const rightPrimary =
      rightPublished || validNoticeTime(right.detected_ts_ms);
    return (
      rightPrimary - leftPrimary ||
      validNoticeTime(right.detected_ts_ms) -
        validNoticeTime(left.detected_ts_ms) ||
      validNoticeTime(right.updated_ts_ms) -
        validNoticeTime(left.updated_ts_ms) ||
      left.exchange.localeCompare(right.exchange) ||
      left.notice_id.localeCompare(right.notice_id)
    );
  });
}

/**
 * Index the newest official Bithumb and Upbit notice for each explicitly
 * extracted asset. Keeping one row per venue makes the slippage table compact
 * while preserving both domestic comparison venues when both published news.
 */
export function latestSlippageNoticesByAsset(
  notices: readonly ExchangeNotice[]
): Map<string, ExchangeNotice[]> {
  const latestByAsset = new Map<string, Map<string, ExchangeNotice>>();
  for (const notice of sortNoticesNewestFirst(notices)) {
    const exchange = String(notice.exchange ?? "")
      .trim()
      .toLowerCase();
    if (
      !SLIPPAGE_NOTICE_EXCHANGES.includes(
        exchange as (typeof SLIPPAGE_NOTICE_EXCHANGES)[number]
      ) ||
      !String(notice.title ?? "").trim()
    ) {
      continue;
    }
    const extractedAssets = Array.from(
      new Set(
        (notice.assets ?? [])
          .map(asset => String(asset).trim().toUpperCase())
          .filter(Boolean)
      )
    );
    // Listing/caution bodies often contain every market pair even when the
    // official title names only INJ. A BTC/USDT/KRW quote currency therefore
    // counts only when it is the sole asset or is explicitly in the title.
    const assets = extractedAssets.filter(
      asset =>
        extractedAssets.length === 1 ||
        !NOTICE_QUOTE_ASSETS.has(asset) ||
        noticeTitleMentionsAsset(notice.title, asset)
    );
    for (const asset of assets) {
      const byExchange = latestByAsset.get(asset) ?? new Map();
      if (!byExchange.has(exchange)) byExchange.set(exchange, notice);
      latestByAsset.set(asset, byExchange);
    }
  }

  return new Map(
    Array.from(latestByAsset, ([asset, byExchange]) => [
      asset,
      SLIPPAGE_NOTICE_EXCHANGES.flatMap(exchange => {
        const notice = byExchange.get(exchange);
        return notice ? [notice] : [];
      }),
    ])
  );
}

export function eventCurrentWindowContribution(
  event: Pick<
    MarketShareReactionEvent,
    "contribution_scope" | "linked_asset_current_window_contribution_pp"
  >
): number | null {
  const value = event.linked_asset_current_window_contribution_pp;
  return event.contribution_scope === CURRENT_WINDOW_CONTRIBUTION_SCOPE &&
    value !== null &&
    Number.isFinite(value)
    ? value
    : null;
}

export function isNoticeRelevantToSelectedWindow(
  notice: Pick<ExchangeNotice, "within_selected_window" | "reaction_studies">
): boolean {
  return notice.within_selected_window;
}

/**
 * Keep the Overview context panel limited to domestic, linked evidence.
 * A time overlap alone is not enough to call a notice relevant here.
 */
export function isRelevantDomesticNotice(
  notice: Pick<
    ExchangeNotice,
    | "exchange"
    | "within_selected_window"
    | "reaction_studies"
    | "market_context"
  >
): boolean {
  const exchange = String(notice.exchange ?? "")
    .trim()
    .toLowerCase();
  if (
    !DOMESTIC_NOTICE_EXCHANGES.has(exchange) ||
    !notice.within_selected_window
  ) {
    return false;
  }
  return (
    (notice.market_context ?? []).length > 0 ||
    (notice.reaction_studies ?? []).some(study =>
      RELEVANT_REACTION_STUDY_STATUSES.has(study.status)
    )
  );
}

export function noticeAnchorTimeMs(
  notice: Pick<
    ExchangeNotice,
    "published_ts_ms" | "detected_ts_ms" | "timestamp_precision" | "schedules"
  >
): number | null {
  const anchors = noticeAnchorTimesMs(notice);
  return anchors.length > 0 ? Math.max(...anchors) : null;
}

export function noticeAnchorTimesMs(
  notice: Pick<
    ExchangeNotice,
    "published_ts_ms" | "detected_ts_ms" | "timestamp_precision" | "schedules"
  >
): number[] {
  const anchors: number[] = [];
  if (notice.timestamp_precision === "SECOND" && notice.published_ts_ms > 0) {
    anchors.push(notice.published_ts_ms);
  }
  const scheduleStarts = notice.schedules
    .flatMap(schedule => [schedule.start_ts_ms, schedule.end_ts_ms])
    .filter(value => Number.isFinite(value) && value > 0);
  anchors.push(...scheduleStarts);
  if (
    notice.timestamp_precision === "DETECTED" &&
    scheduleStarts.length === 0 &&
    notice.detected_ts_ms > 0
  ) {
    anchors.push(notice.detected_ts_ms);
  }
  return anchors;
}

export function noticeTimestampLabel(
  notice: Pick<
    ExchangeNotice,
    | "published_ts_ms"
    | "updated_ts_ms"
    | "detected_ts_ms"
    | "timestamp_precision"
  >
): string {
  const formatDateTime = (value: number): string => formatNoticeTime(value);
  const updatedLabel =
    notice.updated_ts_ms > 0 &&
    notice.published_ts_ms > 0 &&
    notice.updated_ts_ms > notice.published_ts_ms
      ? ` · 수정 ${formatDateTime(notice.updated_ts_ms)}`
      : "";
  if (notice.timestamp_precision === "SECOND" && notice.published_ts_ms > 0) {
    return `게시 ${formatDateTime(notice.published_ts_ms)}${updatedLabel}`;
  }
  if (notice.timestamp_precision === "DAY" && notice.published_ts_ms > 0) {
    return `게시 ${formatNoticeTime(notice.published_ts_ms, true)} · 일 단위${updatedLabel}`;
  }
  if (notice.timestamp_precision === "DETECTED" && notice.detected_ts_ms > 0) {
    return `감지 ${formatDateTime(notice.detected_ts_ms)} · 게시 시각 정보 없음`;
  }
  return "시각 정보 없음";
}

export function noticeTimestampLines(
  notice: Pick<
    ExchangeNotice,
    | "published_ts_ms"
    | "updated_ts_ms"
    | "detected_ts_ms"
    | "timestamp_precision"
  >
): string[] {
  const label = noticeTimestampLabel(notice);
  const separator = " · 수정 ";
  const separatorIndex = label.indexOf(separator);
  if (separatorIndex < 0) return [label];
  return [
    label.slice(0, separatorIndex),
    `수정 ${label.slice(separatorIndex + separator.length)}`,
  ];
}

export function noticeAssetLabels(
  event: Pick<MarketShareReactionEvent, "affected_assets" | "linked_assets">
): string[] {
  const affected = Array.from(
    new Set((event.affected_assets ?? []).filter(Boolean))
  );
  if (affected.length > 0) return affected;
  return Array.from(
    new Set(
      (event.linked_assets ?? []).map(asset => asset.asset).filter(Boolean)
    )
  );
}

export function observedExchangeReactionCount(
  event: Pick<MarketShareReactionEvent, "exchange_reactions">
): number {
  return event.exchange_reactions.filter(row => row.data_status === "OBSERVED")
    .length;
}

export function exchangeNoticeAssetLabels(
  notice: Pick<ExchangeNotice, "assets" | "market_context">
): string[] {
  const official = Array.from(new Set((notice.assets ?? []).filter(Boolean)));
  if (official.length > 0) return official;
  return Array.from(
    new Set(
      (notice.market_context ?? [])
        .map(context => context.asset)
        .filter(Boolean)
    )
  );
}

export function reactionOriginTimingAnchor(
  event: Pick<MarketShareReactionEvent, "origin">
): {
  time: number | null;
  shortLabel: string;
  longLabel: string;
} {
  const basis = event.origin.event_time_basis;
  if (basis === "EFFECTIVE" && event.origin.event_time_ms !== null) {
    return {
      time: event.origin.event_time_ms,
      shortLabel: "적용 기준",
      longLabel: "국내 적용 시각 기준",
    };
  }
  if (basis === "DETECTED" && event.origin.event_time_ms !== null) {
    return {
      time: event.origin.event_time_ms,
      shortLabel: "감지 기준",
      longLabel: "국내 공지 감지 기준",
    };
  }
  if (basis === "SCHEDULE" && event.origin.event_time_ms !== null) {
    return {
      time: event.origin.event_time_ms,
      shortLabel: "일정 기준",
      longLabel: "국내 공지 일정 기준",
    };
  }
  if (basis === "NOTICE" && event.origin.event_time_ms !== null) {
    return {
      time: event.origin.event_time_ms,
      shortLabel: "게시 기준",
      longLabel: "국내 공지 게시 기준",
    };
  }
  if (event.origin.effective_time_ms !== null) {
    return {
      time: event.origin.effective_time_ms,
      shortLabel: "적용 기준",
      longLabel: "국내 적용 시각 기준",
    };
  }
  if (event.origin.notice_time_ms !== null) {
    return {
      time: event.origin.notice_time_ms,
      shortLabel: "게시 기준",
      longLabel: "국내 공지 게시 기준",
    };
  }
  return { time: null, shortLabel: "이벤트 기준", longLabel: "이벤트 기준" };
}

export function selectedReactionEvent(
  reaction: Pick<MarketShareReactionSummary, "events" | "top_notice">,
  selectedEventId: string | null
): MarketShareReactionEvent | null {
  if (!selectedEventId) return reaction.top_notice;
  return (
    reaction.events.find(event => event.event_id === selectedEventId) ??
    (reaction.top_notice?.event_id === selectedEventId
      ? reaction.top_notice
      : null)
  );
}

export function selectedReactionDetailEvent(
  reaction: Pick<MarketShareReactionSummary, "events" | "top_notice">,
  selectedEventId: string | null
): MarketShareReactionEvent | null {
  return selectedEventId
    ? selectedReactionEvent(reaction, selectedEventId)
    : null;
}
