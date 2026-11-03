package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/internal/movement"
	"market-share-go/internal/tickertrend"
)

const marketQuestionFactLedgerLimit = 128

// marketQuestionDisplayFact is an internal, server-owned citation unit. Text is
// display-ready evidence, not a required answer sentence. No raw JSON number,
// question or previous model response is promoted into this ledger.
type marketQuestionDisplayFact struct {
	ID            string                        `json:"id"`
	Text          string                        `json:"text"`
	Metric        string                        `json:"metric"`
	Exchange      string                        `json:"exchange,omitempty"`
	Asset         string                        `json:"asset,omitempty"`
	Period        string                        `json:"period,omitempty"`
	EvidenceLayer string                        `json:"evidence_layer"`
	Quality       string                        `json:"quality"`
	Comparison    *marketQuestionFactComparison `json:"comparison,omitempty"`
}

type marketQuestionLedgerBuilder struct {
	facts []marketQuestionDisplayFact
	seen  map[string]bool
	bytes int
}

func (b *marketQuestionLedgerBuilder) add(fact marketQuestionDisplayFact) {
	fact.Text = strings.TrimSpace(fact.Text)
	// Never truncate a fact halfway through its qualification or numerical token.
	if fact.Text == "" || len(b.facts) >= marketQuestionFactLedgerLimit || len(fact.Text) > 40_000 || b.bytes+len(fact.Text) > 256_000 {
		return
	}
	fact.ID = ""
	encoded, _ := json.Marshal(fact)
	sum := sha256.Sum256(encoded)
	// Letter-only IDs cannot themselves become numerical evidence tokens.
	id := []byte("fact_")
	for _, value := range sum[:16] {
		id = append(id, 'a'+value>>4, 'a'+value&15)
	}
	fact.ID = string(id)
	if b.seen[fact.ID] {
		return
	}
	b.seen[fact.ID] = true
	b.bytes += len(fact.Text)
	b.facts = append(b.facts, fact)
}

func buildMarketQuestionFactLedger(payload marketanalysis.Analysis, officialWindow tickertrend.WindowComparison, wsInterval marketanalysis.MarketQuestionIntervalEvidence, query marketanalysis.MarketQuestionQuery, fallback marketanalysis.MarketQuestionAnswer) []marketQuestionDisplayFact {
	b := marketQuestionLedgerBuilder{seen: map[string]bool{}}
	switch query.Comparison {
	case marketanalysis.MarketQuestionRolling24hEndpointDelta:
		b.officialWindow(officialWindow, query)
	case marketanalysis.MarketQuestionCurrentVsPreviousInterval:
		b.interval(wsInterval, query)
		b.candle(wsInterval.CandleComparison)
	case marketanalysis.MarketQuestionCurrentSnapshot:
		if query.Intent == marketanalysis.MarketQuestionCurrentOfficialShare {
			b.currentTicker(payload, query)
		} else if query.Intent == marketanalysis.MarketQuestionAssetCapture && query.Asset != "" {
			if len(query.ComparedAssets) > 1 {
				for _, asset := range query.ComparedAssets {
					item := query
					item.Asset = asset
					b.currentAsset(payload, item)
				}
			} else {
				b.currentAsset(payload, query)
			}
		}
	}
	// Daily, historical and auxiliary projections retain their
	// existing validated display contract until independently typed here. Import
	// only the server fallback, not model prose. Put its qualification first so a
	// large legacy report cannot displace it with rows of numeric facts.
	legacy := append([]string{fallback.Caveat}, strings.Split(fallback.Answer, "\n\n")...)
	legacy = append(legacy, fallback.KeyPoints...)
	for _, paragraph := range legacy {
		b.add(marketQuestionDisplayFact{Text: paragraph, Metric: "LEGACY_DISPLAY", EvidenceLayer: "SERVER_DISPLAY_COMPATIBILITY", Quality: "SERVER_VERIFIED", Period: fmt.Sprintf("%s:%d:%d:%s:%d:%s", query.Comparison, query.TargetStartMS, query.TargetEndMS, query.TimeUnit, query.TimeValue, query.MarketScope)})
	}
	return b.facts
}

