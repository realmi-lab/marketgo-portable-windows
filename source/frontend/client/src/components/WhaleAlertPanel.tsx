import { RadioTower, Waves } from "lucide-react";

import { Panel } from "@/components/common/Panel";
import { exchangeLabel } from "@/lib/exchanges";
import { formatKrwMagnitude } from "@/lib/formatters";
import {
  visibleWhaleAlerts,
  whaleAlertCountLabel,
  whaleAlertFailureNote,
  whaleAlertKindLabel,
  whaleAlertSideLabel,
} from "@/lib/market-share-whale-alert";
import type { MarketShareWhaleAlertState } from "@/lib/market-share-types";

function alertTime(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "—";
  return new Date(value).toLocaleTimeString("ko-KR", {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function quantity(value: number, asset: string): string {
  if (!Number.isFinite(value) || value <= 0) return `— ${asset}`;
  return `${value.toLocaleString("ko-KR", { maximumFractionDigits: 8 })} ${asset}`;
}

export function WhaleAlertPanel({
  state,
  unavailable = false,
}: {
  state: MarketShareWhaleAlertState | null;
  unavailable?: boolean;
}) {
  const events = visibleWhaleAlerts(state, 10);
  const latest = events[0] ?? null;
  const threshold = state?.threshold_krw ?? 500_000_000;

  return (
    <Panel
      title="고래 체결"
      description={`${formatKrwMagnitude(threshold)} 이상 단일 체결 또는 같은 거래소·코인·방향의 1초 분할 체결을 실시간으로 감지합니다.`}
      icon={<Waves className="h-4 w-4" />}
      actions={
        <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-secondary px-2.5 py-1 text-xs text-muted-foreground">
          <RadioTower className="h-3.5 w-3.5" aria-hidden="true" />
          {whaleAlertCountLabel(state)}
        </span>
      }
    >
      <div className="p-4 sm:p-5">
        {unavailable ? (
          <p role="status" className="mb-3 text-sm text-muted-foreground">
            {whaleAlertFailureNote(state, unavailable)}
          </p>
        ) : null}
        {latest ? (
          <div
            className={`rounded-xl border p-4 ${latest.side === "BUY" ? "border-profit/35 bg-profit/10" : "border-loss/35 bg-loss/10"}`}
            role="status"
            aria-live="polite"
          >
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <p className="text-xs font-semibold tracking-[0.12em] text-muted-foreground">
                  고래 등장 · {whaleAlertKindLabel(latest.kind)}
                </p>
                <p className="mt-1 text-xl font-bold tracking-tight">
                  {exchangeLabel(latest.exchange)} · {latest.asset} ·{" "}
                  {whaleAlertSideLabel(latest.side)}
                </p>
              </div>
              <p
                className={latest.side === "BUY" ? "text-profit" : "text-loss"}
              >
                <span className="text-xl font-bold">
                  {formatKrwMagnitude(latest.notional_krw)}
                </span>
                <span className="ml-2 text-xs text-muted-foreground">
                  {alertTime(latest.event_at_ms)}
                </span>
              </p>
            </div>
            <p className="mt-2 text-xs text-muted-foreground">
              {latest.market} · {quantity(latest.base_quantity, latest.asset)} ·
              체결 {latest.trade_count.toLocaleString("ko-KR")}건
            </p>
          </div>
        ) : unavailable ? null : (
          <div className="rounded-xl border border-dashed border-border bg-inset px-4 py-8 text-center">
            <Waves
              className="mx-auto h-5 w-5 text-muted-foreground"
              aria-hidden="true"
            />
            <p className="mt-3 text-sm font-semibold">
              5억 원 이상 체결을 기다리는 중입니다.
            </p>
            <p className="mt-1 text-xs text-muted-foreground">
              새 체결이 기준을 넘으면 이 자리에 즉시 표시됩니다.
            </p>
          </div>
        )}

        {events.length > 1 ? (
          <div className="mt-3 divide-y divide-border overflow-hidden rounded-xl border border-border">
            {events.slice(1).map(event => (
              <div
                key={event.id}
                className="grid gap-2 bg-card px-3 py-3 text-xs sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center"
              >
                <p className="min-w-0 font-semibold">
                  {exchangeLabel(event.exchange)} · {event.asset} ·{" "}
                  {whaleAlertSideLabel(event.side)}
                  <span className="ml-2 font-normal text-muted-foreground">
                    {whaleAlertKindLabel(event.kind)}
                  </span>
                </p>
                <p
                  className={event.side === "BUY" ? "text-profit" : "text-loss"}
                >
                  {formatKrwMagnitude(event.notional_krw)}
                </p>
                <time
                  className="text-muted-foreground"
                  dateTime={new Date(event.event_at_ms).toISOString()}
                >
                  {alertTime(event.event_at_ms)}
                </time>
              </div>
            ))}
          </div>
        ) : null}

        <p className="mt-3 text-xs leading-5 text-muted-foreground">
          큰 체결 1건 기준이며, 같은 사람의 주문인지는 알 수 없습니다.
        </p>
      </div>
    </Panel>
  );
}
