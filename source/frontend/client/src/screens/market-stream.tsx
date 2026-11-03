import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";

import { useMarketShare } from "@/hooks/useMarketShare";
import { useCollectorConnectionWarnings } from "@/hooks/useCollectorConnectionWarnings";
import type { MarketShareRealtimeFlowStream } from "@/lib/api-client";
import { extendTrail } from "@/lib/chart-interaction";
import type { LiveCoinView } from "@/lib/coin-shares-presentation";
import type { ScreenId } from "@/lib/dashboard-navigation";
import {
  realtimeFlowValuesAreVisible,
  realtimeFlowWindowObserved,
  realtimeFlowWindowValuesAreVisible,
} from "@/lib/market-share-realtime-flow";
import type {
  MarketShareRealtimeFlowSharePoint1S,
  MarketShareRealtimeFlowState,
  MarketShareRealtimeFlowWindow,
  MarketShareRealtimeTick,
} from "@/lib/market-share-types";

/*
 * One market-share stream serves the whole dashboard. The projection follows
 * the open screen so each one receives only what it reads (see
 * analysis_projection.go):
 *  - brief:    the briefing (the overview's summary, official 24h and phase,
 *              brief and windows; notices come over HTTP)
 *  - summary2: the live screen (24-hour realtime monitor, official monitor)
 *  - drivers:  change, cause, coin and exchange screens (whole attribution)
 *  - quality:  data status (gap records)
 *  - shell:    notices, questions and the indexes (tick and independent states;
 *              the notice workspace has its own notice subscription)
 * 코인 인덱스 2 also asks for the coins' live shares (coin_shares=live).
 */
const SCREEN_STREAMS: Record<
  ScreenId,
  {
    view: string;
    realtimeFlow: MarketShareRealtimeFlowStream;
    /** The coins' live shares, every second. */
    coinShares?: boolean;
  }
> = {
  brief: { view: "brief", realtimeFlow: "full" },
  live: { view: "summary2", realtimeFlow: "full" },
  change: { view: "drivers", realtimeFlow: "strip" },
  why: { view: "drivers", realtimeFlow: "strip" },
  coins: { view: "drivers", realtimeFlow: "strip" },
  exchanges: { view: "drivers", realtimeFlow: "strip" },
  // The indexes read their own endpoints; 코인 인덱스 2 reads the coins'
  // live shares from this stream.
  index: { view: "shell", realtimeFlow: "strip" },
  index2: { view: "shell", realtimeFlow: "strip" },
  "coin-shares": { view: "shell", realtimeFlow: "strip", coinShares: true },
  notices: { view: "shell", realtimeFlow: "strip" },
  question: { view: "shell", realtimeFlow: "strip" },
  status: { view: "quality", realtimeFlow: "strip" },
};

/*
 * The live screen keeps its own window, ten seconds to start with, so its
 * figures move with the market; every other screen shares one window that
 * starts at ten minutes, as in the briefing.
 */
type WindowGroup = "live" | "analysis";
const FIRST_WINDOWS: Record<WindowGroup, number> = { live: 10, analysis: 600 };
const windowGroup = (screen: ScreenId): WindowGroup =>
  screen === "live" ? "live" : "analysis";

type MarketStream = ReturnType<typeof useMarketShare> & {
  deferredCollectorWarnings: string[];
  analysisExpected: boolean;
  windowSec: number;
  setWindowSec: (seconds: number) => void;
  /** While paused, the once-a-second values stay as they were. */
  livePaused: boolean;
  setLivePaused: (paused: boolean) => void;
  held: Held;
  hold: <Kind extends keyof Held>(
    kind: Kind,
    value: NonNullable<Held[Kind]>
  ) => void;
};

/**
 * The values shown while paused, each with the store it came from. A screen
 * whose stream differs (another view, window or flow) has new stores, so its
 * values are held from their first frame instead of showing another
 * stream's.
 */
type Held = {
  tick?: { store: unknown; value: MarketShareRealtimeTick };
  flow?: { store: unknown; value: MarketShareRealtimeFlowState };
  coins?: { store: unknown; value: LiveCoinView };
};

const MarketStreamContext = createContext<MarketStream | null>(null);

export function MarketStreamProvider({
  screen,
  children,
  initialWindowSec,
  includeCoinShares = false,
}: {
  screen: ScreenId;
  children: ReactNode;
  initialWindowSec?: number;
  includeCoinShares?: boolean;
}) {
  const [windows, setWindows] = useState(() => initialWindowSec === undefined ? FIRST_WINDOWS : { live: initialWindowSec, analysis: initialWindowSec });
  const group = windowGroup(screen);
  const windowSec = windows[group];
  const setWindowSec = useCallback(
    (seconds: number) =>
      setWindows(current => ({ ...current, [group]: seconds })),
    [group]
  );
  const [paused, setPaused] = useState<Held | null>(null);
  const { view, realtimeFlow, coinShares = false } = SCREEN_STREAMS[screen];
  const stream = useMarketShare(
    windowSec,
    null,
    "KRW",
    view,
    realtimeFlow,
    coinShares || includeCoinShares
  );
  const deferredCollectorWarnings = useCollectorConnectionWarnings(
    stream.readiness,
    stream.connection === "live"
  );
  // Pausing holds nothing yet: each value is held by the first reader that
  // sees it (useHeld), from the stream on view.
  const setLivePaused = useCallback(
    (next: boolean) => setPaused(next ? {} : null),
    []
  );
  const hold = useCallback(
    <Kind extends keyof Held>(kind: Kind, value: NonNullable<Held[Kind]>) =>
      setPaused(current =>
        current && current[kind]?.store !== value.store
          ? { ...current, [kind]: value }
          : current
      ),
    []
  );
  // The stream object changes with every analysis frame, so the value does
  // too; once-a-second values reach only components reading the stores.
  const value: MarketStream = {
    ...stream,
    deferredCollectorWarnings,
    analysisExpected: view !== "shell",
    windowSec,
    setWindowSec,
    livePaused: paused !== null,
    setLivePaused,
    held: paused ?? {},
    hold,
  };
  return (
    <MarketStreamContext.Provider value={value}>
      {children}
    </MarketStreamContext.Provider>
  );
}

