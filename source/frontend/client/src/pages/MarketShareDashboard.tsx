import { lazy, startTransition, Suspense, useState } from "react";
import { BarChart3, Clock3, ListTree } from "lucide-react";

import { SegmentedControl } from "@/components/common/SelectionControl";
import { MobileHelp } from "@/components/common/MobileHelp";
import {
  initialMarketShareDashboardMode,
  marketShareViewForDashboardMode,
  type MarketShareDashboardMode,
} from "@/lib/market-share-dashboard-modes";

const MarketShare = lazy(() => import("@/pages/MarketShare"));
const RollingShareSummary2 = lazy(() => import("@/pages/RollingShareSummary2"));

export default function MarketShareDashboard({
  relatedNoticesOnly = false,
  preciseHourlyMetrics = false,
}: {
  relatedNoticesOnly?: boolean;
  preciseHourlyMetrics?: boolean;
}) {
  const [mode, setMode] = useState<MarketShareDashboardMode>(() =>
    initialMarketShareDashboardMode(window.location.hash)
  );
  const selectedView = marketShareViewForDashboardMode(mode);

  return (
    <div className="space-y-6">
      <section className="glass-card flex flex-wrap items-center justify-between gap-3 px-4 py-3 sm:px-5">
        <div>
          <div className="mobile-help-heading">
            <p className="market-share-dashboard-title ui-body font-semibold">
              통합 점유율 분석
            </p>
            <MobileHelp label="점유율 분석 기준 설명">
              공식 24시간 변화와 선택한 구간의 요약·상세 분석을 확인합니다.
            </MobileHelp>
          </div>
          <p className="ui-caption mt-1 text-muted-foreground mobile-description-copy">
            공식 24시간 변화와 선택한 구간의 요약·상세 분석을 확인합니다.
          </p>
        </div>
        <SegmentedControl
          className="market-share-dashboard-modes"
          ariaLabel="점유율 분석 기준"
          value={mode}
          onChange={nextMode => {
            startTransition(() => setMode(nextMode));
          }}
          options={[
            {
              value: "movement",
              label: "공식 24시간 변화",
              shortLabel: "24시간 변화",
              icon: <Clock3 className="h-3 w-3" />,
            },
            {
              value: "summary",
              label: "선택 구간 요약",
              shortLabel: "구간 요약",
              icon: <BarChart3 className="h-3 w-3" />,
            },
            {
              value: "window",
              label: "선택 구간 상세",
              shortLabel: "구간 상세",
              icon: <ListTree className="h-3 w-3" />,
            },
          ]}
        />
      </section>

      <Suspense
        fallback={
          <div className="dashboard-route-placeholder" aria-hidden="true" />
        }
      >
        {mode === "movement" ? (
          <RollingShareSummary2
            relatedNoticesOnly={relatedNoticesOnly}
            preciseHourlyMetrics={preciseHourlyMetrics}
          />
        ) : selectedView ? (
          <MarketShare view={selectedView} surface="summary" />
        ) : null}
      </Suspense>
    </div>
  );
}
