import type { HTMLAttributes, ReactNode } from "react";
import { MobileHelp } from "@/components/common/MobileHelp";

export function Panel({
  title,
  description,
  descriptionRole = "help",
  icon,
  actions,
  children,
  className = "",
  surfaceClassName = "instrument-panel",
  ...sectionProps
}: Omit<HTMLAttributes<HTMLElement>, "title"> & {
  title: ReactNode;
  description?: ReactNode;
  descriptionRole?: "help" | "status";
  icon?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  surfaceClassName?: string;
}) {
  return (
    <section
      {...sectionProps}
      className={`glass-card overflow-hidden ${surfaceClassName} ${className}`}
    >
      <div className="instrument-panel-header flex flex-wrap items-start justify-between gap-4 border-b border-border px-4 py-4 sm:px-5">
        <div className="panel-heading flex min-w-0 items-start gap-2">
          {icon ? (
            <span className="panel-title-icon mt-1" aria-hidden="true">
              {icon}
            </span>
          ) : null}
          <div className="min-w-0">
            <div className="mobile-help-heading">
              <h3 className="ui-title font-semibold tracking-tight">{title}</h3>
              {description && descriptionRole === "help" ? (
                <MobileHelp
                  label={
                    typeof title === "string"
                      ? `${title} 카드 설명`
                      : "카드 설명"
                  }
                >
                  {description}
                </MobileHelp>
              ) : null}
            </div>
            {description ? (
              <p
                className={`ui-caption panel-description ${descriptionRole === "help" ? "mobile-description-copy" : ""} mt-1 max-w-4xl text-muted-foreground`}
                role={descriptionRole === "status" ? "status" : undefined}
              >
                {description}
              </p>
            ) : null}
          </div>
        </div>
        {actions ? (
          <div className="panel-actions flex min-w-0 flex-wrap items-center gap-2">
            {actions}
          </div>
        ) : null}
      </div>
      {children}
    </section>
  );
}