func (b *marketQuestionLedgerBuilder) currentAsset(payload marketanalysis.Analysis, query marketanalysis.MarketQuestionQuery) {
	basis := currentMarketQuestionAssetBasis(payload)
	if !basis.Complete || basis.AsOfMS <= 0 || query.Exchange != "" && query.Exchange != "bithumb" {
		return
	}
	metrics := currentMarketQuestionAssetMetrics(payload, query.Asset, true)
	if metrics == nil || metrics.CurrentDomesticVolumeKRW == nil {
		return
	}
	period := "KST " + marketQuestionLedgerKST(basis.AsOfMS) + " 기준 "
	layer, quality := "WEBSOCKET_ROLLING_WINDOW", "CONFIRMED"
	qualification := "WebSocket 직접 관측 rolling " + marketQuestionWSWindowLabel(basis.WindowSec)
	if basis.Official {
		layer, quality = "CURRENT_OFFICIAL_TICKER", "LIVE"
		qualification = "국내 5개 거래소 공식 rolling 24시간 티커"
	}
	period += qualification
	add := func(metric, exchange, text string) {
		b.add(marketQuestionDisplayFact{Text: period + ": " + text, Metric: metric, Exchange: exchange, Asset: metrics.Asset, Period: period, EvidenceLayer: layer, Quality: quality})
	}
	for _, row := range []struct {
		exchange, label string
		value           *float64
	}{
		{"domestic", metrics.Asset + " 국내 거래대금", metrics.CurrentDomesticVolumeKRW},
		{"bithumb", metrics.Asset + " 빗썸 거래대금", metrics.CurrentBithumbVolumeKRW},
	} {
		if row.value != nil && finiteNonnegative(*row.value) {
			metric := "EXCHANGE_VOLUME_KRW"
			if row.exchange == "domestic" {
				metric = "DOMESTIC_VOLUME_KRW"
			}
			add(metric, row.exchange, row.label+" "+formatQuestionKRWAmount(*row.value)+".")
		}
	}
	for _, row := range []struct {
		metric, label, denominator string
		value                      *float64
	}{
		{"ASSET_CAPTURE_PCT", "빗썸 포착률", "국내 동일 코인 거래대금", metrics.CurrentBithumbCapturePct},
		{"ASSET_INTERNAL_PCT", "빗썸 내부 비중", "빗썸 전체 거래대금", metrics.CurrentShareWithinBithumbPct},
		{"ASSET_TOTAL_SHARE_PCT", "빗썸 전체 점유율 수준 기여", "국내 전체 거래대금", metrics.CurrentTotalShareContributionPct},
	} {
		if marketQuestionLedgerPct(row.value) {
			add(row.metric, "bithumb", fmt.Sprintf("%s %s %.2f%%; 분모는 %s입니다.", metrics.Asset, row.label, *row.value, row.denominator))
		}
	}
	if !basis.Official && metrics.CurrentTradeCount != nil && *metrics.CurrentTradeCount >= 0 {
		add("TRADE_COUNT", "domestic", fmt.Sprintf("%s 국내 직접 관측 체결 %d건. 사람 수가 아닙니다.", metrics.Asset, *metrics.CurrentTradeCount))
	}
}

func marketQuestionFactLedgerBriefing(facts []marketQuestionDisplayFact) marketanalysis.MarketQuestionAnswer {
	texts := make([]string, 0, min(len(facts), marketQuestionFactLedgerLimit))
	for _, fact := range facts[:min(len(facts), marketQuestionFactLedgerLimit)] {
		texts = append(texts, fact.Text)
	}
	return marketanalysis.MarketQuestionAnswer{Answer: strings.Join(texts, "\n\n")}
}

func marketQuestionLedgerPct(value *float64) bool {
	return value != nil && finiteNonnegative(*value) && *value <= 100
}

