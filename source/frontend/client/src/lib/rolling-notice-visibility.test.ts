import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import type { MarketShareNoticeIdentity } from "./market-share-timeline.ts";
import { selectRelevantRollingNotices } from "./rolling-notice-visibility.ts";

type NoticeRow = MarketShareNoticeIdentity & {
  related: boolean;
  label: string;
  content_priority_score?: number;
  active?: boolean;
};

function notice(
  label: string,
  overrides: Partial<NoticeRow> = {}
): NoticeRow {
  return {
    exchange: "upbit",
    notice_id: label,
    related: false,
    label,
    ...overrides,
  };
}

function assertRows(actual: NoticeRow[], expected: readonly NoticeRow[]) {
  assert.equal(actual.length, expected.length);
  expected.forEach((row, index) => assert.strictEqual(actual[index], row));
}

test("empty evidence stays empty even when the timeline contains a notice", () => {
  assertRows(selectRelevantRollingNotices<NoticeRow>([], []), []);
  assertRows(selectRelevantRollingNotices([], [notice("timeline-only")]), []);
});

test("server relevance includes either publisher without treating every Bithumb notice as relevant", () => {
  const unrelatedBithumb = notice("bithumb-unrelated", { exchange: "bithumb" });
  const relatedUpbit = notice("upbit-related", { related: true });
  const unrelatedUpbit = notice("upbit-unrelated");
  const relatedBithumb = notice("bithumb-related", {
    exchange: "bithumb",
    related: true,
  });
  assertRows(
    selectRelevantRollingNotices(
      [unrelatedBithumb, relatedUpbit, unrelatedUpbit, relatedBithumb],
      []
    ),
    [relatedUpbit, relatedBithumb]
  );
});

test("activity and priority do not substitute for relevance or rendered timeline membership", () => {
  const active = notice("active", { active: true });
  const priority = notice("priority", { content_priority_score: 100 });
  assertRows(selectRelevantRollingNotices([active, priority], []), []);
});

test("only boolean true is accepted as the server relevance flag", () => {
  const truthyString = notice("string", {
    related: "false" as unknown as boolean,
  });
  const truthyNumber = notice("number", {
    related: 1 as unknown as boolean,
  });
  const related = notice("boolean", { related: true });
  assertRows(
    selectRelevantRollingNotices([truthyString, truthyNumber, related], []),
    [related]
  );
});

test("the actual rendered timeline object remains visible even without an identity", () => {
  const rendered = notice("rendered", { notice_id: null, url: null });
  const other = notice("other", { notice_id: null, url: null });
  assertRows(
    selectRelevantRollingNotices([other, rendered], [rendered]),
    [rendered]
  );
});

test("a matching nonnull timeline identity selects the original evidence object", () => {
  const original = notice("original", { exchange: " UPBIT ", notice_id: "42 " });
  const rendered = notice("rendered copy", { notice_id: "42" });
  const result = selectRelevantRollingNotices([original], [rendered]);
  assertRows(result, [original]);
  assert.notStrictEqual(result[0], rendered);
  assert.equal(original.related, false);
});

test("the same notice ID on another exchange is not timeline membership", () => {
  const upbit = notice("upbit", { notice_id: "42" });
  const bithumb = notice("bithumb", { exchange: "bithumb", notice_id: "42" });
  assertRows(
    selectRelevantRollingNotices([bithumb, upbit], [{ ...upbit }]),
    [upbit]
  );
});

test("official URL identity is used when the notice ID is unavailable", () => {
  const original = notice("original", {
    notice_id: null,
    url: "https://UPBIT.COM/notice/42/",
  });
  const rendered = notice("rendered", {
    notice_id: " ",
    url: " https://upbit.com/notice/42 ",
  });
  assertRows(selectRelevantRollingNotices([original], [rendered]), [original]);
});

test("URL membership preserves exchange and query identity rather than matching a whole notice page", () => {
  const selected = notice("selected", {
    notice_id: null,
    url: "https://example.test/notice?id=42",
  });
  const differentQuery = notice("different query", {
    notice_id: null,
    url: "https://example.test/notice?id=43",
  });
  const differentExchange = { ...selected, exchange: "bithumb" };
  assertRows(
    selectRelevantRollingNotices(
      [differentQuery, differentExchange, selected],
      [{ ...selected }]
    ),
    [selected]
  );
});

