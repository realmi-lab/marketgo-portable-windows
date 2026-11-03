import type { MarketShareRealtimeTick } from "./market-share-types";

type TickListener = () => void;

export interface MarketShareTickStore {
  getSnapshot: () => MarketShareRealtimeTick | null;
  subscribe: (listener: TickListener) => () => void;
  publish: (tick: MarketShareRealtimeTick | null) => void;
  pause: () => void;
  resume: () => void;
  reset: () => void;
}

export type TickStoreScheduler = {
  request: (callback: FrameRequestCallback) => number;
  cancel: (handle: number) => void;
};

export function createMarketShareTickStore(
  scheduler: TickStoreScheduler = {
    request: callback => globalThis.requestAnimationFrame(callback),
    cancel: handle => globalThis.cancelAnimationFrame(handle),
  },
  initial: MarketShareRealtimeTick | null = null
): MarketShareTickStore {
  let current: MarketShareRealtimeTick | null = initial;
  let latest: MarketShareRealtimeTick | null = initial;
  let paused = false;
  let notificationFrame: number | null = null;
  const listeners = new Set<TickListener>();

  const notify = () => listeners.forEach(listener => listener());
  const scheduleNotification = (immediate = false) => {
    if (immediate) {
      if (notificationFrame !== null) scheduler.cancel(notificationFrame);
      notificationFrame = null;
      notify();
      return;
    }
    if (notificationFrame !== null) return;
    notificationFrame = scheduler.request(() => {
      notificationFrame = null;
      notify();
    });
  };

  return {
    getSnapshot: () => current,
    subscribe: listener => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    publish: tick => {
      latest = tick;
      if (paused) return;
      current = tick;
      scheduleNotification(tick === null);
    },
    pause: () => {
      paused = true;
    },
    resume: () => {
      if (!paused) return;
      paused = false;
      if (current === latest) return;
      current = latest;
      scheduleNotification(latest === null);
    },
    reset: () => {
      if (notificationFrame !== null) scheduler.cancel(notificationFrame);
      notificationFrame = null;
      current = null;
      latest = null;
      paused = false;
      listeners.clear();
    },
  };
}
