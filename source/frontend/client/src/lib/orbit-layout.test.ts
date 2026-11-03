import assert from "node:assert/strict";
import test from "node:test";

import { linkPath, orbitLayout, pointOnLink } from "./orbit-layout.ts";

const exchanges = [
  { id: "bithumb", volume: 50 },
  { id: "upbit", volume: 120 },
  { id: "coinone", volume: 10 },
  { id: "korbit", volume: 8 },
  { id: "gopax", volume: 1 },
];

function layout() {
  return orbitLayout({
    width: 600,
    height: 500,
    exchanges,
    coins: [
      { id: "BTC", volume: 90 },
      { id: "PYBOBO", volume: 20 },
      { id: "XRP", volume: 40 },
    ],
    links: [
      { id: "1", coin: "BTC", exchange: "upbit", volume: 70 },
      { id: "2", coin: "BTC", exchange: "bithumb", volume: 20 },
      { id: "3", coin: "PYBOBO", exchange: "bithumb", volume: 20 },
      { id: "4", coin: "XRP", exchange: "upbit", volume: 40 },
      { id: "5", coin: "GONE", exchange: "upbit", volume: 5 },
    ],
  });
}

test("puts the first exchange at the top of the inner ring and coins outside it", () => {
  const result = layout();
  const bithumb = result.exchanges[0];
  assert.equal(bithumb.id, "bithumb");
  assert.ok(Math.abs(bithumb.x - result.center.x) < 1e-9);
  assert.ok(bithumb.y < result.center.y);
  for (const coin of result.coins) {
    const distance = Math.hypot(
      coin.x - result.center.x,
      coin.y - result.center.y
    );
    assert.ok(Math.abs(distance - result.outer) < 1e-9);
    assert.ok(result.outer > result.inner);
  }
  // Larger amounts draw larger nodes.
  const upbit = result.exchanges.find(node => node.id === "upbit")!;
  assert.ok(upbit.r > bithumb.r);
});

test("groups each coin with the exchange where it trades most", () => {
  const result = layout();
  const order = result.coins.map(coin => coin.id);
  // Bithumb's cluster (PYBOBO) comes first, then Upbit's, largest first.
  assert.deepEqual(order, ["PYBOBO", "BTC", "XRP"]);
});

test("draws only links whose two ends are on the orbit", () => {
  const result = layout();
  assert.deepEqual(
    result.links.map(link => link.id),
    ["1", "2", "3", "4"]
  );
  const heaviest = result.links.find(link => link.id === "1")!;
  assert.equal(heaviest.weight, 1);
  assert.ok(heaviest.width > result.links.find(link => link.id === "2")!.width);
});

test("walks a link from its exchange to its coin", () => {
  const link = layout().links[0];
  assert.deepEqual(pointOnLink(link, 0), link.from);
  const end = pointOnLink(link, 1);
  assert.ok(
    Math.abs(end.x - link.to.x) < 1e-9 && Math.abs(end.y - link.to.y) < 1e-9
  );
  assert.match(linkPath(link), /^M[\d.]+,[\d.]+Q[\d.]+,[\d.]+ [\d.]+,[\d.]+$/);
});
