import assert from "node:assert/strict";
import test from "node:test";

import { buildShareYAxis, SHARE_Y_AXIS_TICK_STEP } from "./share-chart-axis.ts";

test("uses 0.5 percentage-point ticks with padding outside both data edges", () => {
  const axis = buildShareYAxis([30.8, 31.4]);

  assert.deepEqual(axis, {
    domain: [30, 32],
    ticks: [30, 30.5, 31, 31.5, 32],
  });
  assert.ok(30.8 - axis.domain[0] >= SHARE_Y_AXIS_TICK_STEP);
  assert.ok(axis.domain[1] - 31.4 >= SHARE_Y_AXIS_TICK_STEP);
  assert.ok(
    axis.ticks
      .slice(1)
      .every((tick, index) =>
        Object.is(tick - axis.ticks[index], SHARE_Y_AXIS_TICK_STEP)
      )
  );
});

test("a constant series still gets one interval of padding on both sides", () => {
  assert.deepEqual(buildShareYAxis([31, 31, 31]), {
    domain: [30.5, 31.5],
    ticks: [30.5, 31, 31.5],
  });
});

test("empty and non-finite series use the same deterministic fallback", () => {
  const fallback = {
    domain: [0, 1],
    ticks: [0, 0.5, 1],
  };

  assert.deepEqual(buildShareYAxis([]), fallback);
  assert.deepEqual(
    buildShareYAxis([null, undefined, Number.NaN, Number.POSITIVE_INFINITY]),
    fallback
  );
});

test("the lower percentage boundary keeps a full minimum span", () => {
  assert.deepEqual(buildShareYAxis([0]), {
    domain: [0, 1],
    ticks: [0, 0.5, 1],
  });
  assert.deepEqual(buildShareYAxis([-20]), {
    domain: [0, 1],
    ticks: [0, 0.5, 1],
  });
});

test("the upper percentage boundary keeps a full minimum span", () => {
  assert.deepEqual(buildShareYAxis([100]), {
    domain: [99, 100],
    ticks: [99, 99.5, 100],
  });
  assert.deepEqual(buildShareYAxis([120]), {
    domain: [99, 100],
    ticks: [99, 99.5, 100],
  });
});

test("mixed out-of-range values clamp to the complete percentage domain", () => {
  const axis = buildShareYAxis([-1, 101]);

  assert.deepEqual(axis.domain, [0, 100]);
  assert.equal(axis.ticks[0], 0);
  assert.equal(axis.ticks.at(-1), 100);
  assert.equal(axis.ticks.length, 201);
});
