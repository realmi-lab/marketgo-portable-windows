import {
  dedupeNoticeRows,
  marketShareNoticeIdentity,
  type MarketShareNoticeIdentity,
} from "./market-share-timeline.ts";

/** Reuse server relevance and rendered timeline membership, never infer causality. */
export function selectRelevantRollingNotices<
  T extends MarketShareNoticeIdentity & { related: boolean },
>(notices: readonly T[], timelineNotices: readonly T[]): T[] {
  const timelineRows = new Set(timelineNotices);
  const timelineIDs = new Set(
    timelineNotices
      .map(marketShareNoticeIdentity)
      .filter((identity): identity is string => identity !== null)
  );
  return dedupeNoticeRows(
    notices.filter(notice => {
      const identity = marketShareNoticeIdentity(notice);
      return (
        notice.related === true ||
        timelineRows.has(notice) ||
        (identity !== null && timelineIDs.has(identity))
      );
    })
  );
}
