import assert from "node:assert/strict";
import test from "node:test";

import { labelledIndexes, placedLabels } from "./chart-labels.ts";

// A 40px-wide label at x, centred unless it is the first or last point.
const boxes =
  (xs: readonly number[], ys: readonly number[], width = 40) =>
  (index: number) => {
    const x = xs[index];
    const left =
      index === 0 ? x : index === xs.length - 1 ? x - width : x - width / 2;
    return {
      left,
      right: left + width,
      top: ys[index] - 12,
      bottom: ys[index],
    };
  };

// A chart wide enough for every label below.
const wide = { left: -1000, right: 10_000 };

test("labels every point when there is room", () => {
  const values = [20, 21, 22, 21.5];
  const xs = [0, 100, 200, 300];
  const ys = [50, 50, 50, 50];
  assert.deepEqual(
    [...labelledIndexes(values, boxes(xs, ys), wide)].sort((a, b) => a - b),
    [0, 1, 2, 3]
  );
});

test("thins labels on a narrow chart but keeps the latest, highest and lowest", () => {
  const values = Array.from(
    { length: 48 },
    (_, index) => 20 + Math.sin(index / 5)
  );
  values[10] = 25; // highest
  values[30] = 15; // lowest
  const xs = values.map((_, index) => index * 12);
  const ys = values.map(() => 50);
  const labelled = labelledIndexes(values, boxes(xs, ys), wide);
  assert.ok(labelled.has(47), "latest");
  assert.ok(labelled.has(10), "highest");
  assert.ok(labelled.has(30), "lowest");
  const box = boxes(xs, ys);
  const sorted = [...labelled].sort((a, b) => a - b);
  for (let index = 1; index < sorted.length; index += 1) {
    assert.ok(
      box(sorted[index]).left >= box(sorted[index - 1]).right,
      "no overlap"
    );
  }
});

test("keeps the last label, drawn leftward from its point, off its neighbour", () => {
  // 48px apart: a centred label beside an end-anchored one would overlap.
  const values = [21.5, 21.6, 21.64, 21.53];
  const xs = [0, 48, 96, 144];
  const ys = [50, 50, 50, 50];
  const labelled = labelledIndexes(values, boxes(xs, ys), wide);
  assert.ok(labelled.has(3), "latest");
  assert.ok(!labelled.has(2), "its neighbour gives way");
});

test("labels at different heights may share the same columns", () => {
  const values = [20, 30];
  const xs = [0, 20];
  const ys = [80, 20];
  assert.equal(labelledIndexes(values, boxes(xs, ys), wide).size, 2);
});

test("leaves out a label that would cross the axis", () => {
  // Centred at x=10, the second label would reach left of the plot at 0.
  const values = [20, 21, 22];
  const xs = [0, 10, 100];
  const ys = [50, 80, 50];
  const labelled = labelledIndexes(values, boxes(xs, ys), {
    left: 0,
    right: 200,
  });
  assert.ok(!labelled.has(1));
  assert.ok(labelled.has(0) && labelled.has(2));
});

test("puts the chosen point first and never labels a missing value", () => {
  const values = [20, null, 21, 22, 23];
  const xs = [0, 10, 20, 30, 40];
  const ys = [50, 50, 50, 50, 50];
  const labelled = labelledIndexes(values, boxes(xs, ys), wide, 3);
  assert.ok(labelled.has(3));
  assert.ok(!labelled.has(1));
  assert.equal(
    labelledIndexes([null, null], boxes([0, 50], [50, 50]), wide).size,
    0
  );
});

test("places names beside their circles without overlap or overflow", () => {
  const placed = placedLabels(
    [
      { key: "BTC", x: 100, y: 50, r: 10 },
      // Right beside BTC's name: it goes to the left of its own circle.
      { key: "ETH", x: 120, y: 52, r: 4 },
      // Same spot again: no room on either side, so no name.
      { key: "XRP", x: 118, y: 50, r: 4 },
      // At the right edge: the name goes on the left.
      { key: "SOL", x: 290, y: 120, r: 6 },
    ],
    { left: 0, right: 300 }
  );
  assert.deepEqual(placed.get("BTC"), { x: 114, anchor: "start" });
  assert.deepEqual(placed.get("ETH"), { x: 112, anchor: "end" });
  assert.equal(placed.has("XRP"), false);
  assert.deepEqual(placed.get("SOL"), { x: 280, anchor: "end" });
});

test("crowded coin names cannot fall back into the percentage-axis gutter", () => {
  const btc = { key: "BTC", x: 80, y: 304, r: 17 };
  const rlusd = { key: "RLUSD", x: 61, y: 304, r: 11 };
  const area = { left: 44, right: 310 };
  // RLUSD's old left candidate was x=46, extending across the 0% label.
  const placed = placedLabels([btc, rlusd], area);
  assert.ok(placed.has("BTC"));
  assert.equal(placed.has("RLUSD"), false);
  // Selecting the point prioritizes its full name on the right; the point
  // coordinates and numerical values do not change to make the label fit.
  const selected = placedLabels([rlusd, btc], area);
  assert.deepEqual(selected.get("RLUSD"), { x: 76, anchor: "start" });
  assert.equal(selected.has("BTC"), false);
});
