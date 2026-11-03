import { useEffect, useLayoutEffect, useRef, type ReactNode } from "react";
import {
  Activity,
  BarChart3,
  Bell,
  ChartNoAxesCombined,
  Coins,
  Database,
  FilePenLine,
  LayoutGrid,
  MessageSquareText,
  Moon,
  PanelsTopLeft,
  Sun,
} from "lucide-react";

import { IconButton } from "@/components/common/IconButton";
import { useLiveStatus } from "@/contexts/LiveStatusContext";
import { useMarketQuestionTask } from "@/contexts/MarketQuestionTaskContext";
import { useTheme } from "@/contexts/ThemeContext";
import type { DashboardSection } from "@/lib/dashboard-navigation";
import { marketShareConnectionLabel } from "@/lib/market-share-labels";

export type { DashboardSection } from "@/lib/dashboard-navigation";

const SECTIONS = [
  { id: "summary", label: "요약", mobileLabel: "요약", icon: BarChart3 },
  {
    id: "share-llm",
    label: "점유율 질문",
    mobileLabel: "점유율 질문",
    icon: MessageSquareText,
  },
  {
    id: "intelligence",
    label: "시장 분석",
    mobileLabel: "시장 분석",
    icon: ChartNoAxesCombined,
  },
  {
    id: "drivers",
    label: "코인별 점유율 기여",
    mobileLabel: "코인 기여",
    icon: Coins,
  },
  {
    id: "events",
    label: "공지·시장 이벤트",
    mobileLabel: "공지·이벤트",
    icon: Bell,
  },
  {
    id: "notice-llm2",
    label: "공지 초안 작성",
    mobileLabel: "공지 작성",
    icon: FilePenLine,
  },
  {
    id: "quality",
    label: "데이터 품질",
    mobileLabel: "데이터 품질",
    icon: Database,
  },
  {
    id: "index",
    label: "거래소 인덱스",
    mobileLabel: "거래소 인덱스",
    icon: LayoutGrid,
  },
  {
    id: "index2",
    label: "코인 인덱스",
    mobileLabel: "코인 인덱스",
    icon: PanelsTopLeft,
  },
] satisfies Array<{
  id: DashboardSection;
  label: string;
  mobileLabel: string;
  icon: typeof BarChart3;
}>;

function liveTime(value: number): string {
  if (value <= 0) return "";
  const date = new Date(value);
  return [date.getHours(), date.getMinutes(), date.getSeconds()]
    .map(part => String(part).padStart(2, "0"))
    .join(":");
}

// Subscribe at the leaf: incoming timestamps must not rerender the navigation.
function QuestionTaskStatus() {
  const { task } = useMarketQuestionTask();
  const label =
    task.status === "pending"
      ? "답변 작성 중"
      : task.status === "completed"
        ? "답변 수신"
        : task.status === "failed"
          ? "질문 확인 필요"
          : "질문 대기";
  return (
    <span
      className="app-question-status rounded-md border border-border px-2.5 py-1.5 text-xs text-muted-foreground"
      role="status"
      aria-live="polite"
    >
      {label}
    </span>
  );
}

function LiveConnectionStatus() {
  const { connection, lastMessageAt } = useLiveStatus();
  return (
    <span
      className={`app-live-status inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs font-medium tabular-nums ${
        connection === "live"
          ? "border-profit/35 bg-profit/10 text-profit"
          : connection === "offline"
            ? "border-loss/35 bg-loss/10 text-loss"
            : "border-gold/35 bg-gold/10 text-gold"
      }`}
    >
      <span
        className="h-1.5 w-1.5 rounded-full bg-current"
        aria-hidden="true"
      />
      <span role="status" aria-live="polite" aria-atomic="true">
        {connection === "connecting"
          ? "연결 중"
          : `연결 ${marketShareConnectionLabel(connection)}`}
      </span>
      {lastMessageAt > 0 && (
        <span className="app-live-time hidden md:inline" aria-hidden="true">
          {liveTime(lastMessageAt)}
        </span>
      )}
    </span>
  );
}

// Screens that open no live connection (index screens read closed hourly
// candles over HTTP; LLM2 is a separate tool) would otherwise show "연결 중".
function StaticStatus({ label }: { label: string }) {
  return (
    <span className="app-question-status rounded-md border border-border px-2.5 py-1.5 text-xs text-muted-foreground">
      {label}
    </span>
  );
}

function ThemeToggle() {
  const { theme, toggleTheme } = useTheme();
  return (
    <IconButton
      onClick={toggleTheme}
      icon={theme === "dark" ? Sun : Moon}
      label={theme === "dark" ? "라이트 테마로 전환" : "다크 테마로 전환"}
      size="lg"
      className="app-theme-toggle md:size-8 md:[&>svg]:size-4"
    />
  );
}

