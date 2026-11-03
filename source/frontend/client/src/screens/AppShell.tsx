import {
  lazy,
  startTransition,
  Suspense,
  useEffect,
  useRef,
  useState,
  type ComponentType,
} from "react";
import {
  Activity,
  BarChart3,
  Ellipsis,
  GitCompareArrows,
  LayoutDashboard,
  Moon,
  Pause,
  Play,
  RefreshCw,
  Sun,
} from "lucide-react";

import { useMarketQuestionTask } from "@/contexts/MarketQuestionTaskContext";
import { useTheme } from "@/contexts/ThemeContext";
import { LLM_ENABLED } from "@/lib/app-features";
import {
  screenHash,
  screenRouteFromHash,
  type ScreenId,
  type ScreenRoute,
} from "@/lib/dashboard-navigation";
import { formatKRW, formatSharePercent } from "@/lib/formatters";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import type { MarketShareAnalysis } from "@/lib/market-share-types";
import { flowStatus, windowLabel, type Tone } from "@/lib/screen-copy";
import {
  MarketStreamProvider,
  useMarketStream,
  useShownFlow,
} from "./market-stream";
import { Chip, Empty, LiveValue, Pp } from "./ui";
import { useWindowChanges } from "./data";

export interface ScreenProps {
  focus?: ScreenRoute["focus"];
}

/** A screen's menu name and its code, loaded when first opened. */
function screen(
  label: string,
  load: () => Promise<{ default: ComponentType<ScreenProps> }>
) {
  // Keep prefetch and rendering on the same result. Vite remembers CSS
  // preloads even after failure; trying the loader again could skip that
  // missing stylesheet and render a broken screen without an error.
  let result: ReturnType<typeof load> | undefined;
  const loadOnce = () => (result ??= load());
  return { label, load: loadOnce, component: lazy(loadOnce) };
}

const indexScreen = (name: "ExchangeIndexScreen" | "CoinIndexScreen") => () =>
  import("./IndexScreens").then(module => ({ default: module[name] }));

const SCREENS: Record<ScreenId, ReturnType<typeof screen>> = {
  brief: screen("브리핑", () => import("./BriefScreen")),
  live: screen("실시간", () => import("./LiveScreen")),
  change: screen("점유율 변화", () => import("./ChangeScreen")),
  why: screen("원인", () => import("./WhyScreen")),
  coins: screen("코인", () => import("./CoinsScreen")),
  exchanges: screen("거래소", () => import("./ExchangesScreen")),
  index: screen("거래소 인덱스", indexScreen("ExchangeIndexScreen")),
  index2: screen("코인 인덱스", indexScreen("CoinIndexScreen")),
  "coin-shares": screen("코인 인덱스 2", () => import("./CoinSharesScreen")),
  notices: screen("공지", () => import("./NoticesScreen")),
  question: screen("질문", () => import("./QuestionScreen")),
  status: screen("자료 상태", () => import("./StatusScreen")),
};

/**
 * Loads every screen's code once the first screen is up and the browser is
 * idle, so a tap on the menu opens the next screen at once instead of
 * leaving the current one on view while its code downloads. Skipped when the
 * browser asks to save data.
 */
function usePreloadedScreens() {
  useEffect(() => {
    const connection = (
      navigator as Navigator & { connection?: { saveData?: boolean } }
    ).connection;
    if (connection?.saveData) return;
    const timer = setTimeout(() => {
      const load = () =>
        Object.values(SCREENS).forEach(
          item => void item.load().catch(() => {})
        );
      if ("requestIdleCallback" in window) window.requestIdleCallback(load);
      else load();
    }, 3_000);
    return () => clearTimeout(timer);
  }, []);
}

// The wide-screen menu leaves out the exchange index; phones still open it
// from "더보기", and its address #index still works.
const MENU: ScreenId[] = (
  [
    "brief",
    "live",
    "change",
    "why",
    "coins",
    "exchanges",
    "index2",
    "coin-shares",
    "notices",
    "question",
  ] satisfies ScreenId[]
).filter(id => LLM_ENABLED || id !== "question");

