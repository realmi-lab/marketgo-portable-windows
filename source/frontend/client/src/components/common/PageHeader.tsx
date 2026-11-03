/**
 * PageHeader — 모든 페이지의 통일 헤더.
 * h2 text-2xl font-semibold tracking-tight + 부제목 + 우측 actions 슬롯.
 * P0-7/P1-15 해결: 5가지 제목 스타일 → 1가지.
 */
import { type ReactNode } from "react";
import { MobileHelp } from "@/components/common/MobileHelp";

export function PageHeader({
  title,
  subtitle,
  icon,
  actions,
  titleHelp,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  icon?: ReactNode;
  actions?: ReactNode;
  titleHelp?: ReactNode;
}) {
  return (
    <div className="page-header flex flex-wrap items-center justify-between gap-3">
      <div className="page-header-heading flex min-w-0 items-start gap-2">
        {icon ? (
          <span className="page-header-icon mt-1" aria-hidden="true">
            {icon}
          </span>
        ) : null}
        <div className="min-w-0">
          <div
            className={`mobile-help-heading${titleHelp ? " flex items-center gap-1" : ""}`}
          >
            <h2 className="ui-title font-semibold tracking-tight">{title}</h2>
            {titleHelp ??
              (subtitle && (
                <MobileHelp
                  label={
                    typeof title === "string"
                      ? `${title} 화면 설명`
                      : "화면 설명"
                  }
                >
                  {subtitle}
                </MobileHelp>
              ))}
          </div>
          {subtitle && (
            <p className="ui-caption mobile-description-copy mt-1 max-w-5xl text-muted-foreground">
              {subtitle}
            </p>
          )}
        </div>
      </div>
      {actions && (
        <div className="page-header-actions flex flex-wrap items-center gap-2">
          {actions}
        </div>
      )}
    </div>
  );
}
