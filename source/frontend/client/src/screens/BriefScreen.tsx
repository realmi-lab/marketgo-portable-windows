import { ObservedRangeHelp } from "@/components/common/ObservedRangeHelp";
import { exchangeLabel } from "@/lib/exchanges";
import { formatKRW, formatSharePercent } from "@/lib/formatters";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import {
  comparisonScope,
  comparisonUsable,
  depthSentence,
  hourDriverSentence,
  kstHour,
  phaseSentence,
  shareBridgeSentence,
  windowComparisonNote,
  windowLabel,
  windowLeadSentence,
  sourceStatusLabel,
} from "@/lib/screen-copy";
import { ShareBar } from "./charts";
import { useMarketStream } from "./market-stream";
import {
  BridgeCard,
  DataStatusCard,
  DOMESTIC,
  HourContributionCard,
  ShareTrendCard,
  phaseLine,
  SecondFlowCard,
  TodayNoticesCard,
  useAnalysis,
  useTodayNotices,
  useWindowShare,
  VersusPrevious,
  WindowShareKpi,
  WindowsCard,
} from "./shared";
import { Card, Chip, Empty, Ex, Foot, Kpi, Pct, Pp, Table, Won } from "./ui";

/** 브리핑: where Bithumb stands now, conclusion first and evidence below. */
export default function BriefScreen() {
  return (
    <div className="mg-page">
      <BriefLead />
      <BriefKpis />
      <div className="mg-grid">
        <WindowsCard span={7} size="xl" />
        <SecondFlowCard span={5} size="xl" height={300} />
        <ShareTrendCard span={7} size="xl" chartHeight={460} />
        <BridgeCard span={5} size="xl" />
        <HourContributionCard span={6} size="l" />
        <CoinPreviewCard />
        <TodayNoticesCard span={7} size="s" />
        <DataStatusCard span={5} size="s" />
      </div>
    </div>
  );
}

