import assert from "node:assert/strict";
import test from "node:test";

import { createAnimationFrameCoordinator } from "./animation-frame-coordinator.ts";

test("shares one native animation frame across subscribers", () => {
  const pending = new Map<number, FrameRequestCallback>();
  const cancelled: number[] = [];
  let nextHandle = 1;
  const subscribe = createAnimationFrameCoordinator({
    request: callback => {
      const handle = nextHandle++;
      pending.set(handle, callback);
      return handle;
    },
    cancel: handle => {
      cancelled.push(handle);
      pending.delete(handle);
    },
  });
  const frames: string[] = [];

  subscribe(now => {
    frames.push(`first:${now}`);
    return false;
  });
  const unsubscribeSecond = subscribe(now => {
    frames.push(`second:${now}`);
    return now < 20;
  });

  assert.equal(pending.size, 1);
  const firstFrame = [...pending.entries()][0];
  pending.delete(firstFrame[0]);
  firstFrame[1](10);
  assert.deepEqual(frames, ["first:10", "second:10"]);
  assert.equal(pending.size, 1);

  unsubscribeSecond();
  assert.equal(pending.size, 0);
  assert.deepEqual(cancelled, [2]);
});
