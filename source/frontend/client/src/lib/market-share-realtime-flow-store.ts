import type { MarketShareRealtimeFlowState } from "./market-share-types";

type RealtimeFlowListener = () => void;

export interface MarketShareRealtimeFlowStore {
  getSnapshot: () => MarketShareRealtimeFlowState | null;
  subscribe: (listener: RealtimeFlowListener) => () => void;
  publish: (state: MarketShareRealtimeFlowState | null) => void;
  reset: () => void;
}

/**
 * Keeps one-second realtime-flow frames outside React's page state so only
 * components that explicitly subscribe are rendered for every observation.
 */
export function createMarketShareRealtimeFlowStore(
  initial: MarketShareRealtimeFlowState | null = null
): MarketShareRealtimeFlowStore {
  let current: MarketShareRealtimeFlowState | null = initial;
  const listeners = new Set<RealtimeFlowListener>();

  return {
    getSnapshot: () => current,
    subscribe: listener => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    publish: state => {
      if (current === state) return;
      // Full analysis frames can arrive after a newer one-second publication.
      // Keep event time moving forward; equal-time quality corrections still apply.
      if (current && state && state.as_of_ms < current.as_of_ms) return;
      current = state;
      listeners.forEach(listener => listener());
    },
    reset: () => {
      current = null;
      listeners.clear();
    },
  };
}
