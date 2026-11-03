import { formatKRW, formatKrwPrice } from "./formatters.ts";

const exactNotionalFormat = new Intl.NumberFormat("ko-KR", {
  maximumSignificantDigits: 21,
});
// Live rows use six significant digits and their tooltips use ten. Reuse
// those four formatters; uncommon caller-supplied precision keeps Intl's
// existing validation and formatting without growing an unbounded cache.
const quantityFormats = new Map<string, Intl.NumberFormat>();

function observed(value: number | null | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0;
}

export function bestBookNotional(value: number | null | undefined): string {
  return observed(value) ? formatKRW(value) : "—";
}

export function bestBookExactNotional(
  value: number | null | undefined
): string {
  return observed(value)
    ? `₩${exactNotionalFormat.format(value)}`
    : "—";
}

export function bestBookPrice(value: number | null | undefined): string {
  return observed(value) ? formatKrwPrice(value) : "—";
}

/** A small BTC quantity must not disappear under a fixed four-decimal format. */
/** Six significant digits fit the order-book cell; the tooltip asks for ten. */
export function bestBookQuantity(
  value: number | null | undefined,
  asset?: string,
  significantDigits = 6
): string {
  if (!observed(value)) return "—";
  const notation = value > 0 && value < 1e-8 ? "scientific" : "standard";
  const key = `${notation}:${significantDigits}`;
  let format = quantityFormats.get(key);
  if (!format) {
    format = new Intl.NumberFormat("ko-KR", {
      notation,
      maximumSignificantDigits: significantDigits,
    });
    if (significantDigits === 6 || significantDigits === 10)
      quantityFormats.set(key, format);
  }
  const quantity = format.format(value);
  return asset ? `${quantity} ${asset}` : quantity;
}