function BriefLead() {
  const analysis = useAnalysis();
  const share = useWindowShare();
  const official = analysis?.official_domestic_24h;
  const hour = analysis?.official_ticker_1m;
  const movement = analysis?.official_share_monitor?.movement ?? null;
  const change = analysis?.change_attribution;
  const brief = analysis?.expert_brief;
  const bithumbDepth = analysis?.liquidity?.exchanges?.find(
    row => row.exchange === "bithumb"
  );
  // The leading coin is part of the comparison, so it needs a usable one.
  const topAsset = comparisonUsable(change)
    ? (brief?.attribution_headline?.primary_asset ??
      brief?.major_assets?.[0] ??
      null)
    : null;
  const points = [
    hourDriverSentence(
      hour?.share_delta_pp,
      hour?.exchange_drivers?.[0],
      formatKRW
    ),
    change ? shareBridgeSentence(share.window, change) : "",
    // Depth is compared with a complete window's trading share only.
    share.basis || share.official
      ? ""
      : depthSentence(
          share.share,
          bithumbDepth?.liquidity_share_pct,
          analysis?.summary?.bithumb_trade_count_share_pct
        ),
  ].filter(Boolean);
  const lead = [
    phaseSentence(movement, kstHour),
    windowLeadSentence(share, change, topAsset, { basisInHelp: true }),
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <section className="mg-card mg-lead" aria-label="지금 빗썸 점유율">
      <div>
        <p className="mg-lead-label">
          빗썸 공식 24시간 점유율 · 국내 {official?.ready_exchange_count ?? "—"}
          곳 ·{" "}
          {formatMarketTimeMS(official ? official.as_of * 1000 : null, "time")}
          {official && official.status !== "LIVE"
            ? ` · ${sourceStatusLabel(official.status)}`
            : ""}
        </p>
        <p className="mg-lead-value mg-bt">
          {formatSharePercent(official?.bithumb_share_pct)}
        </p>
        <div className="mg-lead-sub">
          <span>
            <Pp value={hour?.share_delta_pp} /> · 1시간 전보다 · 정시 3곳
          </span>
          {movement ? (
            <Chip tone={movement.direction === "UP" ? "ok" : "alert"}>
              {phaseLine(movement)}
            </Chip>
          ) : null}
        </div>
        {lead ? (
          <p className="mg-copy" style={{ marginTop: 12 }}>
            {lead}
            {share.basis ? <ObservedRangeHelp /> : null}
          </p>
        ) : null}
        {points.length ? (
          <ol className="mg-points">
            {points.map((point, index) => (
              <li key={point}>
                <span>{index + 1}</span>
                <span>{point}</span>
              </li>
            ))}
          </ol>
        ) : null}
      </div>
      <div>
        <Table
          label="거래소별 점유율"
          head={
            <tr>
              <th>거래소</th>
              <th className="mg-num">공식 24시간</th>
              <th className="mg-num">24시간 거래금액</th>
              <th className="mg-num">
                {share.window} {share.official ? "공식" : "실시간"}
              </th>
            </tr>
          }
        >
          {DOMESTIC.map(exchange => {
            const row = official?.exchanges?.[exchange];
            const live = analysis?.exchanges?.find(
              item => item.exchange === exchange
            );
            return (
              <tr
                key={exchange}
                className={exchange === "bithumb" ? "mg-me" : undefined}
              >
                <td>
                  <Ex exchange={exchange} />
                </td>
                <td className="mg-num">
                  <Pct value={row?.share_pct} />
                </td>
                <td className="mg-num">
                  <Won value={row?.volume_krw} />
                </td>
                <td className="mg-num">
                  <Pct value={share.share === null ? null : live?.share_pct} />
                </td>
              </tr>
            );
          })}
        </Table>
        <Foot>
          국내 {official?.ready_exchange_count ?? "—"}곳 24시간 합계{" "}
          <Won value={official?.total_volume_krw} /> ·{" "}
          {official
            ? `${official.ready_exchange_count}/${official.expected_exchange_count}곳 수신`
            : "자료 없음"}
          {official
            ? ` · ${formatMarketTimeMS(official.as_of * 1000, "time-seconds")}`
            : ""}
          {share.basis ? ` · ${share.window} 값은 ${share.basis}` : ""}
        </Foot>
      </div>
    </section>
  );
}

