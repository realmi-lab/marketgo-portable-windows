import { useLayoutEffect, useRef, useState } from "react";
import {
  advanceCollectorWarnings,
  COLLECTOR_WARNING_GRACE_MS,
  deferredCollectorWarnings,
  type CollectorWarningState,
} from "@/lib/collector-connection-warning";
import type { MarketShareReadiness } from "@/lib/market-share-types";

/** Keep one grace clock across dashboard navigation and incoming frames. */
export function useCollectorConnectionWarnings(
  readiness: MarketShareReadiness | null,
  live: boolean
) {
  const state = useRef<CollectorWarningState>({});
  const [deferred, setDeferred] = useState<string[]>([]);
  useLayoutEffect(() => {
    if (!live || !readiness) {
      setDeferred(current => (current.length ? [] : current));
      return;
    }
    const now = performance.now();
    state.current = advanceCollectorWarnings(state.current, readiness, now);
    let timer: number | undefined;
    const publish = () => {
      const at = performance.now();
      const next = deferredCollectorWarnings(state.current, at);
      setDeferred(current =>
        current.length === next.length &&
        current.every((value, i) => value === next[i])
          ? current
          : next
      );
      if (next.length) {
        const deadline = Math.min(
          ...next.map(
            exchange =>
              state.current[exchange].disconnectedAt! +
              COLLECTOR_WARNING_GRACE_MS
          )
        );
        timer = window.setTimeout(
          publish,
          Math.max(1, Math.ceil(deadline - at))
        );
      }
    };
    publish();
    return () => window.clearTimeout(timer);
  }, [readiness, live]);
  return deferred;
}
