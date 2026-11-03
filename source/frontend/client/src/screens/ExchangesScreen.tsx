import { exchangeLabel } from "@/lib/exchanges";
import { formatKrwMagnitude, formatSharePercent } from "@/lib/formatters";
import type {
  MarketShareExchange,
  MarketShareLiquidityExchange,
} from "@/lib/market-share-types";
import type { ScreenProps } from "./AppShell";
import { ShareBar } from "./charts";
import { useMarketStream } from "./market-stream";
import {
  BestQuotesCard,
  CompositionCard,
  DOMESTIC,
  ExchangeChangeCard,
  useAnalysis,
  useWindowShare,
  WhaleCard,
} from "./shared";
import { Card, Empty, Ex, exchangeColor, Foot, Pct, Table, Won } from "./ui";

const bps = (value: number | null | undefined) =>
  typeof value === "number" && Number.isFinite(value)
    ? `${value.toFixed(1)}bp`
    : "—";

/** 거래소: how the competitors differ in trading, order books and markets. */
export default function ExchangesScreen(_props: ScreenProps) {
  return (
    <div className="mg-page">
      <div className="mg-grid">
        <CompositionCard span={7} size="m" />
        <ExchangeChangeCard span={5} size="m" />
        <TradingCard />
        <DepthCard />
        <VolumeVsDepthCard />
        <BestQuotesCard />
        <WhaleCard span={6} size="m" />
        <MarketsCard />
      </div>
    </div>
  );
}

function byExchange<T extends { exchange: string }>(
  rows: readonly T[] | undefined
): T[] {
  return DOMESTIC.map(exchange =>
    rows?.find(row => row.exchange === exchange)
  ).filter((row): row is T => Boolean(row));
}

/** Trading in the selected window: size, count, pace and who initiated. */
function TradingCard() {
  const analysis = useAnalysis();
  const share = useWindowShare();
  const rows: MarketShareExchange[] = byExchange(analysis?.exchanges);
  // Trades are counted from the stream; the official 24 hours has amounts only.
  const counted = !share.official;
  const count = (value: string) => (counted ? value : "—");
  return (
    <Card
      span={12}
      size="m"
      title={`${share.window} 거래 모습`}
      note={`체결 하나하나로 본 거래소 차이 (${share.source}${share.basis ? `, ${share.basis}` : ""})`}
    >
      {rows.length === 0 ? (
        <Empty>거래소 자료를 기다리는 중입니다.</Empty>
      ) : (
        <Table
          label="거래소별 거래 모습"
          head={
            <tr>
              <th>거래소</th>
              <th className="mg-num">거래금액</th>
              <th className="mg-num">점유율</th>
              <th className="mg-num">체결</th>
              <th className="mg-num">체결 비중</th>
              <th className="mg-num">초당 체결</th>
              <th className="mg-num">평균 체결</th>
              <th className="mg-num">매수 주도</th>
              <th>가장 큰 코인</th>
              <th className="mg-num">상위 5개 비중</th>
              <th className="mg-num">거래된 마켓</th>
            </tr>
          }
        >
          {rows.map(row => (
            <tr
              key={row.exchange}
              className={row.exchange === "bithumb" ? "mg-me" : undefined}
            >
              <td>
                <Ex exchange={row.exchange} />
              </td>
              <td className="mg-num">
                <Won value={row.volume_krw} />
              </td>
              <td className="mg-num">
                <Pct value={share.share === null ? null : row.share_pct} />
              </td>
              <td className="mg-num">
                {count(`${row.trade_count.toLocaleString("ko-KR")}건`)}
              </td>
              <td className="mg-num">
                <Pct
                  value={
                    counted && share.share !== null
                      ? row.trade_count_share_pct
                      : null
                  }
                />
              </td>
              <td className="mg-num">
                {count(`${row.trades_per_sec.toFixed(2)}건`)}
              </td>
              <td className="mg-num">
                <Won value={counted ? row.average_trade_krw : null} />
              </td>
              <td className="mg-num">
                <Pct value={counted ? row.aggressor_buy_pct : null} />
              </td>
              <td>
                {row.leading_asset || "—"}{" "}
                <span className="mg-muted">
                  {formatSharePercent(row.leading_asset_share_pct)}
                </span>
              </td>
              <td className="mg-num">
                <Pct value={row.top_5_share_pct} />
              </td>
              <td className="mg-num">
                {row.traded_market_count.toLocaleString("ko-KR")}/
                {row.registered_market_count.toLocaleString("ko-KR")}
              </td>
            </tr>
          ))}
        </Table>
      )}
      <Foot>
        {share.note ? `${share.note.replace(/\.$/, "")}. ` : ""}
        {share.official
          ? "24시간은 거래소 공식 거래금액이라 체결 수가 없습니다. "
          : ""}
        매수 주도는 사는 쪽이 먼저 주문을 낸 체결의 금액 비중입니다. 평균 체결은
        체결 한 건의 평균 금액입니다.
      </Foot>
    </Card>
  );
}

