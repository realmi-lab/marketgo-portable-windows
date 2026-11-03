import assert from "node:assert/strict";
import test from "node:test";

import {
  DASHBOARD_SECTION_IDS,
  dashboardPageForSection,
  dashboardSectionFromHash,
} from "./dashboard-navigation.ts";

test("restores the notice drafting entry alongside active dashboard sections", () => {
  assert.deepEqual(DASHBOARD_SECTION_IDS, [
    "summary",
    "share-llm",
    "intelligence",
    "drivers",
    "events",
    "notice-llm2",
    "quality",
    "index",
    "index2",
  ]);
  assert.equal(new Set(DASHBOARD_SECTION_IDS).size, 9);
});

test("routes the market intelligence tab without changing existing sections", () => {
  assert.equal(dashboardSectionFromHash("#index3"), "summary");
  assert.equal(dashboardSectionFromHash("#index2"), "index2");
  assert.equal(dashboardPageForSection("index2"), "index2");
  assert.equal(dashboardSectionFromHash("#index"), "index");
  assert.equal(dashboardPageForSection("index"), "index");
  assert.equal(dashboardSectionFromHash("#share-llm"), "share-llm");
  assert.equal(dashboardPageForSection("share-llm"), "share-llm");
  assert.equal(dashboardSectionFromHash("#intelligence"), "intelligence");
  assert.equal(dashboardPageForSection("intelligence"), "intelligence");
  assert.equal(dashboardPageForSection("summary"), "summary");
  assert.equal(dashboardPageForSection("events"), "events");
  assert.equal(dashboardSectionFromHash("#notice-llm"), "events");
  assert.equal(dashboardSectionFromHash("#notice-llm2"), "notice-llm2");
  assert.equal(dashboardPageForSection("notice-llm2"), "notice-llm2");
  assert.equal(dashboardPageForSection("drivers"), "market-share");
  assert.equal(dashboardSectionFromHash("#liquidity"), "summary");
  assert.equal(dashboardPageForSection("quality"), "market-share");
  assert.equal(dashboardSectionFromHash("#market-share"), "summary");
  assert.equal(dashboardSectionFromHash("#notices"), "events");
});
