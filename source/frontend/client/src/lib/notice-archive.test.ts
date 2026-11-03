import assert from "node:assert/strict";
import test from "node:test";
import { setImmediate } from "node:timers/promises";
import {
  attachNoticeArchiveContext,
  loadWholeNoticeArchive,
  normalizeNoticeArchivePage,
  noticeArchivePath,
  type NoticeArchiveRequest,
  type NoticeArchivePage,
} from "./notice-archive.ts";

const envelope = () => ({
  snapshot: {
    version: 8,
    sources: {},
    items: [
      {
        exchange: "bithumb",
        notice_id: "1",
        title: "공식 제목",
        image_urls: null,
      },
    ],
  },
  total: 1,
  offset: 0,
  limit: 20,
  as_of_ms: 3000,
  from_ms: 1000,
});

function archivePage(request: NoticeArchiveRequest, total = 650): NoticeArchivePage {
  const limit = request.limit ?? 100;
  return normalizeNoticeArchivePage({
    ...envelope(), total, offset: request.offset, limit,
    snapshot: { version: 8, sources: {}, items: Array.from(
      { length: Math.min(limit, total - request.offset) },
      (_, index) => ({ exchange: "upbit", notice_id: String(request.offset + index), title: "t" })
    ) },
  });
}

test("whole archive bounds concurrency and preserves order when pages finish backwards", async () => {
  const pending: { request: NoticeArchiveRequest; resolve: (page: NoticeArchivePage) => void }[] = [];
  const offsets: number[] = [];
  let active = 0, peak = 0;
  const loading = loadWholeNoticeArchive(request => {
    offsets.push(request.offset);
    if (!request.offset) return Promise.resolve(archivePage(request));
    active++;
    peak = Math.max(peak, active);
    assert.equal(request.version, 8);
    assert.equal(request.asOfMS, 3000);
    return new Promise<NoticeArchivePage>(resolve => pending.push({
      request, resolve: page => { active--; resolve(page); },
    }));
  });
  await setImmediate();
  assert.deepEqual(offsets, [0, 100, 200, 300]);
  for (const page of pending.splice(0).reverse()) page.resolve(archivePage(page.request));
  await setImmediate();
  assert.deepEqual(offsets, [0, 100, 200, 300, 400, 500, 600]);
  for (const page of pending.splice(0).reverse()) page.resolve(archivePage(page.request));
  const result = await loading;
  assert.equal(peak, 3);
  assert.equal(active, 0);
  assert.deepEqual(result.snapshot.items.map(row => row.notice_id), Array.from({ length: 650 }, (_, i) => String(i)));
});

test("whole archive drains failed waves before allowing a snapshot retry", async () => {
  let resolveSibling!: (page: NoticeArchivePage) => void;
  let settled = false;
  const offsets: number[] = [];
  const loading = loadWholeNoticeArchive(request => {
    offsets.push(request.offset);
    if (request.offset === 100) return Promise.reject(new Error("NOTICE_ARCHIVE_CHANGED"));
    if (request.offset === 200) return new Promise(resolve => { resolveSibling = resolve; });
    return Promise.resolve(archivePage(request));
  });
  const rejected = assert.rejects(loading, /NOTICE_ARCHIVE_CHANGED/).then(() => { settled = true; });
  await setImmediate();
  assert.equal(settled, false);
  resolveSibling(archivePage({ offset: 200 }));
  await rejected;
  assert.deepEqual(offsets, [0, 100, 200, 300]);
});

