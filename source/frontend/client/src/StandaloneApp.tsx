import { startTransition, useEffect, useState } from "react";

import ErrorBoundary from "@/components/ErrorBoundary";
import type { EmbeddedMarketQuestionAnswer } from "@/components/HermesMarketQuestion";
import DashboardLayout, {
  type DashboardSection,
} from "@/components/DashboardLayout";
import { LiveStatusProvider } from "@/contexts/LiveStatusContext";
import { TargetExchangeProvider } from "@/contexts/TargetExchangeContext";
import { ThemeProvider } from "@/contexts/ThemeContext";
import {
  dashboardPageForSection,
  dashboardSectionFromHash,
} from "@/lib/dashboard-navigation";
import MarketIntelligence from "@/pages/MarketIntelligence";
import MarketNotices from "@/pages/MarketNotices";
import MarketShare from "@/pages/MarketShare";
import MarketShareLLM from "@/pages/MarketShareLLM";
import StandaloneMarketShareDashboard from "@/pages/StandaloneMarketShareDashboard";

function sectionFromHash(): DashboardSection {
  return dashboardSectionFromHash(window.location.hash);
}

export default function StandaloneApp({
  questionAnswers,
}: {
  questionAnswers: EmbeddedMarketQuestionAnswer[];
}) {
  const [activeSection, setActiveSection] =
    useState<DashboardSection>(sectionFromHash);

  useEffect(() => {
    const syncSection = () => {
      startTransition(() => setActiveSection(sectionFromHash()));
    };
    window.addEventListener("hashchange", syncSection);
    return () => window.removeEventListener("hashchange", syncSection);
  }, []);

  const changeSection = (section: DashboardSection) => {
    startTransition(() => setActiveSection(section));
    if (window.location.hash !== `#${section}`) {
      window.location.hash = section;
    }
  };
  const activePage = dashboardPageForSection(activeSection);

  return (
    <ErrorBoundary>
      <ThemeProvider defaultTheme="dark" switchable>
        <TargetExchangeProvider>
          <LiveStatusProvider>
            <DashboardLayout
              activeSection={activeSection}
              onSectionChange={changeSection}
            >
              {activePage === "events" ? (
                <MarketNotices />
              ) : activePage === "share-llm" ? (
                <MarketShareLLM embeddedAnswers={questionAnswers} />
              ) : activePage === "summary" ? (
                <StandaloneMarketShareDashboard />
              ) : activePage === "intelligence" ? (
                <MarketIntelligence />
              ) : (
                <MarketShare
                  view={
                    activeSection === "drivers" ||
                    activeSection === "quality"
                      ? activeSection
                      : "drivers"
                  }
                />
              )}
            </DashboardLayout>
          </LiveStatusProvider>
        </TargetExchangeProvider>
      </ThemeProvider>
    </ErrorBoundary>
  );
}
