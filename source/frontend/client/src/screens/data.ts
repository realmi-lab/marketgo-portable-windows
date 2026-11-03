import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";

import {
  getNoticeArchive,
  getOfficialAlignedContribution,
  getOfficialAlignedHistory,
  getMarketShareRealtimeHistory,
  getMarketShareRealtimeSummary,
  getShareSeries,
  getWindowChanges,
} from "@/lib/api-client";
import {
  latestSafeOfficialHourMS,
  OFFICIAL_ALIGNED_HOUR_MS,
} from "@/lib/market-share-official-history";
import type { MarketShareSeriesBucketSec } from "@/lib/market-share-types";
import { readFailureReason } from "@/lib/screen-copy";

/* HTTP reads the screens poll next to the stream. */

export interface Polled<T> {
  data: T | undefined;
  /** Why the latest read failed, in plain words; "" after a success. */
  error: string;
  /** True until the first answer for the current key arrives. */
  loading: boolean;
  /** The data is from an earlier read because the latest one failed. */
  stale: boolean;
  /** When the shown data arrived (Unix ms), or 0. */
  receivedAtMS: number;
}

interface PollState<T> {
  data?: T;
  error: string;
  answered: boolean;
  receivedAtMS: number;
}

/**
 * One read per key, however many cards show it. It refreshes every
 * intervalMs while a card reads it and the page is visible, and a result is
 * kept for a minute after the last card closes so a screen opened again
 * starts with it.
 */
interface Poll<T> {
  load: (signal: AbortSignal) => Promise<T>;
  intervalMs: number;
  state: PollState<T>;
  readers: Set<() => void>;
  timer?: ReturnType<typeof setTimeout>;
  controller?: AbortController;
  forget?: ReturnType<typeof setTimeout>;
}

const NOTHING_YET: PollState<never> = {
  error: "",
  answered: false,
  receivedAtMS: 0,
};
const KEEP_AFTER_LAST_READER_MS = 60_000;
const polls = new Map<string, Poll<unknown>>();

function stopPoll(poll: Poll<unknown>) {
  if (poll.timer) clearTimeout(poll.timer);
  poll.timer = undefined;
  poll.controller?.abort();
  poll.controller = undefined;
}

function runPoll<T>(poll: Poll<T>) {
  stopPoll(poll);
  if (poll.readers.size === 0 || document.visibilityState === "hidden") return;
  if (typeof navigator !== "undefined" && navigator.onLine === false) {
    poll.state = {
      ...poll.state,
      error: "네트워크 연결이 끊겼습니다.",
      answered: true,
    };
    poll.readers.forEach(reader => reader());
    return;
  }
  const controller = new AbortController();
  poll.controller = controller;
  const publish = (state: PollState<T>) => {
    if (controller.signal.aborted || poll.controller !== controller) return;
    poll.state = state;
    poll.readers.forEach(reader => reader());
  };
  poll
    .load(controller.signal)
    .then(
      data =>
        publish({ data, error: "", answered: true, receivedAtMS: Date.now() }),
      error => {
        // A failed refresh keeps the last value and says so (see Polled.stale).
        if (!controller.signal.aborted) {
          publish({
            ...poll.state,
            error: readFailureReason(error),
            answered: true,
          });
        }
      }
    )
    .finally(() => {
      if (poll.controller !== controller) return; // stopped or replaced
      poll.controller = undefined;
      if (poll.intervalMs > 0)
        poll.timer = setTimeout(() => runPoll(poll), poll.intervalMs);
    });
}

function readPoll<T>(
  key: string,
  load: (signal: AbortSignal) => Promise<T>,
  intervalMs: number,
  reader: () => void
): () => void {
  let poll = polls.get(key) as Poll<T> | undefined;
  if (!poll) {
    poll = { load, intervalMs, state: NOTHING_YET, readers: new Set() };
    polls.set(key, poll as Poll<unknown>);
  }
  const current = poll;
  if (current.forget) clearTimeout(current.forget);
  current.forget = undefined;
  current.readers.add(reader);
  if (current.readers.size === 1) runPoll(current);
  return () => {
    current.readers.delete(reader);
    if (current.readers.size > 0) return;
    stopPoll(current as Poll<unknown>);
    current.forget = setTimeout(
      () => polls.delete(key),
      KEEP_AFTER_LAST_READER_MS
    );
  };
}

// A hidden page stops every read; showing it again refreshes what is on screen.
document.addEventListener("visibilitychange", () => {
  for (const poll of polls.values()) {
    if (document.visibilityState === "hidden") stopPoll(poll);
    else runPoll(poll);
  }
});

