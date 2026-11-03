import assert from "node:assert/strict";
import test from "node:test";

import {
  OFFICIAL_CHANGE_CARD_GROUPS,
  OFFICIAL_CHANGE_FLOW,
  OFFICIAL_SUMMARY_CARD_ORDER,
  OFFICIAL_SUMMARY_LIGHT_GROUPS,
} from "./official-summary-layout.ts";

test("keeps every official summary capability in one non-duplicated card order", () => {
  assert.deepEqual(OFFICIAL_SUMMARY_CARD_ORDER, [
    "share",
    "bithumb-volume",
    "domestic-volume",
    "collection",
    "event",
    "asset",
    "evidence",
  ]);
  assert.equal(
    OFFICIAL_SUMMARY_CARD_ORDER.filter(card => card === "share").length,
    1
  );
});

test("official change flow keeps the answer first and every detail reachable", () => {
  assert.deepEqual(OFFICIAL_CHANGE_FLOW, ["focus", "question", "details"]);
  assert.deepEqual(
    [
      ...OFFICIAL_CHANGE_CARD_GROUPS.focus,
      ...OFFICIAL_CHANGE_CARD_GROUPS.details,
      ...OFFICIAL_CHANGE_CARD_GROUPS.trust,
    ].sort(),
    [...OFFICIAL_SUMMARY_CARD_ORDER].sort()
  );
});

test("light hierarchy preserves every official summary item exactly once", () => {
  assert.deepEqual(
    [
      ...OFFICIAL_SUMMARY_LIGHT_GROUPS.hero,
      ...OFFICIAL_SUMMARY_LIGHT_GROUPS.secondary,
      ...OFFICIAL_SUMMARY_LIGHT_GROUPS.facts,
    ],
    OFFICIAL_SUMMARY_CARD_ORDER
  );
});
