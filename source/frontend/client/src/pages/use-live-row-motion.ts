import { useLayoutEffect, useRef, type RefObject } from "react";

/** Animate positions only; the displayed amounts always remain server values. */
export function useLiveRowMotion(container: RefObject<HTMLDivElement | null>, enabled: boolean, order: string) {
  const previous = useRef(new Map<string, number>());
  useLayoutEffect(() => {
    const root = container.current;
    if (!root) return;
    const rows = Array.from(root.querySelectorAll<HTMLTableRowElement>("tr[data-coin]"));
    const positions = rows.map(row => ({ row, key: row.dataset.coin!, top: row.offsetTop }));
    const animations: Animation[] = [];
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
    if (enabled && !reduced.matches) {
      const viewport = root.getBoundingClientRect();
      const visible = positions.filter(({ row }) => {
        const bounds = row.getBoundingClientRect();
        return bounds.bottom > viewport.top && bounds.top < viewport.bottom;
      });
      for (const { row, key, top } of visible) {
        const oldTop = previous.current.get(key);
        if (oldTop !== undefined && oldTop !== top && Math.abs(oldTop - top) < root.clientHeight) {
          animations.push(row.animate([
            { transform: `translateY(${oldTop - top}px)` },
            { transform: "translateY(0)" },
          ], { duration: 240, easing: "cubic-bezier(.2,.8,.2,1)" }));
        } else if (oldTop === undefined || oldTop !== top) {
          animations.push(row.animate([{ opacity: 0.45 }, { opacity: 1 }], { duration: 180 }));
        }
      }
    }
    previous.current = enabled ? new Map(positions.map(({ key, top }) => [key, top])) : new Map();
    const cancel = () => animations.forEach(animation => animation.cancel());
    const onPreference = () => { if (reduced.matches) cancel(); };
    reduced.addEventListener("change", onPreference);
    return () => { cancel(); reduced.removeEventListener("change", onPreference); };
  }, [container, enabled, order]);
}