test("an explicit notice ID takes precedence over a shared URL", () => {
  const selected = notice("selected", {
    notice_id: "42",
    url: "https://example.test/notices",
  });
  const other = notice("other", {
    notice_id: "43",
    url: selected.url,
  });
  assertRows(
    selectRelevantRollingNotices([other, selected], [{ ...selected }]),
    [selected]
  );
});

test("null identities never match unrelated copies or missing-exchange rows", () => {
  const unknown = notice("unknown", { notice_id: null, url: null });
  const blank = notice("blank", { notice_id: " ", url: " " });
  const missingExchange = notice("missing exchange", { exchange: " " });
  assertRows(
    selectRelevantRollingNotices(
      [unknown, blank, missingExchange],
      [{ ...unknown }, { ...missingExchange }]
    ),
    []
  );
});

test("duplicate identities collapse to the first eligible original without merging exchanges", () => {
  const first = notice("first", { notice_id: "42", related: true });
  const duplicate = notice("duplicate", { notice_id: "42", related: true });
  const otherExchange = notice("other exchange", {
    exchange: "bithumb",
    notice_id: "42",
    related: true,
  });
  assertRows(
    selectRelevantRollingNotices(
      [first, first, duplicate, otherExchange],
      [duplicate, duplicate]
    ),
    [first, otherExchange]
  );
});

test("distinct related rows without identities are not incorrectly merged", () => {
  const first = notice("first", { notice_id: null, related: true });
  const second = notice("second", { notice_id: null, related: true });
  assertRows(selectRelevantRollingNotices([first, second], []), [first, second]);
});

test("selection preserves evidence order, original references, and frozen inputs", () => {
  const first = Object.freeze(notice("first", { related: true }));
  const excluded = Object.freeze(notice("excluded"));
  const second = Object.freeze(notice("second"));
  const third = Object.freeze(notice("third", { related: true }));
  const notices = Object.freeze([first, excluded, second, third]);
  const timelineNotices = Object.freeze([third, second]);
  const before = JSON.stringify({ notices, timelineNotices });
  const result = selectRelevantRollingNotices(notices, timelineNotices);
  assertRows(result, [first, second, third]);
  assert.notStrictEqual(result, notices);
  assert.equal(JSON.stringify({ notices, timelineNotices }), before);
});

const read = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");

test("keeps the relevant-notice list opt-in after the briefing replaced the old summary", () => {
  assert.doesNotMatch(read("../App.tsx"), /MarketShareDashboard/);
  const unixApp = read("../../../unix_share/src/App.tsx");
  assert.match(unixApp, /<MarketShareDashboard\s*\/>/);
  assert.doesNotMatch(unixApp, /relatedNoticesOnly/);
});

test("dashboard and rolling summary retain opt-out defaults and forward the list policy", () => {
  const dashboard = read("../pages/MarketShareDashboard.tsx");
  const rolling = read("../pages/RollingShareSummary2.tsx");
  for (const [name, source] of [
    ["MarketShareDashboard", dashboard],
    ["RollingShareSummary2", rolling],
  ]) {
    assert.match(
      source,
      new RegExp(`function ${name}\\(\\{\\s*relatedNoticesOnly = false`)
    );
    assert.match(source, /relatedNoticesOnly\?: boolean/);
  }
  assert.match(
    dashboard,
    /<RollingShareSummary2\s+relatedNoticesOnly=\{relatedNoticesOnly\}\s+preciseHourlyMetrics=\{preciseHourlyMetrics\}\s*\/>/
  );
  for (const component of ["Summary2Page", "NoticeEvidence"]) {
    assert.match(
      rolling,
      new RegExp(`<${component}\\b[^>]*relatedNoticesOnly=\\{relatedNoticesOnly\\}`)
    );
  }
});

test("the list policy uses actual rendered timeline rows before per-exchange preview limits", () => {
  const rolling = read("../pages/RollingShareSummary2.tsx");
  assert.match(
    rolling,
    /relatedNoticesOnly\s*\? selectRelevantRollingNotices\(\s*notices,\s*timeline\.rows\.map\(row => row\.notice\)\s*\)\s*: notices/
  );
  assert.match(
    rolling,
    /notices: visibleNotices\s*\.filter\(notice => notice\.exchange === exchange\.id\)\s*\.slice\(0, NOTICE_PREVIEW_LIMIT\)/
  );
});
