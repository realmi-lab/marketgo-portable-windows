import { useEffect, useRef, type Dispatch, type SetStateAction } from "react";

import { currentKSTDay } from "./index-day.ts";

// Historical dates can also change while the archive catches up.
export function useIndexRefresh(refresh: Dispatch<SetStateAction<number>>, intervalMS = 60_000) {
  useEffect(() => {
    const update = () => {
      if (document.visibilityState === "visible") refresh(value => value + 1);
    };
    const timer = window.setInterval(update, intervalMS);
    document.addEventListener("visibilitychange", update);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", update);
    };
  }, [refresh, intervalMS]);
}

/**
 * A page left open on "today" moves to the new KST date after midnight (or
 * when a phone restores the tab the next morning); a date the user picked
 * stays as chosen. Without this the page kept asking for yesterday's date and
 * stopped following the rolling 24-hour data.
 */
export function useFollowKSTToday(day: string, onNewDay: (today: string) => void, referenceDay?: string) {
  const latest = useRef({ day, onNewDay, following: day === (referenceDay ?? currentKSTDay()) });
  useEffect(() => {
    const previous = latest.current;
    latest.current = {
      day,
      onNewDay,
      following: day === (referenceDay ?? currentKSTDay()) || (previous.following && previous.day === day),
    };
  });
  useEffect(() => {
    const check = () => {
      const { day, onNewDay, following } = latest.current;
      const today = referenceDay ?? currentKSTDay();
      if (following && day !== today) onNewDay(today);
    };
    check();
    const timer = window.setInterval(check, 60_000);
    document.addEventListener("visibilitychange", check);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", check);
    };
  }, [referenceDay]);
}
