import assert from "node:assert/strict";
import test from "node:test";

import type { GroundedMarketQuestionResponse } from "./market-share-types.ts";
import {
  buildMarketQuestionMarkdownDocument,
  humanizeMarketQuestionMarkdown,
} from "./market-question-markdown.ts";

function answer(
  prose: GroundedMarketQuestionResponse["prose"]
): GroundedMarketQuestionResponse {
  return { prose } as GroundedMarketQuestionResponse;
}

test("replaces internal market status and implementation terms in reports", () => {
  const result = humanizeMarketQuestionMarkdown(
    "## 상태\nUNAVAILABLE(DEGRADED)이며 WebSocket coverage gap을 확인했습니다."
  );
  assert.equal(
    result,
    "## 상태\n확인 불가(일부 지연)이며 실시간 체결 자료 확보 범위 자료 공백을 확인했습니다."
  );
  for (const forbidden of [
    "UNAVAILABLE",
    "DEGRADED",
    "WebSocket",
    "coverage",
    "gap",
  ]) {
    assert.doesNotMatch(result, new RegExp(forbidden, "i"));
  }
});

test("keeps bold market values renderable before Korean particles", () => {
  assert.equal(
    humanizeMarketQuestionMarkdown(
      "현재 점유율은 **35.87%**입니다. 거래대금은 __8161억원__입니다."
    ),
    "현재 점유율은 **35.87%** 입니다. 거래대금은 __8161억원__ 입니다."
  );
});

test("repairs spaces inside emphasis markers from generated reports", () => {
  assert.equal(
    humanizeMarketQuestionMarkdown(
      "**35.23%** 에서 **\u00a036.17%** 로 **\n+0.93%p** 변했고 **\u200B1회**였으며 분모인 전체 시장이 줄었습니다."
    ),
    "**35.23%** 에서 **36.17%** 로 **+0.93%p** 변했고 **1회** 였으며 분모인 전체 시장이 줄었습니다."
  );
});

test("preserves asset, within-exchange, and domestic denominator explanations", () => {
  for (const text of [
    "BTC 포착률은 국내 BTC 거래금액을 분모로 계산합니다.",
    "빗썸 내부 BTC 비중은 빗썸 전체 거래금액을 분모로 계산합니다.",
    "전체 점유율 기여는 국내 전체 거래금액을 분모로 계산합니다.",
    "분모인 전체 시장이 줄어 점유율이 상승했습니다.",
  ]) {
    assert.equal(humanizeMarketQuestionMarkdown(text), text);
  }
});

test("copied reports include otherwise hidden key points and interpretation limits", () => {
  assert.equal(
    buildMarketQuestionMarkdownDocument(
      " 빗썸 보고서 ",
      answer({
        answer: "## 결론\n점유율은 **35.00%** 입니다.",
        key_points: ["국내 전체 거래금액 변화와 함께 봐야 합니다."],
        caveat: "공지의 영향이라고 단정할 수 없습니다.",
      })
    ),
    "# 빗썸 보고서\n\n## 결론\n점유율은 **35.00%** 입니다.\n\n## 핵심 판단 근거\n\n- 국내 전체 거래금액 변화와 함께 봐야 합니다.\n\n## 사용 데이터와 해석 범위\n\n공지의 영향이라고 단정할 수 없습니다."
  );
});

test("copied reports omit exact repeated body statements and repeated supplemental points", () => {
  const result = buildMarketQuestionMarkdownDocument(
    "보고서",
    answer({
      answer:
        "## 결론\n\n점유율은 **35.00%** 입니다.\n\n- 이미 설명한 근거입니다.",
      key_points: [
        "점유율은 35.00% 입니다.",
        "이미 설명한 근거입니다.",
        "추가로 확인한 근거입니다.",
        "추가로 확인한 근거입니다.",
        "점유율은 35.01% 입니다.",
      ],
      caveat: "이미 설명한 근거입니다.",
    })
  );
  assert.equal((result.match(/이미 설명한 근거입니다\./g) ?? []).length, 1);
  assert.equal((result.match(/추가로 확인한 근거입니다\./g) ?? []).length, 1);
  assert.equal((result.match(/35\.00%/g) ?? []).length, 1);
  assert.match(result, /35\.01%/);
  assert.doesNotMatch(result, /## 사용 데이터와 해석 범위/);
});

test("report deduplication never drops a shorter but different qualification", () => {
  const result = buildMarketQuestionMarkdownDocument(
    "보고서",
    answer({
      answer: "전체 기간을 확인할 수 없습니다.",
      key_points: ["확인할 수 없습니다."],
      caveat: "확인 가능한 구간만 설명합니다.",
    })
  );
  assert.match(result, /- 확인할 수 없습니다\./);
  assert.match(result, /확인 가능한 구간만 설명합니다\./);
});

test("empty supplements preserve the complete original report body and input", () => {
  const prose = Object.freeze({
    answer: "## 상세 분석\n\n" + "확인된 근거를 설명합니다.\n".repeat(3_000),
    key_points: [],
    caveat: " ",
  });
  const response = answer(prose);
  const result = buildMarketQuestionMarkdownDocument("보고서", response);
  assert.equal(result, `# 보고서\n\n${prose.answer.trim()}`);
  assert.strictEqual(response.prose, prose);
  assert.doesNotMatch(result, /## 핵심 판단 근거|## 사용 데이터와 해석 범위/);
});

test("does not pair one bold value closing marker with the next opening marker", () => {
  assert.equal(
    humanizeMarketQuestionMarkdown(
      "최저 **35%**, 최고 **36%**였습니다. 상승 **1회**·하락 **0회**·보합 **0회**였고, **HIVE -0.02%p**와 **SNT -0.02%p**입니다."
    ),
    "최저 **35%**, 최고 **36%** 였습니다. 상승 **1회**·하락 **0회**·보합 **0회** 였고, **HIVE -0.02%p** 와 **SNT -0.02%p** 입니다."
  );
});
