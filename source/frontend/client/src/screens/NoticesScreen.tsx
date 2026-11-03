import { lazy, Suspense } from "react";

import { screenHash } from "@/lib/dashboard-navigation";
import { LLM_ENABLED } from "@/lib/app-features";
import { exchangeLabel } from "@/lib/exchanges";
import { noticeCategoryLabel } from "@/lib/market-share-labels";
import { formatMarketTimeMS } from "@/lib/market-time-format";
import type { ScreenProps } from "./AppShell";
import {
  DOMESTIC,
  NoticeCoinsCard,
  ShareTrendCard,
  useTodayNotices,
} from "./shared";
import {
  Card,
  Empty,
  exchangeColor,
  Foot,
  Legend,
  Seg,
  StaleNote,
  Table,
} from "./ui";

const NoticeList = lazy(() => import("@/pages/MarketNotices"));

/**
 * 공지: what was announced, when, and how the market stood then. The draft
 * tool opens below the tabs from the shell, which keeps it mounted.
 */
export default function NoticesScreen({ focus }: ScreenProps) {
  const draft = LLM_ENABLED && focus === "notice-draft";
  return (
    <div className={draft ? "mg-page mg-page-lead" : "mg-page"}>
      {LLM_ENABLED && (
        <div className="mg-row">
          <Seg
            label="공지 화면"
            options={[
              { value: "list", label: "공지 보기" },
              { value: "draft", label: "공지 초안 작성" },
            ]}
            value={draft ? "draft" : "list"}
            onChange={value => {
              window.location.hash = screenHash({
                screen: "notices",
                focus: value === "draft" ? "notice-draft" : undefined,
              });
            }}
          />
        </div>
      )}
      {draft ? null : (
        <>
          <div className="mg-grid">
            <TimelineCard />
            <BreakdownCard />
            <NoticeCoinsCard contributions={false} />
          </div>
          <Suspense fallback={<Empty>불러오는 중입니다.</Empty>}>
            <NoticeList desktopWorkspace archiveNotices />
          </Suspense>
        </>
      )}
    </div>
  );
}

/**
 * Today's notices on Bithumb's share line, in the bucket size picked: each
 * point's tooltip lists the notices of its bucket.
 */
function TimelineCard() {
  const { notices, items, countLabel, waiting } = useTodayNotices();
  return (
    <ShareTrendCard
      span={12}
      size="l"
      chartHeight={340}
      title="오늘 공지와 점유율"
      hours={24}
      marks={items.map(item => ({
        ms: item.published_ts_ms,
        color: exchangeColor(item.exchange),
        label: `${formatMarketTimeMS(item.published_ts_ms, "time")} ${exchangeLabel(item.exchange)}`,
        title: item.title,
      }))}
    >
      <StaleNote read={notices} />
      <Legend
        items={DOMESTIC.map(exchange => ({
          key: exchange,
          label: `${exchangeLabel(exchange)} 공지`,
          color: exchangeColor(exchange),
          dashed: true,
        }))}
      />
      <Foot>
        {waiting
          ? waiting
          : `오늘 ${countLabel}${
              items.length
                ? ` (${items
                    .slice(0, 8)
                    .map(
                      item =>
                        `${formatMarketTimeMS(item.published_ts_ms, "time")} ${exchangeLabel(item.exchange)}`
                    )
                    .join(", ")}${items.length > 8 ? " 외" : ""})`
                : ""
            }. 공지와 점유율은 같은 시간에 있었다는 뜻일 뿐 원인을 말하지 않습니다.`}
      </Foot>
    </ShareTrendCard>
  );
}

/** Today's notices counted by exchange and kind. */
function BreakdownCard() {
  const { items, waiting } = useTodayNotices();
  const categories = [...new Set(items.map(item => item.category))];
  return (
    <Card
      span={6}
      size="m"
      title="오늘 공지 나누어 보기"
      note="거래소와 종류별 건수"
    >
      {items.length === 0 ? (
        <Empty>{waiting ?? "오늘 올라온 공지가 없습니다."}</Empty>
      ) : (
        <Table
          label="오늘 공지 건수"
          head={
            <tr>
              <th>종류</th>
              {DOMESTIC.map(exchange => (
                <th key={exchange} className="mg-num">
                  {exchangeLabel(exchange)}
                </th>
              ))}
            </tr>
          }
        >
          {categories.map(category => (
            <tr key={category}>
              <td>{noticeCategoryLabel(category)}</td>
              {DOMESTIC.map(exchange => {
                const count = items.filter(
                  item =>
                    item.category === category && item.exchange === exchange
                ).length;
                return (
                  <td
                    key={exchange}
                    className={`mg-num ${count ? "" : "mg-muted"}`}
                  >
                    {count || "·"}
                  </td>
                );
              })}
            </tr>
          ))}
        </Table>
      )}
    </Card>
  );
}
