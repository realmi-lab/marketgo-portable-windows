export type MarketQuestionReportSectionID =
  "summary" | "comparison" | "structure" | "drivers" | "next_check";

export interface MarketQuestionReportSection {
  id: MarketQuestionReportSectionID;
  title: string;
  body: string;
}

const REPORT_SECTION_SPECS: ReadonlyArray<{
  id: MarketQuestionReportSectionID;
  title: string;
}> = [
  { id: "summary", title: "요약 결론" },
  { id: "comparison", title: "비교 기준과 핵심 수치" },
  { id: "structure", title: "점유율 구조 진단" },
  { id: "drivers", title: "주요 기여 요인" },
  { id: "next_check", title: "판단 한계와 다음 확인" },
];

const SECTION_BY_TITLE = new Map(
  REPORT_SECTION_SPECS.map(section => [section.title, section] as const)
);

function reportHeading(line: string): {
  section: (typeof REPORT_SECTION_SPECS)[number];
  inlineBody: string;
} | null {
  const match = line
    .trim()
    .match(
      /^(?:#{1,6}\s*)?(요약 결론|비교 기준과 핵심 수치|점유율 구조 진단|주요 기여 요인|판단 한계와 다음 확인)(?:\s*[:：]\s*(.*))?$/
    );
  if (!match) return null;
  const section = SECTION_BY_TITLE.get(match[1]);
  return section ? { section, inlineBody: match[2]?.trim() ?? "" } : null;
}

export function parseMarketQuestionReport(
  value: string
): MarketQuestionReportSection[] {
  const normalized = value.replace(/\r\n?/g, "\n").trim();
  if (!normalized) return [];

  const result: MarketQuestionReportSection[] = [];
  let active: MarketQuestionReportSection | null = null;
  const preamble: string[] = [];

  const flush = () => {
    if (!active) return;
    active.body = active.body.trim();
    if (active.body) result.push(active);
    active = null;
  };

  for (const line of normalized.split("\n")) {
    const heading = reportHeading(line);
    if (heading) {
      flush();
      active = {
        id: heading.section.id,
        title: heading.section.title,
        body: heading.inlineBody,
      };
      continue;
    }
    if (active) {
      active.body += `${active.body ? "\n" : ""}${line}`;
    } else {
      preamble.push(line);
    }
  }
  flush();

  if (result.length === 0) {
    const paragraphs = normalized
      .split(/\n{2,}/)
      .map(paragraph => paragraph.trim())
      .filter(Boolean);
    return REPORT_SECTION_SPECS.slice(
      0,
      Math.min(paragraphs.length, REPORT_SECTION_SPECS.length)
    ).map((section, index) => ({
      id: section.id,
      title: section.title,
      body:
        index === REPORT_SECTION_SPECS.length - 1
          ? paragraphs.slice(index).join("\n\n")
          : paragraphs[index],
    }));
  }

  const preambleBody = preamble.join("\n").trim();
  if (preambleBody) {
    const existingSummary = result.find(section => section.id === "summary");
    if (existingSummary) {
      existingSummary.body = `${preambleBody}\n\n${existingSummary.body}`;
    } else {
      result.unshift({
        id: "summary",
        title: "요약 결론",
        body: preambleBody,
      });
    }
  }
  return result;
}
