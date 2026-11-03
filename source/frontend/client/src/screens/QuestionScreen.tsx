import MarketShareLLM from "@/pages/MarketShareLLM";
import type { ScreenProps } from "./AppShell";

/** 질문: ask about share and trading; answers keep their evidence and basis. */
export default function QuestionScreen(_props: ScreenProps) {
  return (
    <div className="mg-page">
      <MarketShareLLM scopePresentation="help" mainDashboard />
    </div>
  );
}