test("whole archive rejects mixed snapshots and truncated pages", async () => {
  for (const corrupt of [
    (page: NoticeArchivePage) => { page.snapshot.version++; },
    (page: NoticeArchivePage) => { page.as_of_ms++; },
    (page: NoticeArchivePage) => { page.total++; },
    (page: NoticeArchivePage) => { page.snapshot.items.pop(); },
    (page: NoticeArchivePage) => { page.offset++; },
  ]) {
    await assert.rejects(loadWholeNoticeArchive(async request => {
      const page = archivePage(request, 230);
      if (request.offset) corrupt(page);
      return page;
    }), /NOTICE_ARCHIVE_(CHANGED|INCOMPLETE)/);
  }
});
test("archive path pins subsequent pages to one server window and version", () => {
  assert.equal(
    noticeArchivePath({ offset: 120, version: 8, asOfMS: 3000 }),
    "/api/market-share/notices?offset=120&limit=20&version=8&as_of_ms=3000"
  );
  assert.equal(
    noticeArchivePath({ offset: 0 }),
    "/api/market-share/notices?offset=0&limit=20"
  );
});
test("raw archive rows remain visible without inventing market reaction or OCR", () => {
  const raw = envelope();
  const page = normalizeNoticeArchivePage(raw);
  const row = page.snapshot.items[0];
  assert.deepEqual(row.image_urls, []);
  assert.deepEqual(row.image_analysis, []);
  assert.deepEqual(row.market_context, []);
  assert.equal(row.reaction_status, "NOT_EVALUATED");
  assert.equal(row.within_selected_window, false);
  assert.equal(raw.snapshot.items[0].image_urls, null);
});
test("archive rejects truncated and legacy fallback responses instead of claiming no notices", () => {
  for (const value of [
    null,
    {},
    { analysis: {} },
    { ...envelope(), total: 21 },
    { ...envelope(), limit: 101 },
    { ...envelope(), offset: -1 },
    { ...envelope(), from_ms: 4000 },
    { ...envelope(), snapshot: { version: 8, sources: {}, items: [] } },
  ])
    assert.throws(() => normalizeNoticeArchivePage(value));
  const empty = normalizeNoticeArchivePage({
    ...envelope(),
    total: 0,
    snapshot: { version: 8, sources: {}, items: [] },
  });
  assert.equal(empty.total, 0);
});
test("reaction context joins by identity only at the same archive version", () => {
  const page = normalizeNoticeArchivePage(envelope());
  const row = page.snapshot.items[0];
  const measured = {
    ...row,
    title: "must not replace official title",
    reaction_status: "OBSERVED",
    within_selected_window: true,
  };
  assert.equal(
    attachNoticeArchiveContext(page, { version: 7, items: [measured] })[0],
    row
  );
  const joined = attachNoticeArchiveContext(page, {
    version: 8,
    items: [measured],
  })[0];
  assert.equal(joined.title, "공식 제목");
  assert.equal(joined.reaction_status, "OBSERVED");
  assert.equal(joined.within_selected_window, true);
  assert.equal(row.reaction_status, "NOT_EVALUATED");
  assert.equal(
    attachNoticeArchiveContext(page, {
      version: 8,
      items: [{ ...measured, exchange: "upbit" }],
    })[0],
    row
  );
});

test("the whole archive reads every page pinned to the first page's version", async () => {
  const requests: string[] = [];
  const all = Array.from({ length: 230 }, (_, index) => ({ exchange: "upbit", notice_id: String(index), title: "t" }));
  const whole = await loadWholeNoticeArchive(async request => {
    requests.push(noticeArchivePath(request));
    const limit = request.limit ?? 20;
    return normalizeNoticeArchivePage({
      snapshot: { version: 8, sources: {}, items: all.slice(request.offset, request.offset + limit) },
      total: all.length, offset: request.offset, limit, as_of_ms: 3000, from_ms: 1000,
    });
  });
  assert.equal(whole.snapshot.items.length, 230);
  assert.deepEqual(requests, [
    "/api/market-share/notices?offset=0&limit=100",
    "/api/market-share/notices?offset=100&limit=100&version=8&as_of_ms=3000",
    "/api/market-share/notices?offset=200&limit=100&version=8&as_of_ms=3000",
  ]);
});
