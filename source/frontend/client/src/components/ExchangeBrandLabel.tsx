import { canonicalExchangeId, exchangeLabel } from "@/lib/exchanges";

export function ExchangeBrandLabel({
  exchange,
  label,
  className,
}: {
  exchange: string;
  label?: string;
  className?: string;
}) {
  const isBithumb = canonicalExchangeId(exchange) === "bithumb";

  return (
    <span
      className={`inline-flex min-w-0 items-center gap-1.5${className ? ` ${className}` : ""}`}
      data-exchange-brand={isBithumb ? "bithumb" : undefined}
    >
      <span
        className={
          isBithumb ? "text-[var(--exchange-bithumb-text)]" : undefined
        }
      >
        {label ?? exchangeLabel(exchange)}
      </span>
    </span>
  );
}
