import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const read = (path: string) =>
  readFileSync(new URL(path, import.meta.url), "utf8");
const css = read("../notices.css").replace(/\s+/g, " ");
const page = read("../pages/MarketNotices.tsx");

test("notice reading styles stay within main notice screens", () => {
  assert.match(read("../main.tsx"), /import "\.\/notices\.css"/);
  for (const path of [
    "../index.css",
    "../standalone-main.tsx",
    "../standalone.css",
    "../../../unix_share/src/main.tsx",
    "../../../unix_share/src/base.css",
    "../../../unix_share/src/sampler.css",
  ]) {
    assert.doesNotMatch(read(path), /notices\.css/);
  }
  for (const match of css
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .matchAll(/([^{}]+)\{/g)) {
    const selector = match[1].trim();
    if (selector.startsWith("@media")) continue;
    for (const part of selector.split(/,(?![^()]*\))/)) {
      assert.ok(
        ["events", "notice-llm"].some(section =>
          part.trim().startsWith(`.main-dashboard[data-section="${section}"]`)
        ),
        part
      );
    }
  }
});

test("notices use a full-width reading flow with readable titles and body", () => {
  assert.match(css, /\.notice-official-panel\s*\{[^}]*display: block;/);
  assert.match(
    css,
    /\.notice-list-scroll\s*\{[^}]*max-height: none;[^}]*overflow: visible;/
  );
  assert.match(
    css,
    /\.notice-list-title\s*\{[^}]*white-space: normal;[^}]*font-size: var\(--type-title\);/
  );
  assert.match(
    css,
    /\.notice-list-article\s*\{[^}]*font-size: var\(--type-body\);[^}]*line-height: 1\.75;/
  );
  assert.match(css, /\.notice-pagination\s*\{[^}]*position: sticky;/);
  assert.doesNotMatch(
    page,
    /notice-list-scroll max-h|출처 확인 필요|notice\.inflection_status|line-clamp-5/
  );
});

test("source states, original body and pagination remain available without operational logs", () => {
  assert.match(page, /Object\.entries\(noticeData\.sources\)\.map\(/);
  assert.match(page, /data-notice-source=\{exchange\}/);
  assert.doesNotMatch(page, /noticeData\.error_history\.map\(/);
  assert.match(page, /noticeBodyText\(notice\)/);
  assert.match(page, /visibleNotices\.map\(/);
  assert.match(page, /className="notice-pagination /);
  assert.match(page, /open=\{open\}/);
  assert.match(page, /setOpenNoticeIDs\(current =>/);
});

test("question component remains mounted with the existing request and cancel lifecycle", () => {
  const noticesScreen = read("../screens/NoticesScreen.tsx");
  assert.match(noticesScreen, /lazy\(\(\) => import\("@\/pages\/MarketNotices"\)\)/);
  assert.match(noticesScreen, /<NoticeList desktopWorkspace archiveNotices\s*\/>/);
  assert.match(page, /answerScrollable=\{desktopWorkspace\}/);
  assert.match(page, /compact=\{!llmWorkspace\}/);
  assert.match(
    read("../components/HermesNoticeQuestion.tsx"),
    /requests\.current\.cancel\(\)/
  );
  assert.match(
    css,
    /\.hermes-market-question-error\s*\{[^}]*max-height: none;[^}]*overflow: visible;/
  );
});
