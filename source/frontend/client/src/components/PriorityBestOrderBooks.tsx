import {
  useEffect,
  useRef,
  useSyncExternalStore,
  type HTMLAttributes,
} from "react";
import { Waves } from "lucide-react";

import { Panel } from "@/components/common/Panel";
import { ExchangeBrandLabel } from "@/components/ExchangeBrandLabel";
import type {
  MarketShareConnection,
  MarketShareTickStore,
} from "@/hooks/useMarketShare";
import {
  bestBookExactNotional,
  bestBookNotional,
  bestBookPrice,
  bestBookQuantity,
} from "@/lib/best-orderbook-format";
import { exchangeNameKo } from "@/lib/exchanges";
import { formatSharePercent } from "@/lib/formatters";
import {
  bestBookDepthBalance,
  bestBookVisualChange,
  bestBookVisualSignature,
} from "@/lib/market-intelligence-experience";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import type {
  MarketShareBestOrderBook,
  MarketShareRealtimeTick,
} from "@/lib/market-share-types";
import "./priority-best-order-books.css";

/*
 * The best bid and ask of the main coins on Bithumb and Upbit, as the
 * dashboard showed them before the redesign: one card per coin, one row per
 * exchange, with each change marked as it arrives.
 */

export const PRIORITY_ORDER_BOOK_ASSETS = [
  "BTC",
  "USDT",
  "ETH",
  "XRP",
  "SOL",
] as const;
const PRIORITY_ORDER_BOOK_EXCHANGES = ["bithumb", "upbit"] as const;

function bestBookStatusLabel(status: string): string {
  if (status === "LIVE") return "실시간";
  if (status === "STALE") return "갱신 지연";
  if (status === "INCOMPLETE") return "호가 미완성";
  if (status === "SEQUENCE_GAP") return "순서 재동기화";
  return "수신 대기";
}

function bestBookStatusTone(status: string): string {
  if (status === "LIVE") return "text-profit";
  if (status === "STALE" || status === "SEQUENCE_GAP") return "text-gold";
  return "text-muted-foreground";
}

