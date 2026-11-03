import type { MarketShareRolling24h } from "./market-share-types";

export type RollingNoticeEvidence = MarketShareRolling24h["evidence"];

function emptyEvidenceIsAuthoritative(
  evidence: RollingNoticeEvidence
): boolean {
  const sources = Object.values(evidence.domestic_notice_sources ?? {});
  return (
    sources.length > 0 &&
    sources.every(
      source =>
        source.status === "OK" &&
        (source.history_status === "COMPLETE" ||
          source.history_status === "SOURCE_LIMITED")
    )
  );
}

export function selectRollingNoticeEvidence(
  next: RollingNoticeEvidence,
  retained: RollingNoticeEvidence | null
): RollingNoticeEvidence {
  if (!retained) return next;
  if (next.domestic_notices.length > 0) return next;
  if (emptyEvidenceIsAuthoritative(next)) return next;
  return retained;
}
