import { useEffect, useState } from "react";
import { Moon, Sun } from "lucide-react";
import { useTheme } from "@/contexts/ThemeContext";
import { LiveExchangeChartContent } from "@/components/LiveExchangeChart";
import {
  LiveChartControls,
} from "@/components/LiveChartNavigation";
import { useLiveOverviewHistory } from "@/hooks/useLiveOverviewHistory";
import { windowLabel } from "@/lib/screen-copy";
import { useLiveChartExplorer } from "@/hooks/useLiveChartExplorer";
import type { LiveChartHistoryPoint } from "@/lib/live-chart";
import { appendPortableOfficialShare } from "@/lib/portable-share-history";
import { ANALYSIS_WINDOWS } from "@/lib/market-share-config";
import { Index2FilterButton } from "@/pages/Index2FilterButton";
import MarketIndex2 from "@/pages/MarketIndex2";
import { LiveStrip, StreamNote } from "./AppShell";
import { CompositionCard, ExchangeChangeCard } from "./shared";
import { MarketStreamProvider, useMarketStream, useLiveTick } from "./market-stream";
import "./portable.css";

export default function PortableShell() {
  useEffect(() => {
    const previousTitle = document.title;
    document.title = "MarketGO Portable";
    return () => { document.title = previousTitle; };
  }, []);
  return (
    <MarketStreamProvider screen="exchanges" initialWindowSec={10}>
      <PortableContent />
    </MarketStreamProvider>
  );
}
function PortableContent() {
  const { windowSec } = useMarketStream();
  const [selectedExchange, setSelectedExchange] = useState("all");
  const [exchangeRequestKey, setExchangeRequestKey] = useState(0);
  const selectExchange = (exchange: string) => {
    setSelectedExchange(current => current === exchange ? "all" : exchange);
    setExchangeRequestKey(value => value + 1);
  };
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [indexRefreshing, setIndexRefreshing] = useState(false);
  const [toolbarHost, setToolbarHost] = useState<HTMLDivElement | null>(null);
  const { theme, toggleTheme } = useTheme();
  return (
    <div className="mg mg-portable" data-screen="portable">
      <header className="portable-header">
        <strong>MarketGO Portable</strong>
        <div className="portable-index-toolbar" ref={setToolbarHost} />
        <button className="portable-theme-button" type="button" onClick={toggleTheme} aria-label={theme === "dark" ? "밝은 화면으로" : "어두운 화면으로"} title={theme === "dark" ? "밝은 화면으로" : "어두운 화면으로"}>
          {theme === "dark" ? <Sun size={18} aria-hidden="true" /> : <Moon size={18} aria-hidden="true" />}
        </button>
      </header>
      <LiveStrip showDetails={false} extendedWindows onRefresh={() => setRefreshVersion(value => value + 1)} refreshing={indexRefreshing} />
      <StreamNote />
      <main className="portable-columns" aria-label="포터블 대시보드">
        <section className="portable-index" aria-label="코인 인덱스">
          <MarketIndex2
            toolbarHost={toolbarHost}
            realtimeWindow={windowSec}
            exchangeControl={{ value: selectedExchange, onChange: setSelectedExchange, requestKey: exchangeRequestKey }}
            refreshVersion={refreshVersion}
            onLoadingChange={setIndexRefreshing}
            showVisual={false}
            showStatus={false}
            showShareSort={false}
            showNoticeFilter={false}
            showTableIntro={false}
            defaultDecimals={1}
            showAmountUnit
            showCompositionInitials
            fitColumns
            lookbackDays={8}
            storageNote=""
          />
        </section>
        <section className="portable-right" aria-label="거래소 점유율">
          <CompositionCard showCoverageNote={false} span={12} size="fit" windowPicker={<PortableWindowPicker />} selectedExchange={selectedExchange} onSelectExchange={selectExchange} />
          <ExchangeShare />
          <ExchangeChangeCard span={12} size="fit" />
        </section>
      </main>
    </div>
  );
}
function PortableWindowPicker() {
  const { windowSec, setWindowSec } = useMarketStream();
  return <div className="portable-window-picker">
    <Index2FilterButton
      label="분석 구간"
      triggerLabel={windowLabel(windowSec)}
      value={String(windowSec)}
      options={ANALYSIS_WINDOWS.map(window => [String(window.seconds), window.label] as const)}
      onChange={value => setWindowSec(Number(value))}
    />
  </div>;
}
function ExchangeShare() {
  const { data, connection, livePaused, windowSec } = useMarketStream();
  const tick = useLiveTick();
  const observed = useLiveOverviewHistory({ tick, connection, marketScope: "KRW", requestedWindowSec: windowSec });
  const official = data?.analysis?.official_domestic_24h;
  const [history, setHistory] = useState<LiveChartHistoryPoint[]>([]);
  useEffect(() => {
    setHistory(old => appendPortableOfficialShare(old, official, connection, livePaused));
  }, [official, connection, livePaused]);
  return (
    <ShareChart
      key={windowSec}
      history={windowSec === 86400 ? history : observed}
      windowSec={windowSec}
      receiving={windowSec === 86400 ? (connection === "live" || connection === "stale") && official?.status === "LIVE" && !livePaused : connection === "live" && !livePaused}
    />
  );
}
function ShareChart({
  history,
  windowSec,
  receiving,
}: {
  history: LiveChartHistoryPoint[];
  windowSec: number;
  receiving: boolean;
}) {
  const explorer = useLiveChartExplorer(history, 120000);
  return (
    <section className="mg-card portable-flow-card" data-size="fit" aria-label={`국내 거래소 점유율 실시간 흐름 · ${windowLabel(windowSec)}`}>
      <LiveChartControls explorer={explorer} receiving={receiving} showNavigatorHelp={false} showCoverageNote={false} compact heading={`실시간 점유율 · ${windowLabel(windowSec)}`} />
      <LiveExchangeChartContent
        history={explorer.visible}
        metric="share"
        explorer={explorer}
        flowing={{ running: receiving, sampleIntervalMS: windowSec === 86400 ? 10_000 : 1000, pauseOnHover: false }}
        showValues={false}
        singleChart
      />
    </section>
  );
}
