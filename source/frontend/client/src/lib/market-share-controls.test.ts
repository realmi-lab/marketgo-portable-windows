import assert from "node:assert/strict";
import test from "node:test";

import { clampPage } from "./market-share-controls.ts";

test("clamps a live table page when rows shrink", () => {
  assert.equal(clampPage(4, 80, 50), 2);
  assert.equal(clampPage(3, 0, 50), 1);
  assert.equal(clampPage(-2, 200, 50), 1);
});
