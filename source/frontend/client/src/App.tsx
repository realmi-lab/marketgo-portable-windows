import { lazy, Suspense } from "react";

import ErrorBoundary from "@/components/ErrorBoundary";
import { LiveStatusProvider } from "@/contexts/LiveStatusContext";
import { MarketQuestionTaskProvider } from "@/contexts/MarketQuestionTaskContext";
import { TargetExchangeProvider } from "@/contexts/TargetExchangeContext";
import { ThemeProvider } from "@/contexts/ThemeContext";
import { isNoticePortal } from "@/lib/notice-portal";
import AppShell from "@/screens/AppShell";

const PortableShell = lazy(() => import("@/screens/PortableShell"));
const StandaloneShell = lazy(() => import("@/screens/StandaloneShell"));
const NoticePortal = lazy(() => import("@/pages/NoticePortal"));

export default function App() {
  const portal = isNoticePortal(
    window.location.hostname,
    window.location.pathname
  );
  return (
    <ErrorBoundary>
      <ThemeProvider defaultTheme="dark" switchable>
        <TargetExchangeProvider>
          <LiveStatusProvider>
            {portal ? (
              <Suspense
                fallback={
                  <div
                    className="dashboard-route-placeholder"
                    aria-hidden="true"
                  />
                }
              >
                <NoticePortal />
              </Suspense>
            ) : (
              <MarketQuestionTaskProvider>
                {(import.meta.env.VITE_PORTABLE_PYTHON === "true" || import.meta.env.VITE_PORTABLE_POWERSHELL === "true") ? <Suspense fallback={null}><StandaloneShell /></Suspense> : import.meta.env.VITE_PORTABLE_DESKTOP === "true" ? <Suspense fallback={null}><PortableShell /></Suspense> : <AppShell />}
              </MarketQuestionTaskProvider>
            )}
          </LiveStatusProvider>
        </TargetExchangeProvider>
      </ThemeProvider>
    </ErrorBoundary>
  );
}
