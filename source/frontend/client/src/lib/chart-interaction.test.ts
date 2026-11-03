import assert from "node:assert/strict";
import test from "node:test";

import {
  cycledId,
  extendTrail,
  keyedIndex,
  tipPosition,
} from "./chart-interaction.ts";

test("the first arrow press picks the latest point, later ones step", () => {
  assert.equal(keyedIndex("ArrowLeft", null, 48), 47);
  assert.equal(keyedIndex("ArrowRight", null, 48), 47);
  assert.equal(keyedIndex("ArrowLeft", 47, 48), 46);
  assert.equal(keyedIndex("ArrowLeft", 0, 48), 0);
  assert.equal(keyedIndex("ArrowRight", 47, 48), 47);
  assert.equal(keyedIndex("Home", 20, 48), 0);
  assert.equal(keyedIndex("End", 20, 48), 47);
  assert.equal(keyedIndex("Enter", 20, 48), null);
  assert.equal(keyedIndex("ArrowLeft", null, 0), null);
});

test("cycles coins by id, so a reordered list keeps the same coin", () => {
  const ids = ["BTC", "XRP", "ETH"];
  assert.equal(cycledId(ids, null, 1), "BTC");
  assert.equal(cycledId(ids, null, -1), "ETH");
  assert.equal(cycledId(ids, "XRP", 1), "ETH");
  assert.equal(cycledId(ids, "ETH", 1), "BTC");
  assert.equal(cycledId(ids, "BTC", -1), "ETH");
  // A coin that left the list starts over.
  assert.equal(cycledId(ids, "GONE", 1), "BTC");
  assert.equal(cycledId([], "BTC", 1), null);
});

test("keeps a tooltip inside the chart on every side", () => {
  const box = { width: 320, height: 200 };
  const tip = { width: 180, height: 120 };
  // Room on the right.
  assert.deepEqual(tipPosition({ x: 40, y: 100 }, tip, box), {
    left: 54,
    top: 40,
  });
  // No room on the right: it goes left of the point.
  assert.deepEqual(tipPosition({ x: 300, y: 100 }, tip, box), {
    left: 106,
    top: 40,
  });
  // No room on either side (a phone): pinned inside, never cut.
  const middle = tipPosition({ x: 160, y: 10 }, tip, box);
  assert.ok(middle.left >= 0 && middle.left + tip.width <= box.width);
  assert.equal(middle.top, 0);
  assert.equal(tipPosition({ x: 160, y: 195 }, tip, box).top, 80);
});

test("extends each window's trail by the value received, newest last", () => {
  const first = extendTrail({}, [{ key: 10, value: 21.5 }], 3);
  const second = extendTrail(
    first,
    [
      { key: 10, value: null },
      { key: 30, value: 22 },
    ],
    3
  );
  assert.deepEqual(second, { 10: [21.5, null], 30: [22] });
  const capped = extendTrail({ 10: [1, 2, 3] }, [{ key: 10, value: 4 }], 3);
  assert.deepEqual(capped, { 10: [2, 3, 4] });
});

test("keeps a coin's trail by its name and drops a coin no longer listed", () => {
  const first = extendTrail(
    {},
    [
      { key: "BTC", value: 19.9 },
      { key: "XRP", value: 17.4 },
    ],
    3
  );
  const second = extendTrail(first, [{ key: "BTC", value: 20.1 }], 3);
  assert.deepEqual(second, { BTC: [19.9, 20.1] });
});

test("chart pointer positions stay on the same data point at 80 percent scale", async () => {
  const {chartLocalPoint}=await import("./chart-interaction.ts");
  const size={width:640,height:300};
  assert.deepEqual(chartLocalPoint(360,200,{left:40,top:50,width:640,height:300},size),{x:320,y:150});
  assert.deepEqual(chartLocalPoint(296,170,{left:40,top:50,width:512,height:240},size),{x:320,y:150});
  assert.equal(chartLocalPoint(40,50,{left:40,top:50,width:0,height:0},size),null);
});