// Long-interval and one-time reads also need an immediate retry when the
// network returns. A pending read from before going offline is obsolete.
if (typeof window !== "undefined") {
  window.addEventListener("offline", () => {
    for (const poll of polls.values()) runPoll(poll);
  });
  window.addEventListener("online", () => {
    for (const poll of polls.values()) {
      if (!poll.controller) runPoll(poll);
    }
  });
}

/**
 * Reads `load` for `key` and keeps it fresh while the component shows it.
 * With `keepPrevious`, a new key (the same read moved on, such as a range
 * that ends an hour later) shows the previous key's data until its own
 * arrives, and marks it stale if its first read fails.
 */
export function usePolled<T>(
  key: string | null,
  load: (signal: AbortSignal) => Promise<T>,
  intervalMs: number,
  { keepPrevious = false }: { keepPrevious?: boolean } = {}
): Polled<T> {
  const subscribe = useCallback(
    (reader: () => void) =>
      key ? readPoll(key, load, intervalMs, reader) : () => undefined,
    // `load` is read through the key: a new key is a new request.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [key, intervalMs]
  );
  const snapshot = () =>
    key
      ? ((polls.get(key)?.state as PollState<T>) ?? NOTHING_YET)
      : NOTHING_YET;
  const current = useSyncExternalStore(subscribe, snapshot, snapshot);
  const previous = useRef<PollState<T> | null>(null);
  if (current.data !== undefined) previous.current = current;
  const state =
    keepPrevious && current.data === undefined && previous.current
      ? { ...previous.current, error: current.error, answered: true }
      : current;
  return {
    data: state.data,
    error: state.error,
    loading: key !== null && !state.answered,
    stale: state.error !== "" && state.data !== undefined,
    receivedAtMS: state.receivedAtMS,
  };
}

/** Every window compared with the one before it, refreshed with the stream. */
export function useWindowChanges() {
  return usePolled(
    "window-changes:KRW",
    signal => getWindowChanges("KRW", signal),
    10_000
  );
}

/** The latest closed official hour, moving once an hour. */
export function useLatestOfficialHour(): number {
  const [hour, setHour] = useState(() => latestSafeOfficialHourMS(Date.now()));
  useEffect(() => {
    const timer = setInterval(
      () => setHour(latestSafeOfficialHourMS(Date.now())),
      60_000
    );
    return () => clearInterval(timer);
  }, []);
  return hour;
}

/** Official hourly share (Upbit, Bithumb, Coinone candles) for `hours` hours. */
export function useOfficialHistory(hours: number) {
  const toMS = useLatestOfficialHour();
  const fromMS = toMS - (hours - 1) * OFFICIAL_ALIGNED_HOUR_MS;
  return usePolled(
    `official-history:${fromMS}:${toMS}`,
    signal => getOfficialAlignedHistory(fromMS, toMS, signal),
    5 * 60_000,
    // Each new hour moves the range; the last one stays until it arrives.
    { keepPrevious: true }
  );
}

/**
 * Bithumb's share in each recent minute (two hours), ten minutes or half
 * hour (a day), or nothing for null. Minutes are asked for every 20 seconds,
 * longer buckets every minute.
 */
export function useShareSeries(bucketSec: MarketShareSeriesBucketSec | null) {
  return usePolled(
    bucketSec ? `share-series:${bucketSec}:KRW` : null,
    signal => getShareSeries(bucketSec ?? 60, "KRW", signal),
    bucketSec === 60 ? 20_000 : 60_000
  );
}

/** What moved the share from the hour before `toMS` to `toMS`. */
export function useOfficialContribution(toMS: number | null) {
  return usePolled(
    toMS ? `official-contribution:${toMS}` : null,
    signal => getOfficialAlignedContribution(toMS ?? 0, signal),
    5 * 60_000
  );
}

/**
 * The newest stored notices: one page of the server's largest size, as
 * headlines (a twentieth of the whole notices; an older server sends them
 * whole).
 */
export function useRecentNotices(limit = 100) {
  return usePolled(
    `notices:${limit}`,
    signal =>
      getNoticeArchive({ offset: 0, limit, fields: "headline" }, signal),
    60_000
  );
}

/** The last minutes of ten-second flow samples. */
export function useRealtimeHistory(minutes: number) {
  return usePolled(
    `realtime-history:${minutes}`,
    signal =>
      getMarketShareRealtimeHistory(
        { fromMS: Date.now() - minutes * 60_000, limit: 200 },
        signal
      ),
    10_000
  );
}

/** The day's realtime-flow summary with its latest episodes. */
export function useRealtimeDay(episodeLimit = 20) {
  return usePolled(
    `realtime-day:${episodeLimit}`,
    signal =>
      getMarketShareRealtimeSummary(Date.now(), signal, {
        limit: episodeLimit,
      }),
    30_000
  );
}