export function useMarketStream(): MarketStream {
  const stream = useContext(MarketStreamContext);
  if (!stream) throw new Error("useMarketStream needs MarketStreamProvider");
  return stream;
}

const stayStill = () => () => undefined;

/**
 * A store's value, or while paused the one held from that store: the value
 * on view when the user paused, or, for a stream opened since (another
 * screen), its first value.
 */
function useHeld<Kind extends keyof Held>(
  kind: Kind,
  store: {
    subscribe: (listener: () => void) => () => void;
    getSnapshot: () => NonNullable<Held[Kind]>["value"] | null;
  },
  present: (value: NonNullable<Held[Kind]>["value"]) => boolean
): NonNullable<Held[Kind]>["value"] | null {
  const { livePaused, held, hold } = useMarketStream();
  const entry = held[kind];
  const kept = livePaused && entry?.store === store ? entry.value : null;
  const value = useSyncExternalStore(
    kept ? stayStill : store.subscribe,
    store.getSnapshot,
    store.getSnapshot
  );
  useEffect(() => {
    if (livePaused && !kept && value && present(value))
      hold(kind, { store, value } as NonNullable<Held[Kind]>);
  }, [hold, kept, kind, livePaused, present, store, value]);
  return kept ?? value;
}

const always = () => true;
const hasShares = (view: LiveCoinView) => Boolean(view.shares);

/** The latest realtime tick, or the one held while paused. */
export function useLiveTick(): MarketShareRealtimeTick | null {
  return useHeld("tick", useMarketStream().tickStore, always);
}

/** The latest one-second flow, or the one held while paused. */
export function useLiveFlow(): MarketShareRealtimeFlowState | null {
  return useHeld("flow", useMarketStream().realtimeFlowStore, always);
}

/**
 * The coins' live shares and the seconds their lines are drawn from, or
 * those held while paused: the first frame with shares when paused before
 * this page had one. Frames keep arriving while paused, so the lines go on
 * from where they are when the user resumes.
 */
export function useLiveCoinView(): LiveCoinView {
  const { coinSharesStore } = useMarketStream();
  return (
    useHeld("coins", coinSharesStore, hasShares) ??
    coinSharesStore.getSnapshot()
  );
}

/**
 * The flow and how far each value was observed. A window or second missing
 * some seconds or exchanges keeps the value measured over what was observed,
 * marked as such (관측범위기준); changes and drivers come only from complete
 * windows.
 */
export function useShownFlow() {
  const flow = useLiveFlow();
  const find = (seconds: number) =>
    flow?.windows?.find(item => item.duration_sec === seconds);
  return {
    flow,
    /** Drivers, the latest second and the primary change are complete. */
    visible: realtimeFlowValuesAreVisible(flow),
    /**
     * The window of `seconds` with its measured share, or null when it has
     * none. A partly observed window is returned too (see partial).
     */
    window: (seconds: number): MarketShareRealtimeFlowWindow | null => {
      const window = find(seconds);
      return realtimeFlowWindowObserved(flow, window) ? window : null;
    },
    /** The window's share came from only the seconds observed (관측범위기준). */
    partial: (seconds: number) =>
      !realtimeFlowWindowValuesAreVisible(flow, find(seconds)),
  };
}

/** A one-second point the server marked complete and live. */
export function secondIsComplete(
  point: MarketShareRealtimeFlowSharePoint1S
): boolean {
  return point.complete === true && point.status === "LIVE";
}

/**
 * A second's Bithumb share: of every exchange when the second is complete,
 * otherwise the share the server measured over the exchanges it observed.
 */
export function secondShare(
  point: MarketShareRealtimeFlowSharePoint1S
): number | null {
  return secondIsComplete(point)
    ? (point.bithumb_share_1s_pct ?? null)
    : (point.observed_bithumb_share_1s_pct ?? null);
}

/**
 * The recent values of each realtime window's share (10초, 30초, 1분), one
 * per flow the server sent, oldest first, for small trend lines. A partly
 * observed window keeps its measured share; one without a share leaves a
 * gap. Values are kept as received.
 */
export function useShareTrail(limit = 120): Record<number, (number | null)[]> {
  const { flow, window } = useShownFlow();
  const [trail, setTrail] = useState<{
    at: number;
    series: Record<number, (number | null)[]>;
  }>({ at: 0, series: {} });
  const at = flow?.as_of_ms ?? 0;
  // Each new flow extends the trail once (state adjusted while rendering).
  if (at > trail.at) {
    const latest = (flow?.windows ?? []).map(({ duration_sec: seconds }) => ({
      key: seconds,
      value: window(seconds)?.current_share_pct ?? null,
    }));
    setTrail({ at, series: extendTrail(trail.series, latest, limit) });
  }
  return trail.series;
}
