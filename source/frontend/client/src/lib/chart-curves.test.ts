import assert from "node:assert/strict";
import test from "node:test";

import {
  lonePointsPath,
  smoothAreaPath,
  smoothLinePath,
} from "./chart-curves.ts";

const at = (ys: ReadonlyArray<number | null>) =>
  ys.map((y, index) => ({ x: index * 10, y }));

/** Each cubic segment as [start y, control y, control y, end y]. */
function segments(path: string): number[][] {
  const result: number[][] = [];
  let y = 0;
  for (const command of path.match(/[MLC][^MLCZ]*/g) ?? []) {
    const numbers = command.slice(1).split(/[ ,]/).map(Number);
    if (command[0] === "C") {
      result.push([y, numbers[1], numbers[3], numbers[5]]);
    }
    y = numbers[numbers.length - 1];
  }
  return result;
}

test("passes through every point without overshooting between them", () => {
  const ys = [50, 52, 51, 51, 58, 40, 41];
  const path = smoothLinePath(at(ys));
  const parts = segments(path);
  assert.equal(parts.length, ys.length - 1);
  parts.forEach(([start, first, second, end], index) => {
    assert.equal(start, ys[index]);
    assert.equal(end, ys[index + 1]);
    for (const control of [first, second]) {
      assert.ok(
        control >= Math.min(start, end) - 1e-9 &&
          control <= Math.max(start, end) + 1e-9,
        `segment ${index} control ${control} leaves ${start}..${end}`
      );
    }
  });
});

test("flattens at a peak and is straight between two points", () => {
  // The peak's neighbouring control points sit level with it.
  const [rise, fall] = segments(smoothLinePath(at([0, 10, 0])));
  assert.equal(rise[2], 10);
  assert.equal(fall[1], 10);
  assert.equal(
    smoothLinePath(at([0, 30])),
    "M0.0,0.0C3.3,10.0 6.7,20.0 10.0,30.0"
  );
});

test("breaks where a value is missing and marks a lone point as a dot", () => {
  const points = at([1, 2, null, 5, null, null, 7, 8, null, 9]);
  assert.equal(
    smoothLinePath(points),
    "M0.0,1.0C3.3,1.3 6.7,1.7 10.0,2.0M60.0,7.0C63.3,7.3 66.7,7.7 70.0,8.0"
  );
  assert.equal(lonePointsPath(points), "M30.0,5.0L30.0,5.0M90.0,9.0L90.0,9.0");
  assert.equal(smoothLinePath(at([null, null])), "");
  assert.equal(lonePointsPath(at([1, 2])), "");
});

test("closes each run's area down to the baseline", () => {
  assert.equal(
    smoothAreaPath(at([1, 2, null, 5]), 40),
    "M0.0,40.0L0.0,1.0C3.3,1.3 6.7,1.7 10.0,2.0L10.0,40.0ZM30.0,40.0L30.0,5.0L30.0,40.0Z"
  );
});