/** Order-book depth over ten levels, spreads and the cost to trade. */
function DepthCard() {
  const analysis = useAnalysis();
  const liquidity = analysis?.liquidity;
  const rows: MarketShareLiquidityExchange[] = byExchange(liquidity?.exchanges);
  return (
    <Card
      span={7}
      size="m"
      title="호가 깊이와 체결 비용"
      note={
        liquidity
          ? `호가 ${liquidity.analysis_depth}단계, ${formatKrwMagnitude(liquidity.reference_notional_krw)}을 한 번에 사고팔 때 기준`
          : "호가 자료를 기다리는 중"
      }
    >
      {rows.length === 0 ? (
        <Empty>호가 자료를 기다리는 중입니다.</Empty>
      ) : (
        <Table
          label="거래소별 호가"
          head={
            <tr>
              <th>거래소</th>
              <th className="mg-num">호가 금액</th>
              <th className="mg-num">호가 점유율</th>
              <th className="mg-num">스프레드(중앙)</th>
              <th className="mg-num">살 때 비용</th>
              <th className="mg-num">팔 때 비용</th>
              <th className="mg-num">호가 받는 마켓</th>
            </tr>
          }
        >
          {rows.map(row => (
            <tr
              key={row.exchange}
              className={row.exchange === "bithumb" ? "mg-me" : undefined}
            >
              <td>
                <Ex exchange={row.exchange} />
              </td>
              <td className="mg-num">
                <Won value={row.depth_krw} />
              </td>
              <td className="mg-num">
                <Pct value={row.liquidity_share_pct} />
              </td>
              <td className="mg-num">{bps(row.median_spread_bps)}</td>
              <td className="mg-num">{bps(row.average_buy_slippage_bps)}</td>
              <td className="mg-num">{bps(row.average_sell_slippage_bps)}</td>
              <td className="mg-num">
                <Pct value={row.live_market_pct} />
              </td>
            </tr>
          ))}
        </Table>
      )}
      <Foot>
        비용은 같은 금액을 즉시 사고팔 때 최우선 호가에서 벗어나는 정도(bp,
        0.01%)입니다. 조용한 마켓은 계산에서 뺍니다.
      </Foot>
    </Card>
  );
}

/** Each exchange's part of trading next to its part of the order books. */
function VolumeVsDepthCard() {
  const analysis = useAnalysis();
  const share = useWindowShare();
  const trading = byExchange(analysis?.exchanges);
  const depth = byExchange(analysis?.liquidity?.exchanges);
  return (
    <Card
      span={5}
      size="m"
      title="거래금액 대 호가 금액"
      note={`위 막대는 ${share.window} 거래금액 점유율(${share.source}${share.basis ? `, ${share.basis}` : ""}), 아래 막대는 호가 금액 점유율`}
    >
      <ul className="mg-list">
        {DOMESTIC.map(exchange => {
          const trade =
            share.share === null
              ? null
              : (trading.find(row => row.exchange === exchange)?.share_pct ??
                null);
          const book =
            depth.find(row => row.exchange === exchange)?.liquidity_share_pct ??
            null;
          return (
            <li key={exchange} style={{ display: "block" }}>
              <div
                className="mg-row"
                style={{ justifyContent: "space-between" }}
              >
                <Ex exchange={exchange} />
                <span className="mg-num">
                  거래 {formatSharePercent(trade)} · 호가{" "}
                  {formatSharePercent(book)}
                </span>
              </div>
              <ShareBar
                share={trade}
                max={100}
                color={exchangeColor(exchange)}
              />
              <div style={{ height: 4 }} />
              <ShareBar
                share={book}
                max={100}
                color="var(--muted-foreground)"
              />
            </li>
          );
        })}
      </ul>
      <Foot>
        호가 점유율이 거래 점유율보다 높으면 주문은 많이 걸려 있지만 실제 체결은
        덜 일어난다는 뜻입니다.
      </Foot>
    </Card>
  );
}

/** Markets each exchange lists and how many the stream receives. */
function MarketsCard() {
  const { registry } = useMarketStream();
  const analysis = useAnalysis();
  const source = registry ?? analysis?.registry;
  return (
    <Card
      span={6}
      size="m"
      title="연결된 마켓"
      note="거래소마다 등록된 마켓과 실시간으로 받는 마켓"
    >
      {!source ? (
        <Empty>마켓 목록을 기다리는 중입니다.</Empty>
      ) : (
        <>
          <Table
            label="연결된 마켓"
            head={
              <tr>
                <th>거래소</th>
                <th className="mg-num">마켓</th>
                <th className="mg-num">코인</th>
                <th>호가 통화</th>
                <th>상태</th>
              </tr>
            }
          >
            {DOMESTIC.map(exchange => {
              const row = source.exchanges?.[exchange];
              return (
                <tr key={exchange}>
                  <td>
                    <Ex exchange={exchange} />
                  </td>
                  <td className="mg-num">
                    {row ? row.market_count.toLocaleString("ko-KR") : "—"}
                  </td>
                  <td className="mg-num">
                    {row ? row.asset_count.toLocaleString("ko-KR") : "—"}
                  </td>
                  <td>
                    {row
                      ? Object.entries(row.quotes)
                          .map(([quote, count]) => `${quote} ${count}`)
                          .join(" · ")
                      : "—"}
                  </td>
                  {/* The server's error text stays in the tooltip; the cell says what it means. */}
                  <td
                    className={row?.error ? "mg-attn" : "mg-muted"}
                    title={row?.error || undefined}
                  >
                    {!row
                      ? "—"
                      : row.error
                        ? "목록 갱신 실패 · 마지막 목록 사용"
                        : "정상"}
                  </td>
                </tr>
              );
            })}
          </Table>
          <Foot>
            전체 {source.market_count?.toLocaleString("ko-KR") ?? "자료 없음"}개 마켓 · 실시간 수신 {source.ws_enabled_market_count == null ? "자료 없음" : `${source.ws_enabled_market_count.toLocaleString("ko-KR")}개`} ·{" "}
            {(source.ws_enabled_exchanges ?? []).map(exchangeLabel).join("·")}
          </Foot>
        </>
      )}
    </Card>
  );
}
