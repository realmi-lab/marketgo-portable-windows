import { lazy, Suspense, type ReactNode } from "react";

import { Empty } from "./ui";

/*
 * The two date-based indexes, each a screen of its own. They are the pages
 * from before the redesign, shown in the dashboard's page frame.
 */

const ExchangeIndex = lazy(() => import("@/pages/MarketIndex"));
const CoinIndex = lazy(() => import("@/pages/MarketIndex2"));

function IndexPage({
  loading,
  wide = false,
  children,
}: {
  loading: string;
  /** Uses the whole width of a wide screen (screens.css `.mg-page-wide`). */
  wide?: boolean;
  children: ReactNode;
}) {
  return (
    <div className={`mg-page mg-index-page${wide ? " mg-page-wide" : ""}`}>
      <Suspense fallback={<Empty>{loading}</Empty>}>{children}</Suspense>
    </div>
  );
}

/** 거래소 인덱스: each exchange's official trading, date by date. */
export function ExchangeIndexScreen() {
  return (
    <IndexPage loading="날짜별 거래소 거래금액을 불러오는 중입니다.">
      <ExchangeIndex />
    </IndexPage>
  );
}

/**
 * 코인 인덱스: each coin's trading across exchanges, date by date. Its three
 * panes take the full width of a wide screen.
 */
export function CoinIndexScreen() {
  return (
    <IndexPage loading="날짜별 코인 거래금액을 불러오는 중입니다." wide>
      <CoinIndex />
    </IndexPage>
  );
}