function PriorityBestOrderBookExchangeRow({
  asset,
  exchange,
  row,
  exchangeShare,
  shareWindowLabel,
  visualChange,
}: {
  asset: string;
  exchange: string;
  row: MarketShareBestOrderBook | undefined;
  exchangeShare: number | null | undefined;
  shareWindowLabel: string;
  visualChange: ReturnType<typeof bestBookVisualChange>;
}) {
  // Quotes are discrete observations. Animate their change indicators only;
  // interpolated prices and quantities never existed in the received book.
  const depthBalance = bestBookDepthBalance(
    row?.bid_notional_krw,
    row?.ask_notional_krw
  );
  const visualSignature = bestBookVisualSignature(row);
  const status = row?.status ?? "WAITING";

  return (
    <div
      className="best-book-exchange-row px-2.5 py-3"
      data-updated={visualChange.changed ? "true" : undefined}
    >
      {visualChange.changed ? (
        <span
          key={visualSignature}
          className="best-book-update-sweep"
          aria-hidden="true"
        />
      ) : null}
      <div className="best-book-row-heading mb-2 flex items-center justify-between gap-2 text-xs">
        <div className="flex min-w-0 items-center gap-1">
          <ExchangeBrandLabel exchange={exchange} />
          <span
            className="ui-caption whitespace-nowrap text-muted-foreground"
            title={`${asset} ${shareWindowLabel} 국내 거래금액 중 ${exchangeNameKo(exchange)} 비중`}
          >
            ({formatSharePercent(exchangeShare, 1)})
          </span>
        </div>
        <span className={bestBookStatusTone(status)}>
          {bestBookStatusLabel(status)}
          {typeof row?.age_sec === "number" && Number.isFinite(row.age_sec)
            ? ` · ${row.age_sec.toFixed(1)}초`
            : ""}
        </span>
      </div>
      <div className="best-book-values grid grid-cols-[minmax(0,1fr)_7rem_minmax(0,1fr)] items-center gap-1">
        <div className="min-w-0 text-left tabular-nums">
          <p className="ui-caption whitespace-nowrap text-muted-foreground">
            매수잔량
          </p>
          <p
            className="best-book-liquidity-value ui-caption whitespace-nowrap font-mono font-semibold text-profit"
            data-side="bid"
            data-change-direction={visualChange.bidNotional ?? undefined}
            data-updated={visualChange.bidNotional ? "true" : undefined}
            title={bestBookExactNotional(row?.bid_notional_krw)}
          >
            {bestBookNotional(row?.bid_notional_krw)}
          </p>
          <p
            className="best-book-quantity-value ui-caption whitespace-nowrap text-muted-foreground"
            data-side="bid"
            data-change-direction={visualChange.bidQuantity ?? undefined}
            data-updated={visualChange.bidQuantity ? "true" : undefined}
            title={bestBookQuantity(row?.bid_quantity, asset, 10)}
          >
            {bestBookQuantity(row?.bid_quantity)}
          </p>
        </div>
        <div className="best-book-prices min-w-0 text-center font-mono tabular-nums">
          <p
            className="best-book-price-value whitespace-nowrap text-sm font-semibold text-profit"
            data-change-direction={visualChange.bidPrice ?? undefined}
            title={`최우선 매수가격 ${bestBookPrice(row?.bid_price_krw)}`}
          >
            <span className="best-book-price-label hidden">매수 가격</span>
            {visualChange.bidPrice ? (
              <span className="best-book-direction-mark" aria-hidden="true">
                {visualChange.bidPrice === "up" ? "↑" : "↓"}
              </span>
            ) : null}
            {bestBookPrice(row?.bid_price_krw)}
          </p>
          <div className="my-1 border-t border-border" aria-hidden="true" />
          <p
            className="best-book-price-value whitespace-nowrap text-sm font-semibold text-loss"
            data-change-direction={visualChange.askPrice ?? undefined}
            title={`최우선 매도가격 ${bestBookPrice(row?.ask_price_krw)}`}
          >
            <span className="best-book-price-label hidden">매도 가격</span>
            {visualChange.askPrice ? (
              <span className="best-book-direction-mark" aria-hidden="true">
                {visualChange.askPrice === "up" ? "↑" : "↓"}
              </span>
            ) : null}
            {bestBookPrice(row?.ask_price_krw)}
          </p>
        </div>
        <div className="min-w-0 text-right tabular-nums">
          <p className="ui-caption whitespace-nowrap text-muted-foreground">
            매도잔량
          </p>
          <p
            className="best-book-liquidity-value ui-caption whitespace-nowrap font-mono font-semibold text-loss"
            data-side="ask"
            data-change-direction={visualChange.askNotional ?? undefined}
            data-updated={visualChange.askNotional ? "true" : undefined}
            title={bestBookExactNotional(row?.ask_notional_krw)}
          >
            {bestBookNotional(row?.ask_notional_krw)}
          </p>
          <p
            className="best-book-quantity-value ui-caption whitespace-nowrap text-muted-foreground"
            data-side="ask"
            data-change-direction={visualChange.askQuantity ?? undefined}
            data-updated={visualChange.askQuantity ? "true" : undefined}
            title={bestBookQuantity(row?.ask_quantity, asset, 10)}
          >
            {bestBookQuantity(row?.ask_quantity)}
          </p>
        </div>
      </div>
      {/* The track is drawn even before quotes arrive, so the row keeps its height. */}
      <div
        className="best-book-depth-balance"
        role={depthBalance ? "img" : undefined}
        aria-label={
          depthBalance
            ? `최우선 잔량 비율 매수 ${formatSharePercent(depthBalance.bidPct, 0)}, 매도 ${formatSharePercent(depthBalance.askPct, 0)}`
            : undefined
        }
        aria-hidden={depthBalance ? undefined : true}
        title={
          depthBalance
            ? `매수 ${formatSharePercent(depthBalance.bidPct, 0)} · 매도 ${formatSharePercent(depthBalance.askPct, 0)}`
            : undefined
        }
      >
        {depthBalance ? (
          <>
            <span
              className="best-book-depth-bid smooth-bar"
              style={{ width: `${depthBalance.bidPct}%` }}
            />
            <span
              className="best-book-depth-ask smooth-bar"
              style={{ width: `${depthBalance.askPct}%` }}
            />
          </>
        ) : null}
      </div>
    </div>
  );
}

/** "호가 10개 중 n개 실시간 · 시각", with a pulse while quotes are live. */
export function PriorityBestOrderBookStatus({
  tick,
  connection,
}: {
  tick: MarketShareRealtimeTick | null;
  connection: MarketShareConnection;
}) {
  const rows = tick?.best_order_books ?? [];
  const liveCount = rows.filter(row => row.status === "LIVE").length;
  const asOf = tick?.best_order_book_as_of ?? tick?.generated_at ?? 0;
  // The stream sends seconds; a value already in milliseconds is kept.
  const asOfMS = asOf < 10_000_000_000 ? asOf * 1000 : asOf;
  const live = connection === "live" && liveCount > 0;
  return (
    <span
      className={`inline-flex items-center gap-2 text-base ${live ? "text-profit" : "text-muted-foreground"}`}
    >
      <span
        className={`h-2 w-2 rounded-full ${live ? "best-book-live-indicator bg-profit" : "bg-muted-foreground/50"}`}
        aria-hidden="true"
      />
      호가 10개 중 {liveCount}개 실시간 ·{" "}
      {formatMarketTimeMS(asOfMS > 0 ? asOfMS : null, "time-seconds")}
    </span>
  );
}

