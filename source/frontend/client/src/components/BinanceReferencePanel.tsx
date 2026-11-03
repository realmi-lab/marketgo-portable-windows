import { useEffect, useMemo, useState } from "react";
import { ChevronLeft, ChevronRight, CircleDot, Search } from "lucide-react";

import { Panel } from "@/components/common/Panel";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import {
  buildPriceGapCandidate,
  formatRelativePricePercent,
  parsePercentRate,
} from "@/lib/binance-price-gap";
import {
  formatReferenceKrwPrice,
  premiumIdentityStatusLabel,
  premiumStatusLabel,
  referencePremiumBps,
} from "@/lib/binance-reference";
import { formatKrwCompact } from "@/lib/formatters";
import { clampPage } from "@/lib/market-share-controls";
import type { MarketShareOfficialBinance24h } from "@/lib/market-share-types";

const PAGE_SIZE = 50;

function usdtVolume(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return `${value.toLocaleString("en-US", { maximumFractionDigits: 2 })} USDT`;
}

function referenceTime(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "—";
  return formatMarketTimeMS(value);
}

function statusLabel(value: string | undefined): string {
  if (value === "LIVE") return "정상";
  if (value === "PARTIAL") return "일부 수집";
  if (value === "STALE") return "갱신 지연";
  return "사용 불가";
}

function priceGapPercent(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return "—";
  return formatRelativePricePercent(value / 100);
}

function krw(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return "—";
  return formatKrwCompact(value).replace(/^\+/, "");
}

function candidateLabel(
  candidate:
    | { direction: string; gross?: number; net?: number | null }
    | null
    | undefined,
  key: "gross" | "net"
): string {
  if (!candidate) return "—";
  const value = candidate[key];
  if (value === null || value === undefined || !Number.isFinite(value))
    return "—";
  return `${candidate.direction} ${formatRelativePricePercent(value * 100)}`;
}

