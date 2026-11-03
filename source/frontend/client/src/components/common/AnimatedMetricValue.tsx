import { useAnimatedMetrics } from "@/hooks/useAnimatedMetrics";

type MetricWidth = "compact" | "standard" | "wide" | "auto";

interface AnimatedMetricValueProps {
  value: number | null | undefined;
  digits?: number;
  preserveRawValue?: boolean;
  durationMS?: number;
  format?: (value: number) => string;
  placeholder?: string;
  width?: MetricWidth;
  className?: string;
  resetKey?: string | number | null;
}

export function AnimatedMetricValue({
  resetKey,
  ...props
}: AnimatedMetricValueProps) {
  return <AnimatedMetricFrame key={resetKey ?? "continuous"} {...props} />;
}

function AnimatedMetricFrame({
  value,
  digits = 2,
  preserveRawValue = false,
  durationMS = 520,
  format,
  placeholder = "—",
  width = "standard",
  className = "",
}: AnimatedMetricValueProps) {
  const [displayed] = useAnimatedMetrics(
    [value],
    digits,
    durationMS,
    20,
    preserveRawValue
  );
  const finalValue =
    value !== null && value !== undefined && Number.isFinite(value)
      ? value
      : null;
  const visibleText =
    displayed === null
      ? placeholder
      : format
        ? format(displayed)
        : displayed.toFixed(digits);
  const accessibleText =
    finalValue === null
      ? placeholder
      : format
        ? format(finalValue)
        : finalValue.toFixed(digits);

  return (
    <span
      className={`animated-metric-value ${className}`.trim()}
      data-metric-width={width}
      data-value-state={finalValue === null ? "missing" : "ready"}
    >
      <span aria-hidden="true">{visibleText}</span>
      <span className="sr-only">{accessibleText}</span>
    </span>
  );
}
