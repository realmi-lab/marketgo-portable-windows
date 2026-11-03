/** A formatter may need the raw nonzero value to distinguish it from exact zero. */
export function animatedMetricTarget(
  value: number | null | undefined,
  digits: number,
  preserveRawValue = false
): number | null {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return null;
  }
  return preserveRawValue ? value : Number(value.toFixed(digits));
}
