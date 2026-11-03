import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { ModuleKind, ScriptTarget, transpileModule } from "typescript";

import { animatedMetricTarget } from "./animated-metric-target.ts";
import {
  formatSharePercent,
  formatSignedPercentagePoints,
} from "./formatters.ts";

// Execute the real hook with normal motion and a controlled animation clock.
// Only React's storage/effect boundary and the shared frame scheduler are faked.
function normalMotionHarness() {
  const source = readFileSync(
    new URL("../hooks/useAnimatedMetrics.ts", import.meta.url),
    "utf8"
  );
  const compiled = transpileModule(source, {
    compilerOptions: {
      target: ScriptTarget.ES2022,
      module: ModuleKind.CommonJS,
    },
  }).outputText;
  const slots: unknown[] = [];
  let cursor = 0;
  let frame: ((now: number) => boolean) | undefined;
  const context = {
    exports: {} as {
      useAnimatedMetrics: (
        values: Array<number | null>,
        digits: number,
        duration: number,
        interval: number,
        preserveRawValue: boolean
      ) => Array<number | null>;
    },
    require(name: string) {
      if (name === "react") {
        return {
          useState(initial: unknown) {
            const index = cursor++;
            if (!(index in slots)) slots[index] = initial;
            return [slots[index], (next: unknown) => (slots[index] = next)];
          },
          useRef(initial: unknown) {
            const index = cursor++;
            if (!(index in slots)) slots[index] = { current: initial };
            return slots[index];
          },
          useEffect(effect: () => void) {
            effect();
          },
        };
      }
      if (name === "@/lib/animated-metric-target") {
        return { animatedMetricTarget };
      }
      if (name === "@/lib/animation-frame-coordinator") {
        return {
          subscribeSharedAnimationFrame(callback: (now: number) => boolean) {
            frame = callback;
            return () => {};
          },
        };
      }
      throw new Error(`Unexpected hook import: ${name}`);
    },
    window: {
      matchMedia: () => ({
        matches: false,
        addEventListener() {},
        removeEventListener() {},
      }),
    },
    performance: { now: () => 0 },
  };
  runInNewContext(compiled, context);
  return {
    render(values: Array<number | null>, preserveRawValue = true) {
      cursor = 0;
      context.exports.useAnimatedMetrics(values, 3, 420, 32, preserveRawValue);
    },
    tick(now: number) {
      assert.ok(frame, "normal motion must subscribe to animation frames");
      return frame(now);
    },
    displayed: () => slots[0] as Array<number | null>,
  };
}

test("normal motion commits exact raw endpoints after an interpolated frame", () => {
  const animation = normalMotionHarness();
  const targets = [0.0015, 1e-20, -0.0004, null, 0];
  animation.render([1, 1, -1, 1, 1]);
  animation.render(targets);
  assert.equal(animation.tick(210), true);
  assert.ok(animation.displayed()[0]! > targets[0]!);
  assert.ok(animation.displayed()[0]! < 1);
  assert.equal(animation.tick(420), false);
  targets.forEach((target, index) => {
    assert.equal(animation.displayed()[index], target);
    if (target !== null) {
      assert.equal(
        formatSignedPercentagePoints(animation.displayed()[index]!, 3),
        formatSignedPercentagePoints(target, 3)
      );
    }
  });
  assert.equal(
    formatSignedPercentagePoints(animation.displayed()[0]!, 3),
    "+0.002%p"
  );
  assert.equal(
    formatSignedPercentagePoints(animation.displayed()[1]!, 3),
    "0.001%p 미만 상승"
  );
  assert.equal(
    formatSignedPercentagePoints(animation.displayed()[4]!, 3),
    "변화 없음"
  );
});

test("normal motion also commits the existing rounded default target exactly", () => {
  const animation = normalMotionHarness();
  animation.render([1], false);
  animation.render([0.0015], false);
  assert.equal(animation.tick(420), false);
  assert.equal(animation.displayed()[0], 0.002);
});

test("opt-in raw animation targets preserve tiny signed contributions through final formatting", () => {
  for (const value of [0.0004, -0.0004, 0.00049, -0.00049, 0, 0.2]) {
    const target = animatedMetricTarget(value, 3, true);
    assert.equal(target, value);
    assert.equal(
      formatSignedPercentagePoints(target!, 3),
      formatSignedPercentagePoints(value, 3)
    );
  }
  assert.equal(
    formatSignedPercentagePoints(animatedMetricTarget(0.0004, 3, true)!, 3),
    "0.001%p 미만 상승"
  );
  assert.equal(
    formatSignedPercentagePoints(animatedMetricTarget(0, 3, true)!, 3),
    "변화 없음"
  );
});

test("other metric callers keep the existing rounded default and missing-value behavior", () => {
  assert.equal(animatedMetricTarget(12.3456, 2), 12.35);
  assert.equal(animatedMetricTarget(0.0004, 3), 0);
  for (const value of [null, undefined, Number.NaN, Infinity, -Infinity]) {
    assert.equal(animatedMetricTarget(value, 3), null);
    assert.equal(animatedMetricTarget(value, 3, true), null);
  }
});

test("gross metrics and bounded percentage levels preserve raw targets with accessibility and motion intact", () => {
  const read = (path: string) =>
    readFileSync(new URL(path, import.meta.url), "utf8");
  const hook = read("../hooks/useAnimatedMetrics.ts");
  const metric = read("../components/common/AnimatedMetricValue.tsx");
  const rolling = read("../pages/RollingShareSummary2.tsx");
  assert.match(hook, /preserveRawValue = false/);
  assert.match(hook, /animatedMetricTarget\(value, digits, preserveRawValue\)/);
  assert.match(metric, /useAnimatedMetrics\([\s\S]*?preserveRawValue/);
  assert.match(metric, /aria-hidden="true"/);
  assert.match(metric, /className="sr-only"/);
  assert.match(hook, /prefers-reduced-motion/);
  for (const block of rolling.match(/<AnimatedMetricValue\b[\s\S]*?\/>/g) ??
    []) {
    if (/format=\{value => percent\(value/.test(block)) {
      assert.match(block, /\bpreserveRawValue\b/);
    }
  }
  const fixedHour = read("../components/OfficialSelectedHourMetrics.tsx");
  assert.doesNotMatch(fixedHour, /AnimatedMetricValue|useAnimatedMetrics/);
});

test("raw bounded shares remain distinct from exact zero and complete concentration", () => {
  for (const value of [0.001, 99.9, 100]) {
    const target = animatedMetricTarget(value, 0, true);
    assert.equal(target, value);
    assert.equal(formatSharePercent(target, 0), formatSharePercent(value, 0));
  }
  assert.equal(
    formatSharePercent(animatedMetricTarget(0.001, 0, true), 0),
    "1% 미만"
  );
  assert.equal(
    formatSharePercent(animatedMetricTarget(99.9, 0, true), 0),
    "100% 미만"
  );
  assert.equal(
    formatSharePercent(animatedMetricTarget(100, 0, true), 0),
    "100%"
  );
});
