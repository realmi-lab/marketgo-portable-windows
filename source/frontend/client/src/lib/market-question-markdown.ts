import type { GroundedMarketQuestionResponse } from "./market-share-types.ts";

const USER_FACING_TERMS: ReadonlyArray<readonly [RegExp, string]> = [
  [/\bUNAVAILABLE\b/gi, "확인 불가"],
  [/\bDEGRADED\b/gi, "일부 지연"],
  [/\bPARTIAL\b/gi, "일부 자료만 확인"],
  [/\bPROVISIONAL\b/gi, "확인 중인 자료"],
  [/\bWARMING\b/gi, "자료 준비 중"],
  [/\bCONFIRMED\b/gi, "확인됨"],
  [/\bCOMPLETE\b/gi, "확보 완료"],
  [/\bLIVE\b/gi, "정상 상태"],
  [/\bWATCH\b/gi, "관찰 필요"],
  [/\bALERT\b/gi, "주의 필요"],
  [/\bNORMAL\b/gi, "일반"],
  [/\bWebSocket\b/gi, "실시간 체결"],
  [/\bPostgreSQL\b/gi, "저장된 시장 자료"],
  [/\bserver_evidence\b/gi, "서버가 확인한 근거"],
  [/\btarget_exchange\b/gi, "대상 거래소"],
  [/\bquery\.exchange\b/gi, "질문에서 지정한 거래소"],
  [/\bcoverage\b/gi, "자료 확보 범위"],
  [/\bgap\b/gi, "자료 공백"],
];

export function humanizeMarketQuestionMarkdown(value: string): string {
  const humanized = USER_FACING_TERMS.reduce(
    (current, [pattern, replacement]) => current.replace(pattern, replacement),
    value
  )
    // A model can occasionally put a space immediately inside an opening
    // emphasis marker (`** 36.17%**`). CommonMark treats that marker as plain
    // text, so normalize it before applying the Korean word-boundary rule.
    .replace(/\*\*[\s\u200B\uFEFF]+(?=\S)/gu, "**")
    .replace(/__[\s\u200B\uFEFF]+(?=\S)/gu, "__");
  // CommonMark does not close strong emphasis when a punctuation-ending value
  // is immediately followed by a Korean particle, for example
  // `**35.87%**입니다`. Insert the missing word boundary so the report preview
  // does not expose raw Markdown markers. Require an actual opening boundary;
  // otherwise the closing marker of one value can be paired with the opening
  // marker of the next value across punctuation such as `**1회**·하락 **0회**`.
  return humanized
    .replace(/(^|[\s([{>"'])\*\*([^*\n]+)\*\*(?=[\p{L}\p{N}])/gu, "$1**$2** ")
    .replace(/(^|[\s([{>"'])__([^_\n]+)__(?=[\p{L}\p{N}])/gu, "$1__$2__ ");
}

export function humanizeMarketQuestionPlainText(value: string): string {
  return humanizeMarketQuestionMarkdown(value)
    .replace(/\*\*|__/g, "")
    .replace(/`([^`]*)`/g, "$1");
}

export function buildMarketQuestionMarkdownDocument(
  title: string,
  answer: GroundedMarketQuestionResponse
): string {
  const body = humanizeMarketQuestionMarkdown(answer.prose.answer).trim();
  // Compare complete statements, never a substring or a numerical similarity.
  // Formatting-only differences may repeat an existing sentence, but a new
  // qualification or a different value must remain in the copied report.
  const comparisonText = (value: string) =>
    humanizeMarketQuestionPlainText(value)
      .replace(/^\s*(?:#{1,6}\s+|[-+*]\s+|\d+[.)]\s+)/gm, "")
      .replace(/\s+/gu, " ")
      .trim();
  const known = new Set(
    [
      body,
      ...body.split(/\n\s*\n/u),
      ...body.split(/\n/u),
      ...body.split(/(?<=[.!?。！？])\s+/u),
    ]
      .map(comparisonText)
      .filter(Boolean)
  );
  const distinctText = (value: string): string | null => {
    const text = humanizeMarketQuestionMarkdown(value).trim();
    const identity = comparisonText(text);
    if (!identity || known.has(identity)) return null;
    known.add(identity);
    return text;
  };
  const points = answer.prose.key_points
    .map(distinctText)
    .filter((point): point is string => point !== null);
  const caveat = distinctText(answer.prose.caveat);
  const sections = [`# ${title.trim()}`, body];
  if (points.length > 0) {
    sections.push(
      "## 핵심 판단 근거",
      points.map(point => `- ${point}`).join("\n")
    );
  }
  if (caveat) sections.push("## 사용 데이터와 해석 범위", caveat);
  return sections.filter(Boolean).join("\n\n");
}
