import assert from "node:assert/strict";
import test from "node:test";

import { assetImpactInterpretation } from "./asset-impact-copy.ts";
import type { MarketShareChangeAsset } from "./market-share-types.ts";

function changeAsset(
  overrides: Partial<MarketShareChangeAsset>
): MarketShareChangeAsset {
  return {
    asset: "TEST",
    contribution_pp: 0,
    bithumb_direct_effect_pp: 0,
    competitor_effect_pp: 0,
    current_domestic_volume_krw: 0,
    previous_domestic_volume_krw: 0,
    domestic_volume_change_krw: 0,
    current_bithumb_volume_krw: 0,
    previous_bithumb_volume_krw: 0,
    bithumb_volume_change_krw: 0,
    current_competitor_volume_krw: 0,
    previous_competitor_volume_krw: 0,
    competitor_volume_change_krw: 0,
    ...overrides,
  } as MarketShareChangeAsset;
}

test("explains a vanished competitor market in simple Korean", () => {
  const copy = assetImpactInterpretation(
    changeAsset({
      contribution_pp: 5.22,
      competitor_effect_pp: 5.22,
      previous_domestic_volume_krw: 10_478_000,
      domestic_volume_change_krw: -10_478_000,
      previous_competitor_volume_krw: 10_478_000,
      competitor_volume_change_krw: -10_478_000,
    })
  );

  assert.equal(
    copy,
    "이 코인의 국내 거래금액은 약 1,048만 원에서 0원으로 줄었습니다. 빗썸 거래금액은 그대로였지만, 다른 거래소의 거래금액이 모두 사라지면서 빗썸 점유율을 5.22%p 높이는 데 도움이 됐습니다."
  );
  assert.doesNotMatch(copy, /기여|합한|변화 변화/);
});

test("keeps zero impact short", () => {
  assert.equal(
    assetImpactInterpretation(changeAsset({})),
    "이 코인의 국내 거래금액은 0원으로 그대로입니다. 빗썸 점유율에는 거의 영향을 주지 않았습니다."
  );
});
