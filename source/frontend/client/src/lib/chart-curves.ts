/*
 * Smooth lines for the charts. Each tangent is the harmonic mean of the
 * slopes beside its point, and zero at a peak, a trough or a flat step
 * (Fritsch–Butland), so the curve passes through every point and never
 * overshoots between two of them: it shows no value the points do not.
 */

interface Point {
  x: number;
  y: number;
}

const coordinate = (point: Point) =>
  `${point.x.toFixed(1)},${point.y.toFixed(1)}`;

/** The runs of points with a value, left to right. */
function runs(points: ReadonlyArray<{ x: number; y: number | null }>) {
  const result: Point[][] = [];
  let run: Point[] = [];
  for (const { x, y } of points) {
    if (y === null) {
      if (run.length) result.push(run);
      run = [];
    } else {
      run.push({ x, y });
    }
  }
  if (run.length) result.push(run);
  return result;
}

/** The cubic segments from a run's first point through the rest. */
function curve(run: readonly Point[]): string {
  const slopes = run
    .slice(1)
    .map((point, index) => (point.y - run[index].y) / (point.x - run[index].x));
  const tangents = run.map((_, index) => {
    if (index === 0) return slopes[0] ?? 0;
    if (index === run.length - 1) return slopes[index - 1];
    const before = slopes[index - 1];
    const after = slopes[index];
    return before * after <= 0 ? 0 : 2 / (1 / before + 1 / after);
  });
  return run
    .slice(1)
    .map((point, index) => {
      const start = run[index];
      const third = (point.x - start.x) / 3;
      const first = {
        x: start.x + third,
        y: start.y + tangents[index] * third,
      };
      const second = {
        x: point.x - third,
        y: point.y - tangents[index + 1] * third,
      };
      return `C${coordinate(first)} ${coordinate(second)} ${coordinate(point)}`;
    })
    .join("");
}

/**
 * A smooth line through `points`, broken where a value is missing. A point
 * alone between gaps has no line; lonePointsPath marks it.
 */
export function smoothLinePath(
  points: ReadonlyArray<{ x: number; y: number | null }>
): string {
  return runs(points)
    .filter(run => run.length > 1)
    .map(run => `M${coordinate(run[0])}${curve(run)}`)
    .join("");
}

/**
 * The points alone between gaps, each a segment of no length, which a round
 * line cap draws as a dot the size of the stroke.
 */
export function lonePointsPath(
  points: ReadonlyArray<{ x: number; y: number | null }>
): string {
  return runs(points)
    .filter(run => run.length === 1)
    .map(([point]) => `M${coordinate(point)}L${coordinate(point)}`)
    .join("");
}

/** The area under that line, down to `baseline`, for each run. */
export function smoothAreaPath(
  points: ReadonlyArray<{ x: number; y: number | null }>,
  baseline: number
): string {
  return runs(points)
    .map(run => {
      const first = run[0];
      const last = run[run.length - 1];
      return `M${first.x.toFixed(1)},${baseline.toFixed(1)}L${coordinate(first)}${curve(run)}L${last.x.toFixed(1)},${baseline.toFixed(1)}Z`;
    })
    .join("");
}
