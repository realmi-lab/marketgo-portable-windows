import type {
  ExchangeNotice,
  MarketShareNoticeSnapshot,
} from "./market-share-types.ts";

/**
 * A notice as a list of titles shows it: what the server sends for
 * `fields: "headline"`, without the body, images or analysis.
 */
export type NoticeHeadline = Pick<
  ExchangeNotice,
  | "exchange"
  | "notice_id"
  | "title"
  | "url"
  | "category"
  | "published_ts_ms"
  | "detected_ts_ms"
  | "timestamp_precision"
  | "assets"
  | "analysis_status"
>;

export interface NoticeArchivePage<Item = ExchangeNotice> {
  snapshot: Omit<MarketShareNoticeSnapshot, "items"> & { items: Item[] };
  total: number;
  offset: number;
  limit: number;
  as_of_ms: number;
  from_ms: number;
  source_counts?: Record<
    string,
    { stored: number; body_missing: number; image_pending: number }
  >;
}

export interface NoticeArchiveRequest {
  offset: number;
  /** Page size; the server accepts 1..100 and the paged screens use 20. */
  limit?: number;
  version?: number;
  asOfMS?: number;
  /** "headline": each notice as a list of titles shows it (NoticeHeadline). */
  fields?: "headline";
}

export function noticeArchivePath(request: NoticeArchiveRequest): string {
  const query = new URLSearchParams({
    offset: String(request.offset),
    limit: String(request.limit ?? 20),
  });
  if (request.version !== undefined)
    query.set("version", String(request.version));
  if (request.asOfMS !== undefined)
    query.set("as_of_ms", String(request.asOfMS));
  if (request.fields) query.set("fields", request.fields);
  return `/api/market-share/notices?${query}`;
}

/** Checks a notice page and its items' identity, then shapes each item. */
function normalizePage<Item>(
  value: unknown,
  shape: (item: ExchangeNotice) => Item
): NoticeArchivePage<Item> {
  const page = value as NoticeArchivePage;
  if (
    !page ||
    !Number.isInteger(page.total) ||
    page.total < 0 ||
    !Number.isInteger(page.offset) ||
    page.offset < 0 ||
    !Number.isInteger(page.limit) ||
    page.limit < 1 ||
    page.limit > 100 ||
    !Number.isFinite(page.as_of_ms) ||
    !Number.isFinite(page.from_ms) ||
    page.from_ms >= page.as_of_ms ||
    !page.snapshot ||
    !Number.isFinite(page.snapshot.version) ||
    !page.snapshot.sources ||
    !Array.isArray(page.snapshot.items) ||
    page.snapshot.items.length > page.limit ||
    page.snapshot.items.length !==
      Math.max(0, Math.min(page.limit, page.total - page.offset))
  ) {
    throw new Error("전체 공지 응답을 확인하지 못했습니다.");
  }
  const items = page.snapshot.items.map(item => {
    if (
      !item ||
      typeof item.exchange !== "string" ||
      typeof item.notice_id !== "string" ||
      typeof item.title !== "string"
    )
      throw new Error("공지 항목을 확인하지 못했습니다.");
    return shape(item);
  });
  return {
    ...page,
    snapshot: {
      ...page.snapshot,
      error_history: page.snapshot.error_history ?? [],
      items,
    },
  };
}

export function normalizeNoticeArchivePage(value: unknown): NoticeArchivePage {
  return normalizePage(
    value,
    item =>
      ({
        ...item,
        assets: item.assets ?? [],
        schedules: item.schedules ?? [],
        risk_tags: item.risk_tags ?? [],
        image_urls: item.image_urls ?? [],
        image_analysis: item.image_analysis ?? [],
        // A raw archive row has no measured market reaction. Never invent one.
        market_context: [],
        reaction_studies: [],
        within_selected_window: false,
        reaction_status: "NOT_EVALUATED",
        inflection_status: "INSUFFICIENT_OBSERVATION",
      }) as ExchangeNotice
  );
}

/** A page asked for with fields=headline: only the coin list gets a default. */
export function normalizeNoticeHeadlinePage(
  value: unknown
): NoticeArchivePage<NoticeHeadline> {
  return normalizePage(value, (item): NoticeHeadline => ({
    ...item,
    assets: item.assets ?? [],
  }));
}

export function attachNoticeArchiveContext(
  page: NoticeArchivePage,
  analysis?: { version: number; items: ExchangeNotice[] }
): ExchangeNotice[] {
  if (!analysis || analysis.version !== page.snapshot.version)
    return page.snapshot.items;
  const contexts = new Map(
    analysis.items.map(item => [`${item.exchange}:${item.notice_id}`, item])
  );
  return page.snapshot.items.map(item => {
    const context = contexts.get(`${item.exchange}:${item.notice_id}`);
    if (!context) return item;
    return {
      ...item,
      market_context: context.market_context ?? [],
      reaction_studies: context.reaction_studies ?? [],
      within_selected_window: context.within_selected_window,
      reaction_status: context.reaction_status,
      inflection_status: context.inflection_status,
      image_consistency: context.image_consistency,
    };
  });
}

export const NOTICE_ARCHIVE_MAX_LIMIT = 100;
const NOTICE_ARCHIVE_CONCURRENCY = 3;

/**
 * Every notice of the server's 30-day window, read as one version: later
 * pages are pinned to the first page's version and as_of_ms, so a new ingest
 * fails the read (409) instead of mixing two snapshots.
 */
export async function loadWholeNoticeArchive(
  fetchPage: (request: NoticeArchiveRequest) => Promise<NoticeArchivePage>
): Promise<NoticeArchivePage> {
  const first = await fetchPage({ offset: 0, limit: NOTICE_ARCHIVE_MAX_LIMIT });
  const items = [...first.snapshot.items];
  for (let offset = first.limit; offset < first.total;) {
    const requests: NoticeArchiveRequest[] = [];
    for (let slot = 0; slot < NOTICE_ARCHIVE_CONCURRENCY && offset < first.total; slot++) {
      requests.push({ offset, limit: first.limit, version: first.snapshot.version, asOfMS: first.as_of_ms });
      offset += first.limit;
    }
    // Drain the entire bounded wave before retrying a changed snapshot. A
    // rejected page must not leave old requests overlapping the next load.
    const pages = await Promise.allSettled(requests.map(fetchPage));
    for (let index = 0; index < pages.length; index++) {
      const result = pages[index];
      if (result.status === "rejected") throw result.reason;
      const page = result.value;
      if (page.snapshot.version !== first.snapshot.version ||
          page.as_of_ms !== first.as_of_ms || page.from_ms !== first.from_ms ||
          page.total !== first.total) {
        throw new Error("NOTICE_ARCHIVE_CHANGED");
      }
      if (page.offset !== requests[index].offset || page.limit !== first.limit ||
          page.snapshot.items.length !== Math.min(first.limit, first.total - page.offset)) {
        throw new Error("NOTICE_ARCHIVE_INCOMPLETE");
      }
      items.push(...page.snapshot.items);
    }
  }
  return {
    ...first,
    limit: Math.max(first.limit, items.length),
    snapshot: { ...first.snapshot, items },
  };
}
