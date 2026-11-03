import type { ReactNode } from "react";

export interface SegmentedControlOption<Value extends string> {
  value: Value;
  label: string;
  /** Shown on phones instead of label; the button keeps label as its name. */
  shortLabel?: string;
  icon?: ReactNode;
}

export function SegmentedControl<Value extends string>({
  value,
  options,
  onChange,
  ariaLabel,
  className = "",
}: {
  value: Value;
  options: readonly SegmentedControlOption<Value>[];
  onChange: (value: Value) => void;
  ariaLabel: string;
  className?: string;
}) {
  return (
    <div
      className={`segmented-control flex rounded-lg border border-border bg-well p-1 shadow-inner ${className}`}
      role="group"
      aria-label={ariaLabel}
    >
      {options.map(option => {
        const selected = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            aria-pressed={selected}
            aria-label={option.shortLabel ? option.label : undefined}
            onClick={() => onChange(option.value)}
            className={`segmented-control-item inline-flex min-h-8 items-center justify-center gap-2 border px-3 py-1.5 text-sm font-medium transition-[color,background-color,border-color,box-shadow,transform] duration-150 ease-out focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary active:translate-y-px motion-reduce:transition-none motion-reduce:transform-none ${
              selected
                ? "segmented-control-item-active border-primary/40 bg-primary/10 text-primary shadow-sm"
                : "border-transparent text-muted-foreground hover:border-border hover:bg-card hover:text-foreground"
            }`}
          >
            {option.icon && (
              <span className="shrink-0" aria-hidden="true">
                {option.icon}
              </span>
            )}
            {option.shortLabel ? (
              <>
                <span className="segmented-control-label">{option.label}</span>
                <span
                  className="segmented-control-short-label"
                  aria-hidden="true"
                >
                  {option.shortLabel}
                </span>
              </>
            ) : (
              <span>{option.label}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

export function ToggleChip({
  pressed,
  onClick,
  children,
  className = "",
}: {
  pressed: boolean;
  onClick: () => void;
  children: ReactNode;
  className?: string;
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      onClick={onClick}
      className={`toggle-chip inline-flex min-h-8 items-center justify-center rounded-md border px-3 py-1.5 text-base font-medium transition-[color,background-color,border-color,box-shadow,transform] duration-150 ease-out focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary active:translate-y-px motion-reduce:transition-none motion-reduce:transform-none ${
        pressed
          ? "border-primary/45 bg-primary/10 text-primary shadow-sm"
          : "border-border bg-transparent text-muted-foreground hover:border-primary/30 hover:bg-primary/[0.05] hover:text-foreground"
      } ${className}`}
    >
      {children}
    </button>
  );
}