func marketQuestionLedgerVenue(value string) bool {
	switch domain.Exchange(value) {
	case domain.ExchangeUpbit, domain.ExchangeBithumb, domain.ExchangeCoinone, domain.ExchangeKorbit, domain.ExchangeGopax:
		return true
	}
	return false
}

func marketQuestionLedgerKST(value int64) string {
	return time.UnixMilli(value).In(time.FixedZone("KST", 9*60*60)).Format("2006-01-02 15:04:05.000")
}

func (b *marketQuestionLedgerBuilder) currentTicker(payload marketanalysis.Analysis, query marketanalysis.MarketQuestionQuery) {
	ticker := payload.OfficialDomestic24h
	if query.MarketScope != "KRW" || ticker.Status != "LIVE" || ticker.ExpectedExchangeCount != 5 || ticker.ReadyExchangeCount != 5 ||
		ticker.TotalVolumeKRW == nil || !finitePositive(*ticker.TotalVolumeKRW) || !finitePositive(ticker.AsOf) || ticker.AsOf > 1e11 {
		if query.MarketScope == "KRW" && (ticker.Status == "PARTIAL" || ticker.Status == "STALE") {
			if ticker.ExpectedExchangeCount == 5 {
				b.add(marketQuestionDisplayFact{Text: "국내 5개 거래소의 전체 분모와 점유율은 자료가 불완전하여 확정할 수 없습니다.", Metric: "INCOMPLETE_EXPECTED_EXCHANGE_COUNT", Exchange: "domestic", EvidenceLayer: "CURRENT_OFFICIAL_TICKER", Quality: "PARTIAL"})
			}
			// A missing domestic denominator does not invalidate a live
			// exchange's own amount. Never release shares or domestic totals here.
			for _, exchange := range []domain.Exchange{domain.ExchangeUpbit, domain.ExchangeBithumb, domain.ExchangeCoinone, domain.ExchangeKorbit, domain.ExchangeGopax} {
				row, ok := ticker.Exchanges[exchange]
				if !ok || row.Exchange != exchange || row.Status != "LIVE" || row.VolumeKRW == nil || !finiteNonnegative(*row.VolumeKRW) || !finitePositive(row.AsOf) || row.AsOf > 1e11 {
					continue
				}
				period := "KST " + marketQuestionLedgerKST(int64(row.AsOf*1000)) + " 기준 공식 rolling 24시간"
				b.add(marketQuestionDisplayFact{Text: period + ": " + rollingExchangeLabel(string(exchange)) + " 거래대금 " + formatQuestionKRWAmount(*row.VolumeKRW) + "; 해당 거래소의 확인된 거래대금이며 국내 전체 분모와 점유율은 확정 전입니다.", Metric: "EXCHANGE_VOLUME_KRW", Exchange: string(exchange), Period: period, EvidenceLayer: "CURRENT_OFFICIAL_TICKER", Quality: "LIVE"})
			}
		}
		return
	}
	period := "KST " + marketQuestionLedgerKST(int64(ticker.AsOf*1000)) + " 기준 공식 rolling 24시간"
	add := func(metric, exchange, text string) {
		b.add(marketQuestionDisplayFact{Text: period + ": " + text, Metric: metric, Exchange: exchange, Period: period, EvidenceLayer: "CURRENT_OFFICIAL_TICKER", Quality: "LIVE"})
	}
	add("DOMESTIC_VOLUME_KRW", "domestic", "국내 전체 거래대금 "+formatQuestionKRWAmount(*ticker.TotalVolumeKRW)+"; 분모 범위는 업비트·빗썸·코인원·코빗·고팍스입니다.")
	for _, exchange := range []domain.Exchange{domain.ExchangeUpbit, domain.ExchangeBithumb, domain.ExchangeCoinone, domain.ExchangeKorbit, domain.ExchangeGopax} {
		row, ok := ticker.Exchanges[exchange]
		if !ok || row.Exchange != exchange || row.Status != "LIVE" || row.VolumeKRW == nil || !finiteNonnegative(*row.VolumeKRW) || *row.VolumeKRW > *ticker.TotalVolumeKRW {
			continue
		}
		label := rollingExchangeLabel(string(exchange))
		add("EXCHANGE_VOLUME_KRW", string(exchange), label+" 거래대금 "+formatQuestionKRWAmount(*row.VolumeKRW)+"; 해당 거래소의 최근 누적 거래금액입니다.")
		if marketQuestionLedgerPct(row.SharePercent) {
			add("EXCHANGE_SHARE_PCT", string(exchange), fmt.Sprintf("%s 점유율 %.2f%%; 분자는 해당 거래소 거래대금, 분모는 같은 공식 표본의 국내 전체 거래대금입니다.", label, *row.SharePercent))
		}
	}
	b.currentTickerComparisons(payload, query, period)
}