export function BinanceReferencePanel({
  reference,
}: {
  reference: MarketShareOfficialBinance24h;
}) {
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const [bithumbFeePct, setBithumbFeePct] = useState("0.04");
  const [binanceFeePct, setBinanceFeePct] = useState("0.10");
  const bithumbFee = parsePercentRate(bithumbFeePct);
  const binanceFee = parsePercentRate(binanceFeePct);
  const feesAvailable = bithumbFee !== null && binanceFee !== null;
  const rows = useMemo(() => {
    const needle = search.trim().toUpperCase();
    return reference.assets
      .filter(
        asset => asset.asset.includes(needle) || asset.market.includes(needle)
      )
      .sort((left, right) => right.quote_volume_usdt - left.quote_volume_usdt);
  }, [reference.assets, search]);
  const pages = Math.max(1, Math.ceil(rows.length / PAGE_SIZE));
  const safePage = clampPage(page, rows.length, PAGE_SIZE);
  const visible = rows.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE);

  useEffect(() => setPage(1), [search]);
  useEffect(() => {
    setPage(current => clampPage(current, rows.length, PAGE_SIZE));
  }, [rows.length]);

  return (
    <Panel
      title="글로벌 시장 참고값"
      description={`바이낸스 공식 USDT 현물시장의 독립적인 24시간 참고값 ${rows.length.toLocaleString("ko-KR")}개입니다. 국내 점유율·증감 계산에는 사용하지 않습니다.`}
      actions={
        <label className="market-table-search">
          <Search className="h-4 w-4 text-muted-foreground" />
          <input
            value={search}
            onChange={event => setSearch(event.target.value)}
            placeholder="코인 검색"
            className="w-32 bg-transparent outline-none placeholder:text-muted-foreground/70"
          />
        </label>
      }
    >
      <div className="binance-premium-toolbar">
        <div>
          <span>원화 환산</span>
          <strong>
            빗썸 USDT {formatReferenceKrwPrice(reference.usdt_krw_rate)}
          </strong>
        </div>
        <label>
          <span>빗썸 수수료 가정</span>
          <span className="binance-fee-input">
            <input
              value={bithumbFeePct}
              onChange={event => setBithumbFeePct(event.target.value)}
              inputMode="decimal"
              aria-label="빗썸 수수료 가정"
              aria-invalid={bithumbFee === null}
            />
            %
          </span>
        </label>
        <label>
          <span>바이낸스 수수료 가정</span>
          <span className="binance-fee-input">
            <input
              value={binanceFeePct}
              onChange={event => setBinanceFeePct(event.target.value)}
              inputMode="decimal"
              aria-label="바이낸스 수수료 가정"
              aria-invalid={binanceFee === null}
            />
            %
          </span>
        </label>
        <p>
          현재 가장 좋은 가격과 빗썸의 매수·매도 중간 가격을 단순 비교합니다.
          남은 주문량·예상 가격 손해·송금·지연은 반영하지 않습니다.
          {!feesAvailable ? (
            <span className="block text-loss" role="status">
              수수료를 0~100%의 숫자로 입력해 주세요. 적용 후 가격 차이는 입력을
              확인한 뒤 표시합니다.
            </span>
          ) : null}
        </p>
      </div>
      <div className="market-table-scroll max-h-[540px]">
        <table className="market-data-table market-data-table-sticky-first w-full text-left text-xs">
          <thead>
            <tr>
              <th>코인</th>
              <th className="text-right">바이낸스 거래쌍</th>
              <th className="text-right">24시간 거래금액</th>
              <th className="text-right">원화로 환산</th>
              <th className="text-right">바이낸스의 좋은 매수·매도 가격</th>
              <th className="text-right">빗썸 매수·매도 중간 가격</th>
              <th className="text-right">수수료 적용 전 가격 차이</th>
              <th className="text-right">수수료 적용 후 가격 차이</th>
              <th className="text-right">측정 종료</th>
              <th className="text-right">상태</th>
            </tr>
          </thead>
          <tbody>
            {visible.map(asset => {
              const premium = referencePremiumBps(
                asset.premium_bps,
                asset.premium_identity_status,
                asset.premium_status
              );
              const candidate = buildPriceGapCandidate(
                asset.premium_bithumb_mid_krw,
                asset.estimated_best_bid_krw,
                asset.estimated_best_ask_krw,
                bithumbFee,
                binanceFee
              );
              return (
                <tr key={asset.market}>
                  <td className="whitespace-nowrap font-semibold">
                    <span className="market-table-identity">
                      <span className="market-table-coin-icon">
                        <CircleDot className="h-3 w-3" />
                      </span>
                      {asset.asset}
                    </span>
                  </td>
                  <td className="whitespace-nowrap text-right">
                    {asset.market}
                  </td>
                  <td className="whitespace-nowrap text-right">
                    {usdtVolume(asset.quote_volume_usdt)}
                  </td>
                  <td className="whitespace-nowrap text-right">
                    {krw(asset.estimated_volume_krw)}
                  </td>
                  <td className="whitespace-nowrap text-right">
                    <span className="binance-best-quote">
                      <span>
                        매수{" "}
                        {formatReferenceKrwPrice(asset.estimated_best_ask_krw)}
                      </span>
                      <span>
                        매도{" "}
                        {formatReferenceKrwPrice(asset.estimated_best_bid_krw)}
                      </span>
                    </span>
                  </td>
                  <td className="whitespace-nowrap text-right">
                    {formatReferenceKrwPrice(asset.premium_bithumb_mid_krw)}
                  </td>
                  <td className="whitespace-nowrap text-right">
                    <span className="binance-candidate-value">
                      {candidateLabel(candidate?.gross, "gross")}
                      <small>
                        최근 체결가 대비 빗썸 중간가 {priceGapPercent(premium)}
                      </small>
                    </span>
                  </td>
                  <td className="whitespace-nowrap text-right">
                    <span className="binance-candidate-value">
                      {candidateLabel(candidate?.net, "net")}
                      <small>
                        {feesAvailable
                          ? `${bithumbFeePct}% + ${binanceFeePct}% 가정`
                          : "수수료 입력 확인 필요"}
                      </small>
                    </span>
                  </td>
                  <td className="whitespace-nowrap text-right">
                    {referenceTime(asset.close_time_ms)}
                  </td>
                  <td
                    className="whitespace-nowrap text-right"
                    title={premiumIdentityStatusLabel(
                      asset.premium_identity_status
                    )}
                  >
                    {statusLabel(reference.status)} ·{" "}
                    {premiumStatusLabel(asset.premium_status, premium)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      <div className="market-table-footer">
        <span>
          {rows.length.toLocaleString("ko-KR")}개 중 {visible.length}개 표시
        </span>
        <div className="flex items-center gap-2">
          <button
            type="button"
            disabled={safePage <= 1}
            onClick={() => setPage(value => Math.max(1, value - 1))}
            className="market-table-page-button"
          >
            <ChevronLeft className="h-4 w-4" /> 이전
          </button>
          <span className="min-w-20 text-center tabular-nums text-foreground">
            {safePage} / {pages}
          </span>
          <button
            type="button"
            disabled={safePage >= pages}
            onClick={() => setPage(value => Math.min(pages, value + 1))}
            className="market-table-page-button"
          >
            다음 <ChevronRight className="h-4 w-4" />
          </button>
        </div>
      </div>
    </Panel>
  );
}
