import assert from "node:assert/strict";
import test from "node:test";
import { appendPortableOfficialShare } from "./portable-share-history.ts";
import { flowChartTiming } from "./flow-chart-timing.ts";
import type { MarketShareOfficialDomestic24h } from "./market-share-types.ts";

function snapshot(asOf: number, share = 25) {
 return {as_of:asOf,status:"LIVE" as const,exchanges:{bithumb:{status:"LIVE",share_pct:share}}} as Pick<MarketShareOfficialDomestic24h,"as_of"|"status"|"exchanges">;
}
test("official frames stay connected during unrelated trade tick delays",()=>{
 const first=appendPortableOfficialShare([],snapshot(100),"live",false);
 const next=appendPortableOfficialShare(first,snapshot(110,26),"stale",false);
 assert.deepEqual(next.map(point=>point.at),[100000,110000]);
 assert.ok(next.every(point=>!point.gap));
 assert.equal(next[1].share_upbit,null);
 assert.equal(next[1].share_bithumb,26);
 assert.equal(appendPortableOfficialShare(next,snapshot(110,26),"live",false),next);
});
test("real disconnects, pause, and out of order frames retain their meaning",()=>{
 const first=appendPortableOfficialShare([],snapshot(100),"live",false);
 assert.equal(appendPortableOfficialShare(first,snapshot(110),"live",true),first);
 const disconnected=appendPortableOfficialShare(first,snapshot(110),"offline",false);
 assert.equal(disconnected.at(-1)?.gap,1);
 assert.equal(appendPortableOfficialShare(disconnected,snapshot(100),"live",false),disconnected);
 const resumed=appendPortableOfficialShare(disconnected,snapshot(120),"live",false);
 assert.equal(resumed[1].gap,1);
 assert.equal(resumed.at(-1)?.at,120000);
});
test("ten second sources keep their animation alive through their collection interval",()=>{
 assert.deepEqual(flowChartTiming(),{delayMS:1000,aheadMS:5000,dotFadeMS:300});
 const timing=flowChartTiming(10000);
 assert.ok(timing.delayMS>=10000);
 assert.ok(timing.aheadMS>=10000);
 assert.ok(timing.dotFadeMS>=1000);
});