func (b *marketQuestionLedgerBuilder) officialWindow(w tickertrend.WindowComparison, query marketanalysis.MarketQuestionQuery) {
	window, requested := marketQuestionOfficialWindowSec(query)
	if query.MarketScope != "KRW" || (query.Exchange != "" && query.Exchange != "bithumb") || !requested || w.WindowSec != window || w.Status != "LIVE" || !w.EndpointComparison || w.Previous == nil || w.Current == nil {
		return
	}
	for _, p := range []*tickertrend.Point{w.Previous, w.Current} {
		if p.Status != "LIVE" || !finitePositive(p.TotalVolumeKRW) || !finiteNonnegative(p.BithumbVolumeKRW) || p.BithumbVolumeKRW > p.TotalVolumeKRW || !marketQuestionLedgerPct(&p.BithumbSharePct) || p.BucketMS <= 0 {
			return
		}
	}
	if w.Current.BucketMS <= w.Previous.BucketMS || (w.Current.BucketMS-w.Previous.BucketMS)%1000 != 0 || (w.Current.BucketMS-w.Previous.BucketMS)/1000 != window {
		return
	}
	period := fmt.Sprintf("KST %s → %s 공식 rolling 24시간 endpoint (국내 업비트·빗썸·코인원)", marketQuestionLedgerKST(w.Previous.BucketMS), marketQuestionLedgerKST(w.Current.BucketMS))
	base := marketQuestionDisplayFact{Period: period, EvidenceLayer: "OFFICIAL_ALIGNED_WINDOW", Quality: "LIVE"}
	add := func(metric, exchange, text string) {
		fact := base
		fact.Metric, fact.Exchange, fact.Text = metric, exchange, period+"의 공식 rolling 24시간 endpoint 비교: "+text+" 분모 범위는 업비트·빗썸·코인원입니다. 금액은 두 시점 사이에 새로 발생한 거래대금이 아닙니다."
		b.add(fact)
	}
	add("DOMESTIC_VOLUME_KRW", "domestic", fmt.Sprintf("국내 전체 거래대금 %s → %s.", formatQuestionKRWAmount(w.Previous.TotalVolumeKRW), formatQuestionKRWAmount(w.Current.TotalVolumeKRW)))
	add("EXCHANGE_VOLUME_KRW", "bithumb", fmt.Sprintf("빗썸 거래대금 %s → %s.", formatQuestionKRWAmount(w.Previous.BithumbVolumeKRW), formatQuestionKRWAmount(w.Current.BithumbVolumeKRW)))
	add("EXCHANGE_SHARE_PCT", "bithumb", fmt.Sprintf("빗썸 점유율 %.2f%% → %.2f%%; 빗썸 거래대금 / 국내 전체 거래대금.", w.Previous.BithumbSharePct, w.Current.BithumbSharePct))
	for _, delta := range []struct {
		metric, exchange, label string
		value                   *float64
	}{
		{"SHARE_DELTA_PP", "bithumb", "빗썸 점유율 변화", w.ShareDeltaPP},
		{"VOLUME_DELTA_KRW", "domestic", "국내 전체 거래대금 변화", w.DomesticVolumeDeltaKRW},
		{"VOLUME_DELTA_KRW", "bithumb", "빗썸 거래대금 변화", w.BithumbVolumeDeltaKRW},
	} {
		if delta.value != nil && finiteMovementNumber(*delta.value) {
			value := formatQuestionKRW(*delta.value)
			if delta.metric == "SHARE_DELTA_PP" {
				value = fmt.Sprintf("%+.2f%%p", *delta.value)
			}
			add(delta.metric, delta.exchange, delta.label+" "+value+".")
		}
	}
	b.drivers(base, w.Drivers, w.ExchangeDrivers)
}

