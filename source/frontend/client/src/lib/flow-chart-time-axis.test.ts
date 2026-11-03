import assert from "node:assert/strict";
import test from "node:test";
import { flowChartTimeTickStep } from "./flow-chart-time-axis.ts";

test("two-minute sidebar leaves room for full time labels", () => {
  assert.equal(flowChartTimeTickStep(120_000, 357), 30_000);
  assert.equal(flowChartTimeTickStep(120_000, 800), 15_000);
});

test("time labels keep their spacing across plot widths and display ranges", () => {
  for (const width of [240, 357, 480, 800, 1200]) {
    for (const span of [30_000, 60_000, 120_000, 300_000]) {
      const step = flowChartTimeTickStep(span, width);
      assert.ok(step / span * width >= 72, `${span}ms at ${width}px`);
      assert.equal(step % 15_000, 0);
    }
  }
});
