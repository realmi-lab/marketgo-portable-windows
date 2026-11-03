import assert from "node:assert/strict";
import test from "node:test";

import {
  SCREEN_IDS,
  screenHash,
  screenRouteFromHash,
} from "./dashboard-navigation.ts";

test("lists eleven menus and the data-status screen once each", () => {
  assert.deepEqual(SCREEN_IDS, [
    "brief",
    "live",
    "change",
    "why",
    "coins",
    "exchanges",
    "index",
    "index2",
    "coin-shares",
    "notices",
    "question",
    "status",
  ]);
});

test("opens each screen from its own hash and unknown hashes on the briefing", () => {
  for (const screen of SCREEN_IDS) {
    assert.deepEqual(screenRouteFromHash(`#${screen}`), { screen });
  }
  assert.deepEqual(screenRouteFromHash(""), { screen: "brief" });
  assert.deepEqual(screenRouteFromHash("#index3"), { screen: "brief" });
});

test("keeps links saved before the redesign on the screen that holds their content", () => {
  const legacy: Array<[string, ReturnType<typeof screenRouteFromHash>]> = [
    ["#summary", { screen: "brief" }],
    ["#market-share", { screen: "brief" }],
    ["#intelligence", { screen: "change" }],
    ["#drivers", { screen: "coins" }],
    ["#liquidity", { screen: "exchanges" }],
    ["#events", { screen: "notices" }],
    ["#notices", { screen: "notices" }],
    ["#notice-llm", { screen: "notices" }],
    ["#notice-llm2", { screen: "notices", focus: "notice-draft" }],
    ["#quality", { screen: "status" }],
    ["#share-llm", { screen: "question" }],
  ];
  for (const [hash, route] of legacy) {
    assert.deepEqual(screenRouteFromHash(hash), route, hash);
  }
});

test("keeps the notice draft at its own address and other links on their screen", () => {
  const draft = screenRouteFromHash("#notice-draft");
  assert.deepEqual(draft, { screen: "notices", focus: "notice-draft" });
  assert.equal(screenHash(draft), "#notice-draft");
  assert.equal(
    screenHash(screenRouteFromHash("#notice-llm2")),
    "#notice-draft"
  );
  // The two indexes kept their addresses and open as their own screens.
  assert.equal(screenHash(screenRouteFromHash("#index2")), "#index2");
  assert.equal(screenHash(screenRouteFromHash("#index")), "#index");
  assert.equal(screenHash(screenRouteFromHash("#summary")), "#brief");
});