func marketQuestionLedgerCompleteInterval(w marketanalysis.MarketQuestionIntervalEvidence, query marketanalysis.MarketQuestionQuery) bool {
	window, ok := marketQuestionWSWindowSec(query)
	if !ok || window <= 0 || window > marketQuestionMaxWSIntervalSec || w.WindowSec != window || w.Status != "CONFIRMED" || w.Source != "WEBSOCKET_1S_BUCKETS" || w.MarketScope != query.MarketScope {
		return false
	}
	for _, c := range []marketanalysis.MarketQuestionIntervalCoverage{w.CurrentCoverage, w.PreviousCoverage} {
		if c.Status != "CONFIRMED" || !c.WindowComplete || c.GapSeconds != 0 || c.RequiredSec != window || c.CoverageSec < window {
			return false
		}
	}
	return w.Current.StartMS > 0 && w.Previous.StartMS > 0 && w.Current.StartMS == w.Previous.EndMS &&
		w.Current.EndMS-w.Current.StartMS == window*1000 && w.Previous.EndMS-w.Previous.StartMS == window*1000
}

func (b *marketQuestionLedgerBuilder) interval(w marketanalysis.MarketQuestionIntervalEvidence, query marketanalysis.MarketQuestionQuery) {
	if !marketQuestionLedgerCompleteInterval(w, query) {
		return
	}
	period := fmt.Sprintf("직전 KST [%s, %s) / 최근 KST [%s, %s) 직접 관측 %s 시장", marketQuestionLedgerKST(w.Previous.StartMS), marketQuestionLedgerKST(w.Previous.EndMS), marketQuestionLedgerKST(w.Current.StartMS), marketQuestionLedgerKST(w.Current.EndMS), w.MarketScope)
	base := marketQuestionDisplayFact{Period: period, EvidenceLayer: "WEBSOCKET_1S_BUCKETS", Quality: "CONFIRMED"}
	add := func(metric, exchange, asset, text string) {
		if text == "" {
			return
		}
		fact := base
		fact.Metric, fact.Exchange, fact.Asset = metric, exchange, asset
		fact.Text = period + " 직접 관측 구간 합계: " + text + ". 공식 rolling 합계와 다른 근거이며 체결 건수는 사람 수가 아닙니다."
		b.add(fact)
	}
	pair := func(metric, exchange, asset, label string, previous, current *float64) {
		text := marketQuestionPairedAmount(label, previous, current)
		if strings.HasSuffix(metric, "_PCT") {
			text = ""
			if marketQuestionLedgerPct(previous) && marketQuestionLedgerPct(current) {
				text = fmt.Sprintf("%s: 직전 %.2f%% → 최근 %.2f%%", label, *previous, *current)
			}
		}
		add(metric, exchange, asset, text)
	}
	delta := func(metric, exchange, asset, label string, value *float64) {
		if value == nil || !finiteMovementNumber(*value) {
			return
		}
		text := formatQuestionKRW(*value)
		if strings.HasSuffix(metric, "_PP") {
			text = fmt.Sprintf("%+.2f%%p", *value)
		}
		add(metric, exchange, asset, label+" "+text)
	}
	pair("DOMESTIC_VOLUME_KRW", "domestic", "", "국내 전체 거래대금", w.Previous.DomesticVolumeKRW, w.Current.DomesticVolumeKRW)
	add("TRADE_COUNT", "domestic", "", marketQuestionPairedCount("국내 전체 관측 체결", w.Previous.DomesticTradeCount, w.Current.DomesticTradeCount))
	delta("VOLUME_DELTA_KRW", "domestic", "", "국내 전체 거래대금 변화", w.DomesticVolumeDeltaKRW)
	if target := w.TargetExchange; target != nil && marketQuestionLedgerVenue(target.Exchange) && target.Exchange == query.Exchange {
		label := rollingExchangeLabel(target.Exchange)
		pair("EXCHANGE_VOLUME_KRW", target.Exchange, "", label+" 거래대금", target.PreviousVolumeKRW, target.CurrentVolumeKRW)
		pair("EXCHANGE_SHARE_PCT", target.Exchange, "", label+" 점유율 (분모: 같은 구간 국내 전체 거래대금)", target.PreviousSharePct, target.CurrentSharePct)
		add("TRADE_COUNT", target.Exchange, "", marketQuestionPairedCount(label+" 관측 체결", target.PreviousTradeCount, target.CurrentTradeCount))
		delta("VOLUME_DELTA_KRW", target.Exchange, "", label+" 거래대금 변화", target.VolumeDeltaKRW)
		delta("SHARE_DELTA_PP", target.Exchange, "", label+" 점유율 변화 (분모: 같은 구간 국내 전체 거래대금)", target.ShareDeltaPP)
	}
	if query.Exchange != "" && query.Exchange != "bithumb" {
		return
	}
	pair("EXCHANGE_VOLUME_KRW", "bithumb", "", "빗썸 거래대금", w.Previous.BithumbVolumeKRW, w.Current.BithumbVolumeKRW)
	pair("EXCHANGE_SHARE_PCT", "bithumb", "", "빗썸 점유율 (분모: 같은 구간 국내 전체 거래대금)", w.Previous.BithumbSharePct, w.Current.BithumbSharePct)
	add("TRADE_COUNT", "bithumb", "", marketQuestionPairedCount("빗썸 관측 체결", w.Previous.BithumbTradeCount, w.Current.BithumbTradeCount))
	delta("VOLUME_DELTA_KRW", "bithumb", "", "빗썸 거래대금 변화", w.BithumbVolumeDeltaKRW)
	delta("SHARE_DELTA_PP", "bithumb", "", "빗썸 점유율 변화 (분모: 같은 구간 국내 전체 거래대금)", w.ShareDeltaPP)
	if a := w.Asset; a != nil && a.Asset != "" && (query.Asset == "" || strings.EqualFold(a.Asset, query.Asset)) {
		pair("DOMESTIC_VOLUME_KRW", "domestic", a.Asset, a.Asset+" 국내 거래대금", a.PreviousDomesticVolumeKRW, a.CurrentDomesticVolumeKRW)
		pair("EXCHANGE_VOLUME_KRW", "bithumb", a.Asset, a.Asset+" 빗썸 거래대금", a.PreviousBithumbVolumeKRW, a.CurrentBithumbVolumeKRW)
		pair("ASSET_CAPTURE_PCT", "bithumb", a.Asset, a.Asset+" 빗썸 포착률 (분모: 국내 동일 코인 거래대금)", a.PreviousBithumbCapturePct, a.CurrentBithumbCapturePct)
		pair("ASSET_INTERNAL_PCT", "bithumb", a.Asset, a.Asset+" 빗썸 내부 비중 (분모: 빗썸 전체 거래대금)", a.PreviousShareWithinBithumbPct, a.CurrentShareWithinBithumbPct)
		pair("ASSET_TOTAL_SHARE_PCT", "bithumb", a.Asset, a.Asset+" 전체 점유율 수준 기여 (분모: 국내 전체 거래대금)", a.PreviousTotalShareContributionPct, a.CurrentTotalShareContributionPct)
		add("TRADE_COUNT", "domestic", a.Asset, marketQuestionPairedCount(a.Asset+" 국내 관측 체결", a.PreviousTradeCount, a.CurrentTradeCount))
		delta("ASSET_CAPTURE_DELTA_PP", "bithumb", a.Asset, a.Asset+" 빗썸 포착률 변화 (분모: 국내 동일 코인 거래대금)", a.BithumbCaptureChangePP)
		delta("ASSET_INTERNAL_DELTA_PP", "bithumb", a.Asset, a.Asset+" 빗썸 내부 비중 변화 (분모: 빗썸 전체 거래대금)", a.ShareWithinBithumbChangePP)
		delta("ASSET_TOTAL_SHARE_DELTA_PP", "bithumb", a.Asset, a.Asset+" 전체 점유율 수준 기여 변화 (분모: 국내 전체 거래대금)", a.TotalShareContributionChangePP)
		delta("ASSET_ATTRIBUTION_PP", "bithumb", a.Asset, a.Asset+" 빗썸 점유율 변화 산술 기여 (분모: 국내 전체 거래대금)", a.ChangeAttributionPP)
	}
	assets := make([]movement.Driver, 0, min(len(w.Drivers), 256))
	for _, driver := range w.Drivers[:min(len(w.Drivers), 256)] {
		assets = append(assets, movement.Driver{Asset: driver.Asset, ContributionPP: driver.ContributionPP, BithumbVolumeChangeKRW: driver.BithumbVolumeChangeKRW, CompetitorVolumeChangeKRW: driver.CompetitorVolumeChangeKRW})
	}
	exchanges := make([]movement.ExchangeDriver, 0, min(len(w.ExchangeDrivers), 5))
	for _, driver := range w.ExchangeDrivers[:min(len(w.ExchangeDrivers), 5)] {
		exchanges = append(exchanges, movement.ExchangeDriver{Exchange: string(driver.Exchange), ContributionPP: driver.ContributionPP, VolumeChangeKRW: driver.VolumeChangeKRW})
	}
	b.drivers(base, assets, exchanges)
}

