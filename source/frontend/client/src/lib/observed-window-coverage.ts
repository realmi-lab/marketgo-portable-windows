import { formatDuration } from "./formatters.ts";

/** Format supplied coverage counts only; never infer continuity or fill gaps. */
export function formatObservedWindowCoverage(
  observedSeconds: number | null | undefined,
  requiredSeconds: number | null | undefined,
  gapSeconds: number | null | undefined
): string {
  return `관측 ${formatDuration(observedSeconds)} / 필요 ${formatDuration(requiredSeconds)} · 누락 ${formatDuration(gapSeconds)}`;
}