// Phones keep the four reading screens at hand; the rest open from "더보기".
const PHONE_BAR = [
  { id: "brief", label: "브리핑", icon: LayoutDashboard },
  { id: "live", label: "실시간", icon: Activity },
  { id: "change", label: "변화", icon: BarChart3 },
  { id: "why", label: "원인", icon: GitCompareArrows },
] as const satisfies ReadonlyArray<{
  id: ScreenId;
  label: string;
  icon: unknown;
}>;
const PHONE_MORE: ScreenId[] = (
  [
    "coins",
    "exchanges",
    "index",
    "index2",
    "coin-shares",
    "notices",
    "question",
    "status",
  ] satisfies ScreenId[]
).filter(id => LLM_ENABLED || id !== "question");

const NoticeDraft = lazy(() => import("@/pages/NoticeLLM2Workspace"));

function currentScreenRoute(): ScreenRoute {
  const route = screenRouteFromHash(window.location.hash);
  if (LLM_ENABLED) return route;
  if (route.screen === "question") return { screen: "brief" };
  return { screen: route.screen };
}

/**
 * The route on view and the screen the address asks for, which differ while
 * the next screen's code loads: the menu marks the asked-for screen at once.
 */
function useScreenRoute(): { route: ScreenRoute; target: ScreenId } {
  const [route, setRoute] = useState(() => currentScreenRoute());
  const [target, setTarget] = useState(route.screen);
  useEffect(() => {
    // A transition keeps the current screen on view while the next lazy
    // screen loads, instead of flashing the empty placeholder.
    const sync = () => {
      const next = currentScreenRoute();
      setTarget(next.screen);
      startTransition(() => setRoute(next));
    };
    window.addEventListener("hashchange", sync);
    return () => window.removeEventListener("hashchange", sync);
  }, []);
  // An old link (#summary, #drivers…) is rewritten to the address of what it opens.
  const hash = screenHash(route);
  useEffect(() => {
    if (window.location.hash !== hash) {
      window.history.replaceState(null, "", hash);
    }
  }, [hash, route]);
  return { route, target };
}

export default function AppShell() {
  const { route, target } = useScreenRoute();
  usePreloadedScreens();
  return (
    <MarketStreamProvider screen={route.screen}>
      <Shell route={route} target={target} />
    </MarketStreamProvider>
  );
}

function Shell({ route, target }: { route: ScreenRoute; target: ScreenId }) {
  const mainRef = useRef<HTMLElement>(null);
  const firstRender = useRef(true);
  useEffect(() => {
    if (firstRender.current) {
      firstRender.current = false;
      return;
    }
    window.scrollTo({ top: 0 });
    mainRef.current?.focus({ preventScroll: true });
  }, [route.screen]);

  const Screen = SCREENS[route.screen].component;
  const label = SCREENS[route.screen].label;
  return (
    <div className="mg" data-screen={route.screen}>
      {/* A button, not a #link: the address bar holds the screen. */}
      <button
        type="button"
        className="mg-skip"
        onClick={() => mainRef.current?.focus()}
      >
        본문으로 바로 가기
      </button>
      <TopNav active={target} />
      <LiveStrip />
      <StreamNote />
      <main
        ref={mainRef}
        className="mg-main"
        tabIndex={-1}
        aria-label={`${label} 화면`}
        aria-busy={target !== route.screen || undefined}
      >
        <Suspense fallback={<div className="mg-page" aria-busy="true" />}>
          <Screen focus={route.focus} />
        </Suspense>
        {LLM_ENABLED && (
          <NoticeDraftHost
            shown={route.screen === "notices" && route.focus === "notice-draft"}
          />
        )}
      </main>
      <PhoneNav active={target} />
    </div>
  );
}

/**
 * The notice draft tool is a page of its own in a frame, and a frame reloads
 * whenever it leaves the page. Once opened it stays mounted, hidden on other
 * screens, so a draft in progress survives moving around.
 */