func (b *marketQuestionLedgerBuilder) drivers(base marketQuestionDisplayFact, assets []movement.Driver, exchanges []movement.ExchangeDriver) {
	valid := make([]movement.Driver, 0, min(len(assets), 256))
	for _, d := range assets[:min(len(assets), 256)] {
		if d.Asset != "" && finiteMovementNumber(d.ContributionPP) && finiteMovementNumber(d.BithumbVolumeChangeKRW) && finiteMovementNumber(d.CompetitorVolumeChangeKRW) {
			valid = append(valid, d)
		}
	}
	positive, negative := directionalMovementDrivers(valid, 5)
	for _, d := range append(positive, negative...) {
		fact := base
		fact.Metric, fact.Exchange, fact.Asset = "ASSET_ATTRIBUTION_PP", "bithumb", d.Asset
		fact.Text = fmt.Sprintf("%s에서 %s의 빗썸 점유율 산술 기여 %+.2f%%p; 빗썸 거래대금 변화 %s, 경쟁 거래소 변화 %s. 같은 국내 분모의 변화 분해이며 가격·공지의 인과관계가 아닙니다.", base.Period, d.Asset, d.ContributionPP, formatQuestionKRW(d.BithumbVolumeChangeKRW), formatQuestionKRW(d.CompetitorVolumeChangeKRW))
		b.add(fact)
	}
	for _, d := range exchanges[:min(len(exchanges), 5)] {
		if !marketQuestionLedgerVenue(d.Exchange) || !finiteMovementNumber(d.ContributionPP) || !finiteMovementNumber(d.VolumeChangeKRW) {
			continue
		}
		fact := base
		fact.Metric, fact.Exchange = "EXCHANGE_ATTRIBUTION_PP", d.Exchange
		fact.Text = fmt.Sprintf("%s에서 %s 거래대금 변화 %s, 빗썸 점유율 산술 기여 %+.2f%%p. 거래소별 분해와 코인별 분해는 같은 변화이므로 다시 합산하지 않습니다.", base.Period, rollingExchangeLabel(d.Exchange), formatQuestionKRW(d.VolumeChangeKRW), d.ContributionPP)
		b.add(fact)
	}
}
