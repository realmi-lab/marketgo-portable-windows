/*
 * How the dashboard's charts answer the keyboard and where their tooltips
 * go. Pure helpers, so the behaviour is tested without a browser.
 */

/**
 * The point an arrow, Home or End key moves to; null for any other key.
 * With nothing picked yet, an arrow starts at the latest point.
 */
export function keyedIndex(
  key: string,
  current: number | null,
  count: number
): number | null {
  if (count === 0) return null;
  const last = count - 1;
  if (key === "Home") return 0;
  if (key === "End") return last;
  if (key !== "ArrowLeft" && key !== "ArrowRight") return null;
  if (current === null) return last;
  return key === "ArrowLeft"
    ? Math.max(0, current - 1)
    : Math.min(last, current + 1);
}

/** The next id in `ids` one step forward or back, wrapping around. */
export function cycledId(
  ids: readonly string[],
  current: string | null,
  step: 1 | -1
): string | null {
  if (ids.length === 0) return null;
  const index = current === null ? -1 : ids.indexOf(current);
  if (index < 0) return step > 0 ? ids[0] : ids[ids.length - 1];
  return ids[(index + step + ids.length) % ids.length];
}

/**
 * Where a tooltip goes beside the point it describes: to the right when it
 * fits, otherwise to the left, and always inside the chart so nothing is cut.
 */
export function tipPosition(
  anchor: { x: number; y: number },
  tip: { width: number; height: number },
  box: { width: number; height: number },
  gap = 14
): { left: number; top: number } {
  let left = anchor.x + gap;
  if (left + tip.width > box.width) left = anchor.x - gap - tip.width;
  return {
    left: Math.max(0, Math.min(left, box.width - tip.width)),
    top: Math.max(
      0,
      Math.min(anchor.y - tip.height / 2, box.height - tip.height)
    ),
  };
}

/**
 * A trail of recent values per series (a window, a coin), extended by one new
 * value each, keeping the newest `limit`. A series missing from `latest` is
 * dropped. Values are stored as received.
 */
export function extendTrail<K extends string | number>(
  series: Readonly<Partial<Record<K, readonly (number | null)[]>>>,
  latest: ReadonlyArray<{ key: K; value: number | null }>,
  limit: number
): Record<K, (number | null)[]> {
  const next = {} as Record<K, (number | null)[]>;
  for (const { key, value } of latest) {
    next[key] = [...(series[key] ?? []), value].slice(-limit);
  }
  return next;
}

/** Pointer coordinates are viewport pixels; chart geometry uses local CSS pixels. */
export function chartLocalPoint(
  clientX: number,
  clientY: number,
  bounds: {left: number; top: number; width: number; height: number},
  size: {width: number; height: number}
): {x: number; y: number} | null {
  if (bounds.width <= 0 || bounds.height <= 0) return null;
  return {x:(clientX-bounds.left)*size.width/bounds.width, y:(clientY-bounds.top)*size.height/bounds.height};
}