/**
 * One card per coin with Bithumb's and Upbit's best quotes. Other attributes
 * go to the grid; a `className` is added to its own.
 */
export function PriorityBestOrderBookGrid({
  tick,
  className = "",
  ...attributes
}: {
  tick: MarketShareRealtimeTick | null;
} & HTMLAttributes<HTMLDivElement>) {
  const rows = tick?.best_order_books ?? [];
  const previousRowsRef = useRef<Map<string, MarketShareBestOrderBook>>(
    new Map()
  );
  const visualChangeByKey = new Map(
    rows.map(row => {
      const key = `${row.asset}:${row.exchange}`;
      return [
        key,
        bestBookVisualChange(previousRowsRef.current.get(key), row),
      ] as const;
    })
  );
  const rowByKey = new Map<string, MarketShareBestOrderBook>(
    rows.map(row => [`${row.asset}:${row.exchange}`, row] as const)
  );
  const assetExchangeShareByKey = new Map<string, number | null>(
    (tick?.asset_exchange_shares ?? []).flatMap(assetRow =>
      assetRow.exchanges.map(
        exchangeRow =>
          [
            `${assetRow.asset.toUpperCase()}:${exchangeRow.exchange.toLowerCase()}`,
            exchangeRow.share_pct,
          ] as const
      )
    )
  );
  const shareWindowLabel =
    typeof tick?.window_sec === "number" && tick.window_sec > 0
      ? `최근 ${tick.window_sec}초`
      : "현재 집계 구간";

  useEffect(() => {
    previousRowsRef.current = new Map(
      rows.map(row => [`${row.asset}:${row.exchange}`, row] as const)
    );
  }, [rows, tick?.best_order_book_version]);

  return (
    <div
      {...attributes}
      className={[
        "grid gap-3 p-3 sm:p-4 md:grid-cols-2 xl:grid-cols-3",
        className,
      ]
        .filter(Boolean)
        .join(" ")}
    >
      {PRIORITY_ORDER_BOOK_ASSETS.map(asset => (
        <article
          key={asset}
          className="min-w-0 overflow-hidden rounded-xl border border-border bg-inset"
          aria-label={`${asset} 업비트와 빗썸 최우선 호가`}
        >
          <header className="flex items-center justify-between border-b border-border px-3 py-2.5">
            <div>
              <strong className="font-mono text-base font-semibold">
                {asset}
              </strong>
              <span className="text-xs text-muted-foreground">/KRW</span>
            </div>
            <span className="text-xs text-muted-foreground">
              가장 좋은 가격
            </span>
          </header>
          <div className="divide-y divide-border">
            {PRIORITY_ORDER_BOOK_EXCHANGES.map(exchange => {
              const rowKey = `${asset}:${exchange}`;
              const row = rowByKey.get(rowKey);
              const visualChange =
                visualChangeByKey.get(rowKey) ??
                bestBookVisualChange(null, row);
              return (
                <PriorityBestOrderBookExchangeRow
                  key={exchange}
                  asset={asset}
                  exchange={exchange}
                  row={row}
                  exchangeShare={assetExchangeShareByKey.get(rowKey)}
                  shareWindowLabel={shareWindowLabel}
                  visualChange={visualChange}
                />
              );
            })}
          </div>
        </article>
      ))}
    </div>
  );
}

/** The whole panel, read from a tick store (the pages before the redesign). */
export function PriorityBestOrderBooks({
  tickStore,
  connection,
}: {
  tickStore: MarketShareTickStore;
  connection: MarketShareConnection;
}) {
  const tick = useSyncExternalStore(
    tickStore.subscribe,
    tickStore.getSnapshot,
    tickStore.getSnapshot
  );
  return (
    <Panel
      title="업비트·빗썸에서 가장 좋은 매수·매도 가격"
      description="BTC·USDT·ETH·XRP·SOL의 현재 가장 좋은 매수·매도 가격입니다. 금액은 그 가격에 남은 주문을 원화로 바꾼 값이고, 거래소 옆 괄호는 해당 코인 거래금액 중 그 거래소 몫입니다."
      icon={<Waves className="h-4 w-4" />}
      actions={
        <PriorityBestOrderBookStatus tick={tick} connection={connection} />
      }
      surfaceClassName="mi-panel"
      className="mt-5"
    >
      <PriorityBestOrderBookGrid tick={tick} />
    </Panel>
  );
}
