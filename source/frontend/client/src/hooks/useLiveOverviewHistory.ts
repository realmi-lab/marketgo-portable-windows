import { useEffect, useRef, useState } from "react";

import type { MarketShareConnection } from "@/hooks/useMarketShare";
import { getClosedSeconds } from "@/lib/api-client";
import {
  appendLiveHistoryGap,
  appendLiveHistoryPoint,
  seedLiveHistory,
  type LiveChartHistoryPoint,
} from "@/lib/live-chart";
import {
  buildClosedSecondsHistory,
  buildLiveOverviewPoint,
} from "@/lib/market-share-overview-point";
import type {
  MarketScope,
  MarketShareRealtimeTick,
} from "@/lib/market-share-types";

/** The windows whose recent closed seconds the calculator gives at once. */
const SEEDED_WINDOWS = new Set([10, 30, 60]);
const EMPTY_HISTORY: LiveChartHistoryPoint[] = [];

/**
 * The live charts' recent history, one point per tick of the requested
 * window. It starts over when the scope or window changes and marks a gap
 * when the connection drops, so a line never bridges a stretch it did not see.
 * A window of a minute or less opens with its last two minutes, read once.
 */
export function useLiveOverviewHistory({
  tick,
  connection,
  marketScope,
  requestedWindowSec,
}: {
  tick: MarketShareRealtimeTick | null;
  connection: MarketShareConnection;
  marketScope: MarketScope;
  requestedWindowSec: number;
}): LiveChartHistoryPoint[] {
  const key = `${marketScope}:${requestedWindowSec}`;
  const [state, setState] = useState(() => ({ key, history: EMPTY_HISTORY }));
  const previousConnectionRef = useRef(connection);

  useEffect(() => {
    setState({ key, history: EMPTY_HISTORY });
    if (!SEEDED_WINDOWS.has(requestedWindowSec)) return;
    const controller = new AbortController();
    getClosedSeconds(requestedWindowSec, marketScope, controller.signal)
      .then(answer => {
        if (
          controller.signal.aborted ||
          answer.window_sec !== requestedWindowSec ||
          answer.market_scope !== marketScope
        )
          return;
        const seeds = buildClosedSecondsHistory(
          answer.seconds,
          requestedWindowSec
        );
        setState(current =>
          current.key === key
            ? { key, history: seedLiveHistory(current.history, seeds) }
            : current
        );
      })
      // Without them (an older calculator, a failed read) the chart fills
      // as the ticks come.
      .catch(() => undefined);
    return () => controller.abort();
  }, [key, marketScope, requestedWindowSec]);
  useEffect(() => {
    const previous = previousConnectionRef.current;
    previousConnectionRef.current = connection;
    if (previous === "live" && connection !== "live") {
      setState(current =>
        current.key === key
          ? { key, history: appendLiveHistoryGap(current.history) }
          : current
      );
    }
  }, [connection, key]);
  useEffect(() => {
    if (connection !== "live") return;
    const point = buildLiveOverviewPoint(tick, marketScope, requestedWindowSec);
    if (!point) return;
    setState(current => {
      const history = current.key === key ? current.history : EMPTY_HISTORY;
      const next = appendLiveHistoryPoint(history, point);
      return current.key === key && next === history
        ? current
        : { key, history: next };
    });
  }, [connection, key, marketScope, requestedWindowSec, tick]);
  return state.key === key ? state.history : EMPTY_HISTORY;
}
