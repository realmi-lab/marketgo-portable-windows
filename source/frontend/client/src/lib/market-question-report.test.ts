import assert from "node:assert/strict";
import test from "node:test";

import { parseMarketQuestionReport } from "./market-question-report.ts";

test("parses the fixed market-share report sections in order", () => {
  const sections = parseMarketQuestionReport(`요약 결론
빗썸 점유율은 상승했습니다.

비교 기준과 핵심 수치
직전 구간 대비 1.2%p 상승했습니다.

점유율 구조 진단
분자 효과가 더 컸습니다.

주요 기여 요인: XRP 기여가 가장 컸습니다.

판단 한계와 다음 확인
다음 마감 구간을 확인해야 합니다.`);

  assert.deepEqual(
    sections.map(section => section.id),
    ["summary", "comparison", "structure", "drivers", "next_check"]
  );
  assert.equal(sections[3].body, "XRP 기여가 가장 컸습니다.");
});

test("keeps a legacy unsectioned answer as one executive summary", () => {
  assert.deepEqual(
    parseMarketQuestionReport("현재 자료 기준 핵심 답변입니다."),
    [
      {
        id: "summary",
        title: "요약 결론",
        body: "현재 자료 기준 핵심 답변입니다.",
      },
    ]
  );
});

test("turns legacy report paragraphs into visible report sections", () => {
  const sections = parseMarketQuestionReport(
    "핵심 결론입니다.\n\n비교 수치입니다.\n\n대상 거래소와 국내시장 해석입니다."
  );
  assert.deepEqual(
    sections.map(section => section.title),
    ["요약 결론", "비교 기준과 핵심 수치", "점유율 구조 진단"]
  );
});
