import assert from "node:assert/strict";
import test from "node:test";
import { helpPopoverPosition } from "./help-popover-position.ts";
import { MOBILE_VIEWPORT } from "../../../browser-viewport.ts";

const phone = { left: 0, top: 0, ...MOBILE_VIEWPORT };

test("help below a top trigger stays centered inside the phone", () => {
  assert.deepEqual(
    helpPopoverPosition(
      { left: 10, top: 40, bottom: 84, width: 44 },
      phone,
      160
    ),
    { left: 12, top: 92, width: 320, maxHeight: 820 }
  );
});

test("bottom help opens above its trigger instead of below the screen", () => {
  const result = helpPopoverPosition(
    { left: 340, top: 760, bottom: 804, width: 44 },
    phone,
    240
  );
  assert.equal(result.left, 58);
  assert.equal(result.top, 512);
  assert.ok(result.top + 240 <= phone.height - 12);
});

test("long help is height bounded and leaves its text available for scrolling", () => {
  const result = helpPopoverPosition(
    { left: 180, top: 740, bottom: 784, width: 44 },
    phone,
    1400
  );
  assert.equal(result.top, 12);
  assert.equal(result.maxHeight, 820);
});

test("keyboard and zoom visible viewport offsets constrain all popup edges", () => {
  const viewport = { left: 30, top: 220, width: 300, height: 280 };
  const result = helpPopoverPosition(
    { left: 370, top: 650, bottom: 694, width: 44 },
    viewport,
    500
  );
  assert.deepEqual(result, { left: 42, top: 232, width: 276, maxHeight: 256 });
});

test("offscreen anchor and a tiny viewport cannot produce negative popup size", () => {
  const result = helpPopoverPosition(
    { left: -300, top: -100, bottom: -56, width: 44 },
    { left: 0, top: 0, width: 16, height: 18 },
    40
  );
  assert.deepEqual(result, { left: 8, top: 8, width: 0, maxHeight: 2 });
});
