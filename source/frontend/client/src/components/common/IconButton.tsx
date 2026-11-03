import type { ButtonHTMLAttributes } from "react";
import type { LucideIcon } from "lucide-react";

type IconButtonSize = "sm" | "md" | "lg";

const sizeClasses: Record<IconButtonSize, string> = {
  sm: "size-8",
  md: "size-9",
  lg: "size-11",
};

const iconClasses: Record<IconButtonSize, string> = {
  sm: "size-4",
  md: "size-[18px]",
  lg: "size-5",
};

export function IconButton({
  icon: Icon,
  label,
  size = "md",
  className = "",
  type = "button",
  ...props
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> & {
  icon: LucideIcon;
  label: string;
  size?: IconButtonSize;
}) {
  return (
    <button
      {...props}
      type={type}
      aria-label={label}
      title={props.title ?? label}
      className={`icon-button inline-flex shrink-0 items-center justify-center border border-border bg-secondary text-muted-foreground transition-[color,background-color,border-color,transform] duration-150 hover:border-primary/35 hover:bg-primary/[0.07] hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary active:scale-[0.97] disabled:cursor-not-allowed disabled:opacity-40 motion-reduce:transition-none motion-reduce:transform-none ${sizeClasses[size]} ${className}`}
    >
      <Icon className={iconClasses[size]} aria-hidden="true" />
    </button>
  );
}