function NoticeDraftHost({ shown }: { shown: boolean }) {
  const [opened, setOpened] = useState(shown);
  useEffect(() => {
    if (shown) setOpened(true);
  }, [shown]);
  if (!opened && !shown) return null;
  return (
    <div hidden={!shown}>
      <div className="mg-page mg-page-follow">
        <Suspense fallback={<Empty>불러오는 중입니다.</Empty>}>
          <NoticeDraft />
        </Suspense>
      </div>
    </div>
  );
}

/** How a market question stands while the user reads other screens. */
function useQuestionStatus(): { label: string; tone: Tone } | null {
  const { task } = useMarketQuestionTask();
  if (task.status === "pending")
    return { label: "답변 작성 중", tone: "muted" };
  if (task.status === "completed") return { label: "답변 수신", tone: "ok" };
  if (task.status === "failed")
    return { label: "질문 확인 필요", tone: "warn" };
  return null;
}

/** Phones: four reading screens at hand, the rest behind "더보기". */
function PhoneNav({ active }: { active: ScreenId }) {
  const [moreOpen, setMoreOpen] = useState(false);
  const sheetRef = useRef<HTMLElement>(null);
  const moreRef = useRef<HTMLButtonElement>(null);
  useEffect(() => setMoreOpen(false), [active]);
  // An opened sheet takes the focus; Escape closes it and gives it back.
  useEffect(() => {
    if (moreOpen) sheetRef.current?.querySelector("a")?.focus();
  }, [moreOpen]);
  const question = useQuestionStatus();
  return (
    <>
      {moreOpen ? (
        <nav
          ref={sheetRef}
          className="mg-sheet"
          aria-label="다른 화면"
          onKeyDown={event => {
            if (event.key !== "Escape") return;
            setMoreOpen(false);
            moreRef.current?.focus();
          }}
        >
          <ul>
            {PHONE_MORE.map(id => (
              <li key={id}>
                <a
                  href={`#${id}`}
                  aria-current={active === id ? "page" : undefined}
                  onClick={() => setMoreOpen(false)}
                >
                  {SCREENS[id].label}
                  {id === "question" && question ? ` · ${question.label}` : ""}
                </a>
              </li>
            ))}
          </ul>
        </nav>
      ) : null}
      <nav className="mg-tabbar" aria-label="화면">
        {PHONE_BAR.map(({ id, label, icon: Icon }) => (
          <a
            key={id}
            href={`#${id}`}
            aria-current={active === id ? "page" : undefined}
          >
            <Icon size={20} aria-hidden="true" />
            {label}
          </a>
        ))}
        <button
          ref={moreRef}
          type="button"
          aria-expanded={moreOpen}
          aria-current={PHONE_MORE.includes(active) ? "page" : undefined}
          onClick={() => setMoreOpen(open => !open)}
        >
          <Ellipsis size={20} aria-hidden="true" />
          더보기
          {question && active !== "question" ? (
            <>
              <i className="mg-tab-badge" aria-hidden="true" />
              <span className="mg-sr-only">질문 {question.label}</span>
            </>
          ) : null}
        </button>
      </nav>
    </>
  );
}

function TopNav({ active }: { active: ScreenId }) {
  const { theme, toggleTheme } = useTheme();
  const { connection, data, analysisExpected } = useMarketStream();
  const analysis = data?.analysis;
  const status = dataStatusChip(connection, analysis, analysisExpected);
  return (
    <header className="mg-nav">
      <a className="mg-brand" href="#brief">
        <i aria-hidden="true" />
        MarketGO
      </a>
      <nav className="mg-tabs" aria-label="화면">
        {MENU.map(id => (
          <a
            key={id}
            className="mg-tab"
            href={`#${id}`}
            aria-current={active === id ? "page" : undefined}
          >
            {SCREENS[id].label}
          </a>
        ))}
      </nav>
      <div className="mg-nav-side">
        {LLM_ENABLED && <QuestionChip active={active} />}
        <span
          className="mg-nav-data"
          role="status"
          aria-live="polite"
          aria-atomic="true"
        >
          <Chip tone={status.tone} href="#status">
            <span className="mg-dot" style={{ background: "currentColor" }} />
            {status.label}
          </Chip>
        </span>
        <span className="mg-nav-time mg-num">
          {analysis?.generated_at
            ? `${formatMarketTimeMS(analysis.generated_at * 1000, "time")} 기준`
            : ""}
        </span>
        {toggleTheme ? (
          <button
            type="button"
            className="mg-icon-button"
            onClick={toggleTheme}
            aria-label={theme === "dark" ? "밝은 화면으로" : "어두운 화면으로"}
          >
            {theme === "dark" ? <Sun size={18} /> : <Moon size={18} />}
          </button>
        ) : null}
      </div>
    </header>
  );
}

