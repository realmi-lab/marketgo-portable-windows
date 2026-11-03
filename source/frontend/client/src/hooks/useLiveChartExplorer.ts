import { useMemo, useState } from "react";

import type { LiveChartHistoryPoint } from "@/lib/live-chart";
import {
  liveChartPointIndex,
  liveChartRange,
  normalizeChartRange,
  type ChartRange,
} from "@/lib/live-chart-explorer";

/** One bounded in-memory view shared by the share and activity charts. */
export function useLiveChartExplorer(
  history: LiveChartHistoryPoint[],
  initialDurationMS: number | null = 60_000
) {
  const [durationMS, setDurationMS] = useState<number | null>(
    initialDurationMS
  );
  const [held, setHeld] = useState<{
    rows: LiveChartHistoryPoint[];
    range: ChartRange;
    pinnedAt: number | null;
  } | null>(null);
  const [hoverAt, setHoverAt] = useState<number | null>(null);
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set());
  const source = held?.rows ?? history;
  const range = useMemo(
    () => liveChartRange(source, durationMS, held?.range),
    [source, durationMS, held?.range]
  );
  const visible = useMemo(
    () => source.slice(range[0], range[1] + 1),
    [source, range]
  );
  const candidate = held?.pinnedAt ?? hoverAt;
  const selectedIndex = liveChartPointIndex(visible, candidate);
  const selectedAt = selectedIndex < 0 ? null : candidate;

  const follow = (duration = durationMS) => {
    setDurationMS(duration);
    setHeld(null);
    setHoverAt(null);
  };
  const zoom = (first: number, last: number, hold = false) => {
    const next = normalizeChartRange(source.length, first, last);
    if (!hold && next[0] === range[0] && next[1] === range[1]) return;
    if (!source.length) return;
    setHeld({
      rows: source,
      range: [source[next[0]].at, source[next[1]].at],
      pinnedAt: null,
    });
    setHoverAt(null);
  };
  const pin = (at: number) => {
    if (liveChartPointIndex(visible, at) < 0) return;
    setHeld({
      rows: source,
      range: [visible[0].at, visible.at(-1)!.at],
      pinnedAt: at,
    });
    setHoverAt(null);
  };
  const inspect = (at: number | null) => {
    if (held?.pinnedAt !== null && held?.pinnedAt !== undefined) return;
    setHoverAt(at);
  };
  const step = (offset: number) => {
    const from = selectedIndex < 0 ? visible.length - 1 : selectedIndex;
    const row =
      visible[Math.max(0, Math.min(visible.length - 1, from + offset))];
    if (row) pin(row.at);
  };
  const toggleExchange = (exchange: string) =>
    setHidden(current => {
      const next = new Set(current);
      if (next.has(exchange)) next.delete(exchange);
      else next.add(exchange);
      return next;
    });

  return {
    source,
    range,
    visible,
    durationMS,
    held: held !== null,
    pinned: held?.pinnedAt != null,
    selectedAt,
    selectedIndex,
    hidden,
    follow,
    zoom,
    pin,
    inspect,
    step,
    toggleExchange,
  };
}

export type LiveChartExplorer = ReturnType<typeof useLiveChartExplorer>;
