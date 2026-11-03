import assert from "node:assert/strict";
import test from "node:test";
import type { MarketShareRolling24h } from "./market-share-types.ts";
import { selectRollingNoticeEvidence } from "./rolling-notice-retention.ts";

type Evidence = MarketShareRolling24h["evidence"];

function evidence(
  notices: Evidence["domestic_notices"],
  status: "OK" | "PENDING" | undefined,
  historyStatus: string = "PENDING"
): Evidence {
  return {
    domestic_notices: notices,
    domestic_notice_sources: status
      ? {
          bithumb: {
            status,
            fetched_count: notices.length,
            history_status: historyStatus,
            last_success_ts: status === "OK" ? 1 : 0,
          },
        }
      : undefined,
    causal_claim: false,
  };
}

const retained = evidence([{ notice_id: "saved" } as never], "OK", "COMPLETE");

test("keeps the last notice timeline while a compact refresh omits evidence", () => {
  assert.equal(
    selectRollingNoticeEvidence(evidence([], undefined), retained),
    retained
  );
  assert.equal(
    selectRollingNoticeEvidence(evidence([], "PENDING"), retained),
    retained
  );
});

test("replaces retained notices with a new non-empty snapshot", () => {
  const next = evidence([{ notice_id: "new" } as never], "PENDING");
  assert.equal(selectRollingNoticeEvidence(next, retained), next);
});

test("accepts an authoritative healthy empty snapshot", () => {
  const next = evidence([], "OK", "COMPLETE");
  assert.equal(selectRollingNoticeEvidence(next, retained), next);
});