/** Desktop: a question still being answered stays in sight on other screens. */
function QuestionChip({ active }: { active: ScreenId }) {
  const question = useQuestionStatus();
  return (
    <span className="mg-nav-question" role="status" aria-live="polite">
      {question && active !== "question" ? (
        <Chip tone={question.tone} href="#question">
          {question.label}
        </Chip>
      ) : null}
    </span>
  );
}

function dataStatusChip(
  connection: string,
  analysis: MarketShareAnalysis | undefined,
  analysisExpected: boolean
): { label: string; tone: Tone } {
  if (connection === "offline") return { label: "연결 끊김", tone: "alert" };
  if (connection === "stale") return { label: "갱신 지연", tone: "warn" };
  if (connection === "connecting") return { label: "연결 중", tone: "muted" };
  if (!analysisExpected) return { label: "연결 정상", tone: "ok" };
  // A heartbeat proves the connection, not that any figures have arrived.
  if (!analysis) return { label: "자료 준비 중", tone: "muted" };
  // The notice view carries no window state, so only the connection is known.
  if (!analysis.context) return { label: "연결 정상", tone: "ok" };
  const windowStatus = analysis.context?.selected_window_status;
  if (windowStatus === "PARTIAL" || windowStatus === "UNAVAILABLE") {
    return { label: "자료 일부 누락", tone: "warn" };
  }
  return { label: "자료 정상", tone: "ok" };
}

/**
 * What the stream itself reports: a reconnect (saying whether the figures on
 * screen are the last ones received), a late frame or a server error. It
 * floats over the page so the page never moves when it comes and goes.
 */
export function StreamNote() {
  const { connection, data, error, errorSeverity } = useMarketStream();
  // A quiet stream has no error of its own; the figures stay, marked as old.
  const quiet =
    connection === "stale"
      ? data
        ? "새 자료가 늦어지고 있습니다. 화면의 값은 마지막으로 받은 값입니다."
        : "새 자료가 늦어지고 있습니다."
      : "";
  const message = error || quiet;
  return (
    <div className="mg-stream-slot" role="status" aria-live="polite">
      {message ? (
        <p
          className="mg-stream-note"
          data-tone={errorSeverity === "error" ? "alert" : "warn"}
        >
          {message}
        </p>
      ) : null}
    </div>
  );
}

function ExtendedStripWindows() {
  const changes = useWindowChanges();
  return <>{[600, 1800, 3600].map(seconds => {
    const row = changes.data?.windows.find(item => item.window_sec === seconds);
    const share = row?.current_share_pct ?? null;
    const provisional = row?.status === "PROVISIONAL";
    const title = [
      "10초마다 갱신",
      changes.data ? `${formatMarketTimeMS(changes.data.as_of_second * 1000, "time-seconds")} 기준` : "자료 수신 대기",
      provisional ? "일부 초만 관측한 값(관측범위기준)" : "",
      changes.stale ? "갱신 지연 · 마지막 수신값" : "",
    ].filter(Boolean).join(" · ");
    return <span key={seconds} className="mg-strip-wide" title={title}>
      {windowLabel(seconds)} <b className="mg-num"><LiveValue value={share}>
        {formatSharePercent(share)}{share !== null && (provisional || changes.stale) ? "*" : ""}
      </LiveValue></b>
    </span>;
  })}</>;
}

