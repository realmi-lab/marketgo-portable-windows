import { useEffect, useRef, useState } from "react";

import { subscribeSharedAnimationFrame } from "@/lib/animation-frame-coordinator";
import { animatedMetricTarget } from "@/lib/animated-metric-target";

/**
 * Smooths a completed numeric update over a short, bounded interval. The hook
 * keeps the latest displayed frame as the next animation origin, so a faster
 * live update never jumps back to an older value.
 */
export function useAnimatedMetrics(
  values: Array<number | null | undefined>,
  digits = 2,
  durationMS = 420,
  frameIntervalMS = 32,
  preserveRawValue = false
): Array<number | null> {
  const targets = values.map(value =>
    animatedMetricTarget(value, digits, preserveRawValue)
  );
  const targetKey = targets.join("|");
  const safeDurationMS = Math.max(0, durationMS);
  const safeFrameIntervalMS = Math.max(8, frameIntervalMS);
  const [displayed, setDisplayed] = useState(targets);
  const displayedRef = useRef(targets);

  useEffect(() => {
    const nextTargets = targetKey
      .split("|")
      .map(value => (value === "" ? null : Number(value)));
    const from = displayedRef.current;
    const canAnimate =
      from.length === nextTargets.length &&
      nextTargets.some(
        (target, index) =>
          target !== null &&
          typeof from[index] === "number" &&
          target !== from[index]
      );
    const reducedMotionQuery =
      typeof window === "undefined"
        ? null
        : window.matchMedia("(prefers-reduced-motion: reduce)");
    if (
      !canAnimate ||
      nextTargets.every((target, index) => target === from[index]) ||
      safeDurationMS === 0 ||
      reducedMotionQuery?.matches
    ) {
      displayedRef.current = nextTargets;
      setDisplayed(nextTargets);
      return;
    }

    const startedAt = performance.now();
    let unsubscribeFrame = () => {};
    let lastRenderedAt = 0;
    const finishImmediately = () => {
      unsubscribeFrame();
      displayedRef.current = nextTargets;
      setDisplayed(nextTargets);
    };
    const handleReducedMotionChange = (event: MediaQueryListEvent) => {
      if (event.matches) finishImmediately();
    };
    reducedMotionQuery?.addEventListener("change", handleReducedMotionChange);
    const tick = (now: number) => {
      const progress = Math.min(1, (now - startedAt) / safeDurationMS);
      if (progress === 1 || now - lastRenderedAt >= safeFrameIntervalMS) {
        lastRenderedAt = now;
        const eased = 1 - Math.pow(1 - progress, 3);
        const next = nextTargets.map((target, index) => {
          const start = from[index];
          // Interpolation can lose precision even at eased=1; commit the exact
          // endpoint so the visible value matches its accessible final value.
          return progress === 1 || target === null || start === null
            ? target
            : start + (target - start) * eased;
        });
        displayedRef.current = next;
        setDisplayed(next);
      }
      return progress < 1;
    };
    unsubscribeFrame = subscribeSharedAnimationFrame(tick);
    return () => {
      unsubscribeFrame();
      reducedMotionQuery?.removeEventListener(
        "change",
        handleReducedMotionChange
      );
    };
  }, [safeDurationMS, safeFrameIntervalMS, targetKey]);

  return displayed;
}
