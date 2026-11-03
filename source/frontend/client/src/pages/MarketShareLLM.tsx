import { BarChart3, Bot, Database, Scale } from "lucide-react";

import {
  HermesMarketQuestion,
  type EmbeddedMarketQuestionAnswer,
} from "@/components/HermesMarketQuestion";
import { PageHeader } from "@/components/common/PageHeader";
import { MobileHelp } from "@/components/common/MobileHelp";
import type { GroundedMarketQuestionResponse } from "@/lib/market-share-types";

const REPORT_SCOPE = [
  {
    icon: Scale,
    title: "거래소를 바꿔서 질문",
    detail:
      "빗썸·업비트·코인원·코빗·고팍스의 거래금액과 국내 비중을 구분합니다.",
  },
  {
    icon: BarChart3,
    title: "변화의 구조를 진단",
    detail:
      "점유율 수준보다 대상 거래소·국내시장 효과, 주요 코인, 집중 시점을 먼저 짚습니다.",
  },
  {
    icon: Database,
    title: "확인된 자료만 사용",
    detail:
      "질문에 필요한 저장 자료와 현재 자료만 확인하고, 비어 있는 기간은 그대로 알려드립니다.",
  },
] as const;

const PAGE_DESCRIPTION =
  "어느 국내 거래소든 현재 점유율, 기간별 변화, 거래금액, 코인 기여를 질문하면 결론부터 보고서 형태로 분석합니다.";

export default function MarketShareLLM({
  embeddedAnswer,
  embeddedAnswers,
  scopePresentation = "cards",
  mainDashboard = false,
}: {
  embeddedAnswer?: GroundedMarketQuestionResponse;
  embeddedAnswers?: EmbeddedMarketQuestionAnswer[];
  scopePresentation?: "cards" | "help";
  mainDashboard?: boolean;
}) {
  const pageDescription = mainDashboard ? undefined : PAGE_DESCRIPTION;
  return (
    <div className="market-share-llm-page w-full space-y-6">
      <PageHeader
        title="점유율 질문"
        subtitle={pageDescription}
        icon={<Bot className="h-5 w-5" />}
        titleHelp={
          scopePresentation === "help" ? (
            <MobileHelp allSizes label="점유율 질문 사용 안내">
              {pageDescription && <p>{pageDescription}</p>}
              <dl className="mt-4 space-y-4">
                {REPORT_SCOPE.map(({ title, detail }) => (
                  <div key={title}>
                    <dt className="font-semibold">{title}</dt>
                    <dd className="mt-1 text-muted-foreground">{detail}</dd>
                  </div>
                ))}
              </dl>
            </MobileHelp>
          ) : undefined
        }
      />

      {scopePresentation === "cards" && (
        <section
          className="share-llm-scope-grid"
          aria-label="점유율 질문 분석 범위"
        >
          {REPORT_SCOPE.map(({ icon: Icon, title, detail }) => (
            <article key={title}>
              <span aria-hidden="true">
                <Icon className="h-4 w-4" />
              </span>
              <div>
                <div className="mobile-help-heading">
                  <h3>{title}</h3>
                  <MobileHelp label={`${title} 설명`}>{detail}</MobileHelp>
                </div>
                <p className="mobile-description-copy">{detail}</p>
              </div>
            </article>
          ))}
        </section>
      )}

      <HermesMarketQuestion
        standalone
        standaloneAnswer={embeddedAnswer}
        standaloneAnswers={embeddedAnswers}
        mainDashboard={mainDashboard}
        selectedExchange="bithumb"
      />
    </div>
  );
}