/** The once-a-second line under the menu, with a pause for reading. */
export function LiveStrip({ onRefresh, refreshing = false, showDetails = true, extendedWindows = false, showTimeZone = false }: { onRefresh?: () => void; refreshing?: boolean; showDetails?: boolean; extendedWindows?: boolean; showTimeZone?: boolean } = {}) {
  const { flow, visible, window, partial } = useShownFlow();
  const { connection, livePaused, setLivePaused } = useMarketStream();
  const shareFor = (seconds: number) =>
    window(seconds)?.current_share_pct ?? null;
  const state = flowStatus(flow?.status);
  const top = visible ? flow?.drivers?.[0] : undefined;
  return (
    <section className="mg-strip" aria-label="실시간 요약">
      <span className="mg-strip-now">
        <span
          className="mg-pulse"
          data-off={
            livePaused || !flow || connection !== "live" ? "" : undefined
          }
        />
        <span className="mg-strip-label">실시간</span>
        <span className="mg-strip-clock mg-num">
          {formatMarketTimeMS(flow?.as_of_ms, "time-seconds")}
          {showTimeZone ? " KST" : ""}
        </span>
      </span>
      {[10, 30, 60].map(seconds => (
        <span
          key={seconds}
          className={seconds === 10 ? undefined : "mg-strip-wide"}
        >
          {seconds === 10 ? (
            <span className="mg-strip-label">빗썸 </span>
          ) : null}
          {windowLabel(seconds)}{" "}
          <b
            className="mg-num"
            title={
              partial(seconds) && shareFor(seconds) !== null
                ? "일부 초만 관측한 값(관측범위기준)"
                : undefined
            }
          >
            <LiveValue value={shareFor(seconds)}>
              {formatSharePercent(shareFor(seconds))}
              {partial(seconds) && shareFor(seconds) !== null ? "*" : ""}
            </LiveValue>
          </b>
        </span>
      ))}
      {extendedWindows && <ExtendedStripWindows />}
      {showDetails && <>
      <Chip tone={state.tone}>{state.label}</Chip>
      <span className="mg-strip-wide">
        {flow?.primary_window_sec ? `${flow.primary_window_sec}초 ` : ""}
        <Pp value={visible ? flow?.share_delta_pp : null} />
      </span>
      {top ? (
        <span className="mg-strip-wide">
          가장 큰 기여 <b>{top.asset}</b> <Pp value={top.contribution_pp} />
        </span>
      ) : null}
      <span className="mg-strip-wide">
        국내 1초{" "}
        <b className="mg-num">
          <LiveValue value={flow?.domestic_volume_1s_krw}>
            {typeof flow?.domestic_volume_1s_krw === "number"
              ? `${formatKRW(flow.domestic_volume_1s_krw)}${visible ? "" : "*"}`
              : "—"}
          </LiveValue>
        </b>
      </span>
      </>}
      <span className="mg-strip-fill" />
      {/* On a phone only the icon shows. A screen reader hears one toggle,
          "일시정지", pressed or not, rather than a name that also changes. */}
      {onRefresh ? (
        <button
          type="button"
          className="mg-button mg-strip-refresh"
          aria-label="인덱스 새로고침"
          aria-busy={refreshing}
          disabled={refreshing}
          onClick={onRefresh}
        >
          <RefreshCw aria-hidden="true" size={16} />
          <span>{refreshing ? "갱신 중…" : "새로고침"}</span>
        </button>
      ) : (
      <button
        type="button"
        className="mg-button mg-strip-pause"
        aria-label="일시정지"
        aria-pressed={livePaused}
        onClick={() => setLivePaused(!livePaused)}
      >
        {livePaused ? (
          <Play aria-hidden="true" size={16} />
        ) : (
          <Pause aria-hidden="true" size={16} />
        )}
        <span className="mg-strip-pause-name" aria-hidden="true">
          {livePaused ? "다시 재생" : "일시정지"}
        </span>
      </button>
      )}
    </section>
  );
}
