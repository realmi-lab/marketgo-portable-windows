export type MarketShareTimelinePeriod = {
  startMs: number;
  endMs: number;
  periodCapped: boolean;
};

export type MarketShareNoticeIdentity = {
  exchange: string;
  notice_id?: string | null;
  url?: string | null;
};

export function marketShareTimelineFocusScrollLeft({
  fixedWidth,
  timelineWidth,
  viewportWidth,
  focusLeft,
  fixedColumnsPinned = true,
}: {
  fixedWidth: number;
  timelineWidth: number;
  viewportWidth: number;
  focusLeft: number;
  /** False only when the main app's text columns scroll with the dates. */
  fixedColumnsPinned?: boolean;
}): number {
  const contentWidth = Math.max(0, fixedWidth) + Math.max(0, timelineWidth);
  const maxScrollLeft = Math.max(0, contentWidth - Math.max(0, viewportWidth));
  if (maxScrollLeft === 0) return 0;

  const visibleTimelineWidth = Math.max(0, viewportWidth - fixedWidth);
  const preferredFocusPosition = Math.max(80, visibleTimelineWidth * 0.62);
  const requested = fixedColumnsPinned
    ? focusLeft - preferredFocusPosition
    : Math.max(0, fixedWidth) + focusLeft - Math.max(0, viewportWidth) / 2;
  return Math.min(maxScrollLeft, Math.max(0, requested));
}

function normalizedNoticeUrl(value: string | null | undefined): string {
  const raw = value?.trim() ?? "";
  if (!raw) return "";
  try {
    const parsed = new URL(raw);
    return `${parsed.protocol.toLowerCase()}//${parsed.host.toLowerCase()}${parsed.pathname.replace(/\/$/, "") || "/"}${parsed.search}`;
  } catch {
    return raw.replace(/\/$/, "").toLowerCase();
  }
}

export function marketShareNoticeIdentity(
  row: MarketShareNoticeIdentity
): string | null {
  const exchange = row.exchange.trim().toLowerCase();
  if (!exchange) return null;
  const noticeId = row.notice_id?.trim() ?? "";
  if (noticeId) return `${exchange}:id:${noticeId}`;
  const url = normalizedNoticeUrl(row.url);
  return url ? `${exchange}:url:${url}` : null;
}

/** Keep one row per official notice; schedule revisions are not new notices. */
export function dedupeNoticeRows<T extends MarketShareNoticeIdentity>(
  rows: T[]
): T[] {
  const seen = new Set<string>();
  return rows.filter(row => {
    const identity = marketShareNoticeIdentity(row);
    if (!identity) return true;
    if (seen.has(identity)) return false;
    seen.add(identity);
    return true;
  });
}

/**
 * Collapse schedule revisions that share an event start into one timeline bar.
 * A notice page can retain its original period together with later extensions.
 */
export function mergeTimelinePeriods(
  periods: MarketShareTimelinePeriod[],
  startKey: (startMs: number) => string = startMs => String(startMs)
): MarketShareTimelinePeriod[] {
  const merged = new Map<string, MarketShareTimelinePeriod>();
  for (const period of periods) {
    const key = startKey(period.startMs);
    const existing = merged.get(key);
    if (!existing) {
      merged.set(key, period);
      continue;
    }
    merged.set(key, {
      startMs: Math.min(existing.startMs, period.startMs),
      endMs: Math.max(existing.endMs, period.endMs),
      periodCapped: existing.periodCapped || period.periodCapped,
    });
  }
  return [...merged.values()].sort(
    (left, right) => left.startMs - right.startMs || left.endMs - right.endMs
  );
}
