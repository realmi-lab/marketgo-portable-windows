import { useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  History,
  RefreshCw,
} from "lucide-react";

import { getMarketRegistryHistory } from "@/lib/api-client";
import { exchangeLabel } from "@/lib/exchanges";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import {
  actualMarketRegistryEvents,
  latestMarketRegistryRefreshes,
} from "@/lib/market-registry-history";
import { DOMESTIC_EXCHANGES } from "@/lib/market-share-config";
import type { MarketRegistryHistoryResponse } from "@/lib/market-share-types";

const REGISTRY_HISTORY_REFRESH_MS = 10 * 60 * 1_000;
const EMPTY_HISTORY: MarketRegistryHistoryResponse = {
  refreshes: [],
  events: [],
  generated_at: 0,
};
let retainedMarketRegistryHistory = EMPTY_HISTORY;

function registryTime(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "시각 기록 없음";
  return formatMarketTimeMS(value);
}

export function MarketRegistryChanges() {
  const [history, setHistory] = useState<MarketRegistryHistoryResponse>(
    retainedMarketRegistryHistory
  );
  const [loading, setLoading] = useState(
    retainedMarketRegistryHistory.generated_at <= 0
  );
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState(false);
  const [refreshToken, setRefreshToken] = useState(0);

  useEffect(() => {
    let active = true;
    let controller: AbortController | null = null;

    const load = async () => {
      if (document.visibilityState === "hidden") return;
      controller?.abort();
      const requestController = new AbortController();
      controller = requestController;
      setLoading(true);
      try {
        const response = await getMarketRegistryHistory(
          requestController.signal
        );
        if (!active || controller !== requestController) return;
        retainedMarketRegistryHistory = response;
        setHistory(response);
        setError("");
      } catch (caughtError) {
        if (
          !active ||
          controller !== requestController ||
          (caughtError as { name?: string }).name === "AbortError"
        )
          return;
        setError("마켓 변경 기록을 불러오지 못했습니다.");
      } finally {
        if (active && controller === requestController) {
          controller = null;
          setLoading(false);
        }
      }
    };

    const reloadWhenVisible = () => {
      if (document.visibilityState === "visible") void load();
    };
    void load();
    const timer = window.setInterval(
      () => void load(),
      REGISTRY_HISTORY_REFRESH_MS
    );
    document.addEventListener("visibilitychange", reloadWhenVisible);
    return () => {
      active = false;
      controller?.abort();
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", reloadWhenVisible);
    };
  }, [refreshToken]);

  const latestRefreshes = useMemo(
    () => latestMarketRegistryRefreshes(history.refreshes, DOMESTIC_EXCHANGES),
    [history.refreshes]
  );
  const changes = useMemo(
    () =>
      actualMarketRegistryEvents(
        history.events.filter(item =>
          DOMESTIC_EXCHANGES.some(exchange => exchange === item.exchange)
        )
      ),
    [history.events]
  );
  const successfulCount = latestRefreshes.filter(
    item => item.status === "SUCCESS" && item.authoritative
  ).length;
  const failedRefreshes = latestRefreshes.filter(
    item => item.status !== "SUCCESS" || !item.authoritative
  );
  const latestAt = Math.max(
    0,
    ...latestRefreshes.map(item => item.observed_at_ms)
  );
  const hasHistory = latestRefreshes.length > 0;
  const panelID = "market-registry-change-history";
  const buttonID = "market-registry-change-toggle";

  return (
    <div className="border-t border-border bg-inset">
      <div
        className="flex flex-wrap items-center justify-between gap-3 px-4 py-3"
        aria-live="polite"
      >
        <div className="flex min-w-0 items-center gap-2.5 text-xs">
          {error || failedRefreshes.length ? (
            <AlertTriangle className="h-4 w-4 shrink-0 text-gold" />
          ) : (
            <CheckCircle2 className="h-4 w-4 shrink-0 text-profit" />
          )}
          <div className="min-w-0">
            <p className="font-semibold text-foreground">
              {hasHistory
                ? `${successfulCount}/${DOMESTIC_EXCHANGES.length} 거래소 목록 확인`
                : "마켓 목록 확인 기록 없음"}
            </p>
            <p className="mt-0.5 text-base text-muted-foreground">
              {latestAt > 0 ? (
                <>
                  마지막 확인{" "}
                  <time dateTime={new Date(latestAt).toISOString()}>
                    {registryTime(latestAt)}
                  </time>
                  {failedRefreshes.length > 0 &&
                    ` · 오류 ${failedRefreshes.map(item => exchangeLabel(item.exchange)).join("·")}`}
                  {error && " · 최신 확인 실패 · 이전 기록 유지"}
                </>
              ) : error ? (
                error
              ) : (
                "10분마다 활성 마켓 목록을 다시 확인합니다."
              )}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {error && (
            <button
              type="button"
              onClick={() => {
                if (!loading) setRefreshToken(current => current + 1);
              }}
              className="inline-flex min-h-8 items-center gap-1.5 border border-gold/40 px-2.5 text-base text-gold hover:border-gold"
            >
              <RefreshCw className="h-3.5 w-3.5" />
              다시 확인
            </button>
          )}
          <button
            id={buttonID}
            type="button"
            aria-expanded={expanded}
            aria-controls={panelID}
            onClick={() => setExpanded(current => !current)}
            className="inline-flex min-h-8 items-center gap-1.5 border border-border bg-card px-2.5 text-base font-semibold text-foreground hover:border-primary hover:text-primary"
          >
            <History className="h-3.5 w-3.5" />
            마켓 변경 {changes.length > 0 ? `${changes.length}건` : "없음"}
            <ChevronDown
              className={`h-3.5 w-3.5 transition-transform ${expanded ? "rotate-180" : ""}`}
              aria-hidden="true"
            />
          </button>
        </div>
      </div>

      {expanded && (
        <div
          id={panelID}
          role="region"
          aria-labelledby={buttonID}
          className="border-t border-border bg-card"
        >
          <div className="grid gap-px bg-border sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-5">
            {DOMESTIC_EXCHANGES.map(exchange => {
              const item = latestRefreshes.find(
                refresh => refresh.exchange === exchange
              );
              const success = item?.status === "SUCCESS" && item.authoritative;
              return (
                <div key={exchange} className="min-w-0 bg-card px-3 py-2.5">
                  <div className="flex items-center justify-between gap-2 text-base">
                    <strong>{exchangeLabel(exchange)}</strong>
                    <span
                      className={
                        item
                          ? success
                            ? "text-profit"
                            : "text-loss"
                          : "text-muted-foreground"
                      }
                    >
                      {item ? (success ? "성공" : "오류") : "기록 없음"}
                    </span>
                  </div>
                  <p className="mt-1 truncate text-base text-muted-foreground">
                    {item ? registryTime(item.observed_at_ms) : "—"}
                    {item && !success ? " · 마지막 정상 목록 유지" : ""}
                  </p>
                </div>
              );
            })}
          </div>

          {changes.length > 0 ? (
            <ol className="max-h-72 overflow-y-auto divide-y divide-border/60">
              {changes.map(item => (
                <li
                  key={`${item.observed_at_ms}-${item.exchange}-${item.market}-${item.event_type}`}
                  className="grid gap-1 px-4 py-2.5 text-base sm:grid-cols-[5rem_7rem_minmax(0,1fr)_auto] sm:items-center sm:gap-3"
                >
                  <span
                    className={
                      item.event_type === "ADDED"
                        ? "font-semibold text-profit"
                        : "font-semibold text-loss"
                    }
                  >
                    {item.event_type === "ADDED" ? "추가" : "제거"}
                  </span>
                  <span>{exchangeLabel(item.exchange)}</span>
                  <span className="font-medium text-foreground">
                    {item.market}
                  </span>
                  <time
                    className="text-muted-foreground sm:text-right"
                    dateTime={new Date(item.observed_at_ms).toISOString()}
                  >
                    {registryTime(item.observed_at_ms)}
                  </time>
                </li>
              ))}
            </ol>
          ) : (
            <p className="px-4 py-4 text-xs text-muted-foreground">
              최초 기준선은 변경에서 제외합니다. 이후 추가·제거된 마켓은 아직
              없습니다.
            </p>
          )}
        </div>
      )}
    </div>
  );
}
