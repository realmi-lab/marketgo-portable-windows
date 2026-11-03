import { OBSERVED_RANGE_BASIS_LABEL } from "@/lib/selected-window-presentation";
import { MobileHelp } from "./MobileHelp";

/** Keep the partial status visible while explaining its basis on demand. */
export function ObservedRangeHelp() {
  return (
    <span className="mobile-help-heading">
      <span className="mg-attn">일부 관측</span>
      <MobileHelp label="일부 관측 설명">
        <p>
          이 점유율은 {OBSERVED_RANGE_BASIS_LABEL}이며 구간 전체의 점유율과 다를
          수 있습니다. 실제로 관측한 범위의 자료로 계산한 값입니다.
        </p>
        <p>
          서버 시작·재시작 후 선택한 구간의 자료가 아직 다 쌓이지 않았거나,
          실제 수집 공백으로 일부 시각·거래소의 자료를 받지 못했을 때 표시됩니다.
        </p>
        <p>받지 못한 자료를 0으로 채우지 않습니다.</p>
      </MobileHelp>
    </span>
  );
}
