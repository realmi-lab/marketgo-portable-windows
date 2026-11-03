/**
 * KpiCard — 통일 KPI 카드.
 * glass-card p-4 + 라벨(text-xs muted) + 값(.stat-value) + 톤 색상.
 * P0-5/P1-1 해결: 3가지 KPI 레이아웃 → 1가지.
 */
import { type ReactNode } from "react";

// "neutral" is for levels and amounts: colour is kept for direction
// (profit/loss) and states that need attention (gold/degraded).
type StatusTone =
  "neutral" | "profit" | "loss" | "gold" | "info" | "muted" | "degraded";

const toneClass: Record<StatusTone, string> = {
  neutral: "text-foreground",
  profit: "text-profit",
  loss: "text-loss",
  gold: "text-gold",
  info: "text-info",
  muted: "text-muted-foreground",
  degraded: "text-gold",
};

export function KpiCard({
  label,
  value,
  sub,
  icon,
  tone,
  className,
  valueClassName,
}: {
  label: ReactNode;
  value: ReactNode;
  sub?: ReactNode;
  icon?: ReactNode;
  tone?: StatusTone;
  className?: string;
  valueClassName?: string;
}) {
  const valueClass = tone ? toneClass[tone] : "";
  const panelTone = tone ?? "muted";
  return (
    <div
      className={`glass-card metric-panel metric-panel-${panelTone} ${className ?? ""}`}
    >
      <div className="metric-panel-header">
        <span className="metric-panel-label">{label}</span>
        {icon && (
          <span className="metric-panel-icon" aria-hidden="true">
            {icon}
          </span>
        )}
      </div>
      <div className={`stat-value mt-2 ${valueClass} ${valueClassName ?? ""}`}>
        <span className="metric-value-settle inline-block">{value}</span>
      </div>
      {sub && <div className="ui-caption metric-panel-detail mt-1">{sub}</div>}
    </div>
  );
}