export default function DashboardLayout({
  children,
  activeSection,
  onSectionChange,
}: {
  children: ReactNode;
  activeSection: DashboardSection;
  onSectionChange: (section: DashboardSection) => void;
}) {
  const contentRef = useRef<HTMLElement>(null);
  const mobileNavigationRef = useRef<HTMLElement>(null);
  const previousSectionRef = useRef(activeSection);
  const selectedSection = SECTIONS.find(
    section => section.id === activeSection
  );
  const activeLabel = selectedSection?.label ?? "개요";
  const activeMobileLabel = selectedSection?.mobileLabel ?? activeLabel;
  const fixedSummaryPerspective = activeSection === "summary";

  useLayoutEffect(() => {
    const content = contentRef.current;
    const previousSection = previousSectionRef.current;
    if (!content || previousSection === activeSection) return;

    content.scrollTo({
      top: 0,
      behavior: "instant",
    });
    content.focus({ preventScroll: true });
    previousSectionRef.current = activeSection;
  }, [activeSection]);

  useEffect(() => {
    mobileNavigationRef.current
      ?.querySelector<HTMLElement>('[aria-current="page"]')
      ?.scrollIntoView({
        behavior: "auto",
        block: "nearest",
        inline: "center",
      });
  }, [activeSection]);

  return (
    <div
      className="main-dashboard terminal-shell flex h-dvh overflow-hidden bg-background font-sans text-foreground"
      data-section={activeSection}
      style={{
        paddingTop: "env(safe-area-inset-top, 0px)",
        paddingLeft: "env(safe-area-inset-left, 0px)",
        paddingRight: "env(safe-area-inset-right, 0px)",
      }}
    >
      <button
        type="button"
        className="dashboard-skip-link"
        onClick={() => contentRef.current?.focus({ preventScroll: true })}
      >
        본문으로 바로 가기
      </button>
      <aside className="app-sidebar hidden w-56 shrink-0 flex-col gap-2 border-r border-sidebar-border bg-sidebar px-3 py-6 md:flex">
        <div className="app-sidebar-heading">
          <div className="app-sidebar-copy">
            <strong>시장 점유율</strong>
            <span>국내 거래소 분석</span>
          </div>
        </div>
        <nav className="flex w-full flex-col gap-2" aria-label="화면 전환">
          {SECTIONS.map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              type="button"
              aria-label={label}
              onClick={() => onSectionChange(id)}
              aria-current={activeSection === id ? "page" : undefined}
              className={`app-nav-item relative flex h-10 w-full items-center gap-3 rounded-lg px-3 text-left transition-[color,background-color,box-shadow,transform] duration-150 ease-out focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary active:scale-[0.97] motion-reduce:transition-none motion-reduce:transform-none ${
                activeSection === id
                  ? "bg-primary/10 text-primary shadow-sm"
                  : "text-muted-foreground hover:bg-accent hover:text-foreground"
              }`}
            >
              {activeSection === id && (
                <span
                  className="absolute -right-3 h-5 w-0.5 rounded-full bg-primary"
                  aria-hidden="true"
                />
              )}
              <Icon className="h-5 w-5 shrink-0" aria-hidden="true" />
              <span className="truncate text-sm font-medium">{label}</span>
            </button>
          ))}
        </nav>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <header className="app-topbar z-30 border-b border-border px-4 py-2 md:px-6 md:py-4">
          <div className="app-topbar-primary flex items-center justify-between gap-3">
            <div className="app-topbar-identity flex min-w-0 items-center gap-2">
              <span className="app-desktop-section-title ui-body hidden rounded-md border border-border/60 bg-secondary px-2 py-0.5 font-semibold md:inline">
                {activeLabel}
              </span>
              <div className="app-mobile-context flex shrink-0 items-center justify-between gap-3 md:hidden">
                <p className="app-mobile-section-title min-w-0 text-base font-semibold leading-6">
                  {activeMobileLabel}
                </p>
                {fixedSummaryPerspective && (
                  <span
                    className="shrink-0 text-base font-semibold text-muted-foreground"
                    aria-label="요약은 빗썸 기준"
                  >
                    빗썸 기준
                  </span>
                )}
              </div>
            </div>
            <div className="app-topbar-actions flex shrink-0 items-center gap-2">
              {fixedSummaryPerspective && (
                <span
                  className="hidden rounded-md border border-border bg-secondary px-2 py-1 text-base text-muted-foreground md:inline-flex"
                  aria-label="요약은 빗썸 기준"
                >
                  빗썸 기준
                </span>
              )}
              {activeSection === "share-llm" ? (
                <QuestionTaskStatus />
              ) : activeSection === "index" || activeSection === "index2" ? (
                <StaticStatus label="공식 시간봉 자료" />
              ) : activeSection === "notice-llm2" ? (
                <StaticStatus label="공지 작성 도구" />
              ) : (
                <LiveConnectionStatus />
              )}
              <ThemeToggle />
            </div>
          </div>
        </header>

        <nav
          ref={mobileNavigationRef}
          className="mobile-navigation flex shrink-0 gap-1 overflow-x-auto overscroll-x-contain border-b border-border bg-sidebar px-3 py-2 md:hidden"
          aria-label="화면 전환"
        >
          {SECTIONS.map(({ id, label, mobileLabel, icon: Icon }) => (
            <button
              key={id}
              type="button"
              title={label}
              data-section-target={id}
              aria-label={
                mobileLabel === label ? label : `${mobileLabel} (${label})`
              }
              onClick={() => onSectionChange(id)}
              aria-current={activeSection === id ? "page" : undefined}
              className={`mobile-navigation-item flex min-h-12 shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-lg border px-3 py-2 text-base font-medium transition-[color,background-color,border-color,transform] duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary active:translate-y-px motion-reduce:transition-none motion-reduce:transform-none ${
                activeSection === id
                  ? "border-primary bg-primary/10 text-primary"
                  : "border-transparent text-muted-foreground hover:bg-accent hover:text-foreground"
              }`}
            >
              <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
              <span className="mobile-navigation-label">{mobileLabel}</span>
            </button>
          ))}
        </nav>

        <main
          ref={contentRef}
          tabIndex={-1}
          aria-label={`${activeLabel} 화면`}
          className="dashboard-content min-w-0 flex-1 overflow-y-auto p-4 focus:outline-none sm:p-6"
        >
          <div className="dashboard-page mx-auto min-w-0 w-full">
            {children}
          </div>
        </main>
      </div>
    </div>
  );
}