function BriefKpis() {
  const analysis = useAnalysis();
  const { windowSec } = useMarketStream();
  const share = useWindowShare();
  const { notices, items: todayNotices, countLabel } = useTodayNotices();
  const label = share.window;
  const official = analysis?.official_domestic_24h;
  const hour = analysis?.official_ticker_1m;
  const summary = analysis?.summary;
  const change = analysis?.change_attribution;
  // Trade counts come from observed trades; the official 24 hours has none.
  const counted = !share.official && share.share !== null;
  const liquidity = analysis?.liquidity;
  const depth = liquidity?.exchanges?.find(row => row.exchange === "bithumb");
  const buyLed = (exchange: string) =>
    analysis?.exchanges?.find(row => row.exchange === exchange)
      ?.aggressor_buy_pct ?? null;
  return (
    <div className="mg-kpis">
      <Kpi
        label="빗썸 24시간 거래금액"
        tag="공식"
        value={<Won value={official?.bithumb_volume_krw} />}
        detail={
          <>
            국내 <Won value={official?.total_volume_krw} /> 중
          </>
        }
      />
      <Kpi
        label="1시간 거래금액 변화"
        tag="정시 3곳"
        value={<Won value={hour?.domestic_volume_delta_krw} signed />}
        detail={
          <>
            빗썸 <Won value={hour?.bithumb_volume_delta_krw} signed />
          </>
        }
      />
      <WindowShareKpi
        comparison={<VersusPrevious windowSec={windowSec} change={change} />}
      />
      <Kpi
        label={`${label} 체결 건수 점유율`}
        value={
          <Pct
            value={counted ? summary?.bithumb_trade_count_share_pct : null}
          />
        }
        detail={
          counted && summary
            ? `${summary.bithumb_trade_count.toLocaleString("ko-KR")}건 / ${summary.domestic_trade_count.toLocaleString("ko-KR")}건${share.basis ? ` · ${share.basis}` : ""}`
            : share.official
              ? "공식 24시간에는 체결 수가 없습니다"
              : share.note || "—"
        }
      />
      <Kpi
        label="호가 금액 점유율"
        tag={
          liquidity?.analysis_depth
            ? `${liquidity.analysis_depth}단계`
            : undefined
        }
        value={<Pct value={depth?.liquidity_share_pct} />}
        detail={
          <>
            빗썸 <Won value={liquidity?.bithumb_depth_krw} /> / 국내{" "}
            <Won value={liquidity?.domestic_depth_krw} />
          </>
        }
      />
      <Kpi
        label={`빗썸 ${label} 매수 주도 비중`}
        value={<Pct value={buyLed("bithumb")} />}
        detail={`업비트 ${formatSharePercent(buyLed("upbit"))} · 코인원 ${formatSharePercent(buyLed("coinone"))}`}
      />
      <Kpi
        label="빗썸 상장 코인의 거래 비중"
        value={
          <Pct value={summary?.bithumb_volume_weighted_listing_coverage_pct} />
        }
        detail={
          summary
            ? `국내 거래 코인 ${summary.domestic_asset_count.toLocaleString("ko-KR")}개 중 ${summary.bithumb_listed_asset_count.toLocaleString("ko-KR")}개 상장`
            : "—"
        }
      />
      <Kpi
        label="오늘 공지"
        value={notices.data ? countLabel : "—"}
        detail={
          notices.data
            ? `빗썸 ${todayNotices.filter(item => item.exchange === "bithumb").length}건${notices.stale ? " · 갱신 실패" : ""}`
            : notices.error || "불러오는 중"
        }
      />
    </div>
  );
}

/** The coins that moved the share most, each with Bithumb's part of it. */
function CoinPreviewCard() {
  const analysis = useAnalysis();
  const { windowSec } = useMarketStream();
  const change = analysis?.change_attribution;
  // A coin's contribution is its part of the comparison with the previous window.
  const assets = comparisonUsable(change)
    ? (analysis?.expert_brief?.major_assets ?? []).slice(0, 8)
    : [];
  const scope = comparisonScope(change);
  return (
    <Card
      span={6}
      size="l"
      title="코인 미리 보기"
      note={`${windowLabel(windowSec)} 동안 기여가 큰 코인${scope ? ` (${scope})` : ""}. 막대는 그 코인 거래 중 빗썸의 몫입니다.`}
      actions={
        <a className="mg-button" href="#coins">
          코인에서 크게
        </a>
      }
    >
      {assets.length === 0 ? (
        <Empty>
          코인별 기여는 직전 구간과 비교할 수 있을 때 나옵니다.{" "}
          {comparisonUsable(change)
            ? ""
            : windowComparisonNote(windowSec, change)}
        </Empty>
      ) : (
        <Table
          label="기여가 큰 코인"
          head={
            <tr>
              <th>코인</th>
              <th className="mg-bar-col" data-wide="">
                빗썸 비중
              </th>
              <th className="mg-num">기여</th>
            </tr>
          }
        >
          {assets.map(asset => (
            <tr key={asset.asset}>
              <td>
                <span className="mg-name">{asset.asset}</span>{" "}
                <span className="mg-muted">
                  {asset.domestic_exchanges.length === 1
                    ? `${exchangeLabel(asset.domestic_exchanges[0])}만`
                    : `${asset.domestic_exchanges.length}곳`}
                </span>
              </td>
              <td>
                <ShareBar share={asset.current_bithumb_capture_pct} max={100} />
                <span className="mg-num mg-muted">
                  {formatSharePercent(asset.current_bithumb_capture_pct)}
                </span>
              </td>
              <td className="mg-num">
                <Pp value={asset.contribution_pp} />
              </td>
            </tr>
          ))}
        </Table>
      )}
    </Card>
  );
}
