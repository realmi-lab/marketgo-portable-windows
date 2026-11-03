export interface LabelBox {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

/**
 * Which points of a series carry a value label. Labels go on the chosen
 * point, the latest one, the highest and the lowest first, then on the other
 * points counting back from the latest; a label is drawn only when its box
 * (from `boxOf`, in px) stays between `area.left` and `area.right` and clears
 * every label already drawn by `gap` px, so the numbers never overlap each
 * other or the axis at any chart width.
 */
export function labelledIndexes(
  values: readonly (number | null)[],
  boxOf: (index: number) => LabelBox,
  area: { left: number; right: number },
  chosen: number | null = null,
  gap = 4
): Set<number> {
  const present = values.flatMap((value, index) =>
    typeof value === "number" ? [index] : []
  );
  if (present.length === 0) return new Set();
  const byValue = [...present].sort(
    (left, right) => (values[left] as number) - (values[right] as number)
  );
  const latest = present[present.length - 1];
  const first = [chosen, latest, byValue[byValue.length - 1], byValue[0]];
  const rest = [...present].reverse();
  const placed = new Map<number, LabelBox>();
  for (const index of [...first, ...rest]) {
    if (index === null || placed.has(index)) continue;
    if (typeof values[index] !== "number") continue;
    const box = boxOf(index);
    if (box.left < area.left || box.right > area.right) continue;
    const clear = [...placed.values()].every(
      other =>
        box.right + gap <= other.left ||
        box.left >= other.right + gap ||
        box.bottom + gap <= other.top ||
        box.top >= other.bottom + gap
    );
    if (clear) placed.set(index, box);
  }
  return new Set(placed.keys());
}

export interface LabelSpot {
  key: string;
  /** The circle's centre and radius, in px. */
  x: number;
  y: number;
  r: number;
}

/**
 * Where each name goes beside its circle, in the order given: on the right,
 * else on the left, else not at all, so no name leaves the plotting area or lies over
 * a name already placed. A name is taken as `charWidth` px a letter plus a
 * little room, a bit wider than the 12px chart labels measure.
 */
export function placedLabels(
  spots: readonly LabelSpot[],
  area: { left: number; right: number },
  charWidth = 8.5
): Map<string, { x: number; anchor: "start" | "end" }> {
  const taken: Array<{ left: number; right: number; y: number }> = [];
  const placed = new Map<string, { x: number; anchor: "start" | "end" }>();
  for (const spot of spots) {
    const length = spot.key.length * charWidth + 2;
    const right = spot.x + spot.r + 4;
    const left = spot.x - spot.r - 4;
    const sides = [
      { x: right, anchor: "start" as const, from: right, to: right + length },
      { x: left, anchor: "end" as const, from: left - length, to: left },
    ];
    const side = sides.find(
      option =>
        option.from >= area.left &&
        option.to <= area.right &&
        !taken.some(
          other =>
            option.from < other.right &&
            option.to > other.left &&
            Math.abs(spot.y - other.y) < 16
        )
    );
    if (!side) continue;
    taken.push({ left: side.from, right: side.to, y: spot.y });
    placed.set(spot.key, { x: side.x, anchor: side.anchor });
  }
  return placed;
}
