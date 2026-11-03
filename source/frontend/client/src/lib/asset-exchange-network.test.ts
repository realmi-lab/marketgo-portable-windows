import assert from "node:assert/strict";
import test from "node:test";

import {
  ASSET_EXCHANGE_NETWORK_MIN_EDGE_WIDTH,
  assetExchangeNetworkEdgePath,
  assetExchangeNetworkEdgeWidth,
  buildAssetExchangeNetworkLayout,
  filterAssetExchangeNetworkEdges,
} from "./asset-exchange-network.ts";

const nodes = [
  { id: "asset:BTC", kind: "ASSET" as const, current_volume_krw: 100 },
  { id: "asset:ETH", kind: "ASSET" as const, current_volume_krw: 50 },
  {
    id: "exchange:bithumb",
    kind: "EXCHANGE" as const,
    current_volume_krw: 80,
  },
  {
    id: "exchange:upbit",
    kind: "EXCHANGE" as const,
    current_volume_krw: 70,
  },
];

test("layout is deterministic and keeps exchanges inside the asset ring", () => {
  const first = buildAssetExchangeNetworkLayout(nodes);
  const second = buildAssetExchangeNetworkLayout(nodes);

  assert.deepEqual(first, second);

  const center = { x: first.width / 2, y: first.height / 2 };
  const distance = (id: string) => {
    const point = first.positions[id];
    return Math.hypot(point.x - center.x, point.y - center.y);
  };

  assert.ok(distance("asset:BTC") > distance("exchange:bithumb"));
  assert.ok(distance("asset:ETH") > distance("exchange:upbit"));
});

test("line width is monotonic and a zero-current decrease remains visible", () => {
  const zero = assetExchangeNetworkEdgeWidth(0, 1_000);
  const medium = assetExchangeNetworkEdgeWidth(250, 1_000);
  const maximum = assetExchangeNetworkEdgeWidth(1_000, 1_000);

  assert.equal(zero, ASSET_EXCHANGE_NETWORK_MIN_EDGE_WIDTH);
  assert.ok(medium > zero);
  assert.ok(maximum > medium);
  assert.equal(
    assetExchangeNetworkEdgeWidth(Number.NaN, 1_000),
    ASSET_EXCHANGE_NETWORK_MIN_EDGE_WIDTH
  );
});

test("direction filters preserve server order and do not infer direction", () => {
  const edges = [
    { id: "one", volume_direction: "DECREASE" as const },
    { id: "two", volume_direction: "INCREASE" as const },
    { id: "three", volume_direction: "UNCHANGED" as const },
  ];

  assert.deepEqual(
    filterAssetExchangeNetworkEdges(edges, "INCREASE").map(edge => edge.id),
    ["two"]
  );
  assert.deepEqual(
    filterAssetExchangeNetworkEdges(edges, "DECREASE").map(edge => edge.id),
    ["one"]
  );
  assert.deepEqual(
    filterAssetExchangeNetworkEdges(edges, "ALL").map(edge => edge.id),
    ["one", "two", "three"]
  );
});

test("edge curves are stable and contain no arrow marker semantics", () => {
  const source = { x: 10, y: 20 };
  const target = { x: 200, y: 160 };
  const first = assetExchangeNetworkEdgePath("BTC:bithumb", source, target);

  assert.equal(
    first,
    assetExchangeNetworkEdgePath("BTC:bithumb", source, target)
  );
  assert.match(first, /^M .+ Q .+$/);
  assert.doesNotMatch(first, /marker|arrow/i);
});
