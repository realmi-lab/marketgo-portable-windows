/** Keep full HH:mm:ss labels apart in the actual plotting area. */
export function flowChartTimeTickStep(spanMS: number, plotWidth: number): number {
  const minimumLabelSlot = 72;
  const requiredStep = spanMS * minimumLabelSlot / Math.max(1, plotWidth);
  let step = 15_000;
  while (step < requiredStep) step *= 2;
  return step;
}
