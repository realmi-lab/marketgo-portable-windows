import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import {
  dedupeNoticeRows,
  marketShareTimelineFocusScrollLeft,
  marketShareNoticeIdentity,
  mergeTimelinePeriods,
} from "./market-share-timeline.ts";

test("keeps a notice timeline at the start when all dates fit", () => {
  assert.equal(
    marketShareTimelineFocusScrollLeft({
      fixedWidth: 696,
      timelineWidth: 560,
      viewportWidth: 1280,
      focusLeft: 540,
    }),
    0
  );
});

test("focuses the current day inside the visible timeline area", () => {
  assert.equal(
    marketShareTimelineFocusScrollLeft({
      fixedWidth: 600,
      timelineWidth: 2000,
      viewportWidth: 1000,
      focusLeft: 1000,
    }),
    752
  );
});

test("clamps notice timeline focus to the final scroll position", () => {
  assert.equal(
    marketShareTimelineFocusScrollLeft({
      fixedWidth: 696,
      timelineWidth: 2600,
      viewportWidth: 1132,
      focusLeft: 2590,
    }),
    2164
  );
});

test("unpinned tablet columns keep today centered using full content coordinates", () => {
  for (const outerWidth of [438, 490]) {
    const padding = 16;
    const fixedWidth = 536;
    const focusLeft = 966;
    const scrollLeft = marketShareTimelineFocusScrollLeft({
      fixedWidth,
      timelineWidth: 1652,
      viewportWidth: outerWidth - padding * 2,
      focusLeft,
      fixedColumnsPinned: false,
    });
    assert.equal(padding + fixedWidth + focusLeft - scrollLeft, outerWidth / 2);
  }
});

test("unpinned focus clamps both ends without changing the legacy pinned default", () => {
  const input = {
    fixedWidth: 536,
    timelineWidth: 1652,
    viewportWidth: 406,
    focusLeft: 9999,
  };
  assert.equal(
    marketShareTimelineFocusScrollLeft({ ...input, fixedColumnsPinned: false }),
    1782
  );
  assert.equal(
    marketShareTimelineFocusScrollLeft({
      ...input,
      fixedColumnsPinned: false,
      focusLeft: -9999,
    }),
    0
  );
  assert.equal(
    marketShareTimelineFocusScrollLeft(input),
    marketShareTimelineFocusScrollLeft({ ...input, fixedColumnsPinned: true })
  );
});

test("only the main app's actual static columns opt into full-content centering", () => {
  const source = readFileSync(
    new URL("../components/NoticeTimeline.tsx", import.meta.url),
    "utf8"
  );
  const focus = source.slice(
    source.indexOf("const scrollTimelineToDay ="),
    source.indexOf(
      "  useEffect(() => {",
      source.indexOf("const scrollTimelineToDay =")
    )
  );
  assert.match(focus, /scroller\.closest\("\.main-dashboard"\)/);
  assert.match(
    focus,
    /window\.getComputedStyle\(firstColumn\)\.position === "static"/
  );
  assert.match(focus, /fixedColumnsPinned: !columnsScrollWithDates/);
  assert.match(
    focus,
    /viewportWidth: scroller\.clientWidth - horizontalPadding/
  );
});

test("merges an original event period with a same-day extension", () => {
  assert.deepEqual(
    mergeTimelinePeriods(
      [
        { startMs: 100, endMs: 200, periodCapped: false },
        { startMs: 199, endMs: 300, periodCapped: false },
      ],
      startMs => String(Math.floor(startMs / 100))
    ),
    [{ startMs: 100, endMs: 300, periodCapped: false }]
  );
});

test("keeps periods with different starts as separate timeline entries", () => {
  assert.equal(
    mergeTimelinePeriods([
      { startMs: 100, endMs: 200, periodCapped: false },
      { startMs: 250, endMs: 300, periodCapped: false },
    ]).length,
    2
  );
});

test("deduplicates the same official notice before rendering list or timeline", () => {
  assert.deepEqual(
    dedupeNoticeRows([
      {
        exchange: "upbit",
        notice_id: "6413",
        url: "https://upbit.com/service_center/notice?id=6413",
      },
      {
        exchange: "upbit",
        notice_id: "6413",
        url: "https://upbit.com/service_center/notice?id=6413",
      },
      {
        exchange: "upbit",
        notice_id: "6414",
        url: "https://upbit.com/service_center/notice?id=6414",
      },
    ]).map(row => row.notice_id),
    ["6413", "6414"]
  );
});

test("uses URL only when a source notice ID is absent", () => {
  assert.equal(
    marketShareNoticeIdentity({
      exchange: "bithumb",
      notice_id: "7",
      url: "https://example.test/a",
    }),
    "bithumb:id:7"
  );
  assert.equal(
    marketShareNoticeIdentity({
      exchange: "bithumb",
      notice_id: "",
      url: "https://EXAMPLE.test/a/",
    }),
    "bithumb:url:https://example.test/a"
  );
  assert.equal(
    dedupeNoticeRows([
      { exchange: "bithumb", notice_id: "7", url: "https://example.test/a" },
      { exchange: "bithumb", notice_id: "8", url: "https://example.test/a" },
    ]).length,
    2
  );
});
