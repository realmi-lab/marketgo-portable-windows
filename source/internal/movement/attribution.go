package movement

import (
	"math"
	"sort"
	"strings"
)

const attributionVolumeRelativeTolerance = 1e-9
const attributionShareAbsoluteTolerancePP = 1e-8

type marketImpact struct {
	asset, exchange                 string
	previous, current, contribution float64
}

// AttributeSnapshots decomposes the exact share change between two official
// ticker snapshots. The returned contribution values are percentage points.
func AttributeSnapshots(previous, current *Snapshot) ([]Driver, []ExchangeDriver) {
	if previous == nil || current == nil || previous.TotalVolumeKRW <= 0 || current.TotalVolumeKRW <= 0 {
		return nil, nil
	}
	return attribute(previous, current)
}

// AttributeExchangeSnapshots decomposes the share change using exchange-level
// totals only. It is useful for historical samples written before per-asset
// ticker values were retained.
func AttributeExchangeSnapshots(previous, current *Snapshot) []ExchangeDriver {
	if !exchangeRowsReconcile(previous) || !exchangeRowsReconcile(current) {
		return nil
	}
	bithumbCoefficient, competitorCoefficient := coefficients(
		previous.BithumbVolumeKRW,
		previous.TotalVolumeKRW,
		current.BithumbVolumeKRW,
		current.TotalVolumeKRW,
	)
	keys := make(map[string]struct{}, len(previous.Exchanges)+len(current.Exchanges))
	for exchange := range previous.Exchanges {
		keys[exchange] = struct{}{}
	}
	for exchange := range current.Exchanges {
		keys[exchange] = struct{}{}
	}
	result := make([]ExchangeDriver, 0, len(keys))
	for exchange := range keys {
		before := previous.Exchanges[exchange].VolumeKRW
		after := current.Exchanges[exchange].VolumeKRW
		coefficient := competitorCoefficient
		if exchange == "bithumb" {
			coefficient = bithumbCoefficient
		}
		result = append(result, ExchangeDriver{
			Exchange: exchange, ContributionPP: (after - before) * coefficient,
			VolumeChangeKRW: after - before, PreviousVolumeKRW: before, CurrentVolumeKRW: after,
		})
	}
	sort.Slice(result, func(i, j int) bool { return exchangeImpactLess(result[i], result[j]) })
	if len(result) > 0 {
		target := current.BithumbSharePct - previous.BithumbSharePct
		sum := 0.0
		for _, row := range result {
			sum += row.ContributionPP
		}
		result[0].ContributionPP += target - sum
		sort.Slice(result, func(i, j int) bool { return exchangeImpactLess(result[i], result[j]) })
	}
	for i := range result {
		result[i].Rank = i + 1
	}
	return result
}

func attribute(previous, current *Snapshot) ([]Driver, []ExchangeDriver) {
	if !assetRowsReconcile(previous) || !assetRowsReconcile(current) {
		return nil, nil
	}
	impacts := attributeMarkets(previous, current)
	type assetTotal struct{ contribution, previousB, currentB, previousOther, currentOther float64 }
	assetTotals := map[string]assetTotal{}
	type exchangeTotal struct {
		contribution, previous, current float64
		leading                         marketImpact
	}
	exchangeTotals := map[string]exchangeTotal{}
	for _, impact := range impacts {
		a := assetTotals[impact.asset]
		a.contribution += impact.contribution
		if impact.exchange == "bithumb" {
			a.previousB += impact.previous
			a.currentB += impact.current
		} else {
			a.previousOther += impact.previous
			a.currentOther += impact.current
		}
		assetTotals[impact.asset] = a
		e := exchangeTotals[impact.exchange]
		e.contribution += impact.contribution
		e.previous += impact.previous
		e.current += impact.current
		if math.Abs(impact.contribution) > math.Abs(e.leading.contribution) {
			e.leading = impact
		}
		exchangeTotals[impact.exchange] = e
	}
	drivers := make([]Driver, 0, len(assetTotals))
	for asset, row := range assetTotals {
		drivers = append(drivers, Driver{
			Asset:                       asset,
			ContributionPP:              row.contribution,
			BithumbVolumeChangeKRW:      row.currentB - row.previousB,
			CompetitorVolumeChangeKRW:   row.currentOther - row.previousOther,
			PreviousBithumbVolumeKRW:    row.previousB,
			CurrentBithumbVolumeKRW:     row.currentB,
			PreviousCompetitorVolumeKRW: row.previousOther,
			CurrentCompetitorVolumeKRW:  row.currentOther,
		})
	}
	sort.Slice(drivers, func(i, j int) bool { return driverImpactLess(drivers[i], drivers[j]) })
	for i := range drivers {
		drivers[i].Rank = i + 1
	}
	exchanges := make([]ExchangeDriver, 0, len(exchangeTotals))
	for exchange, row := range exchangeTotals {
		exchanges = append(exchanges, ExchangeDriver{
			Exchange:          exchange,
			ContributionPP:    row.contribution,
			VolumeChangeKRW:   row.current - row.previous,
			PreviousVolumeKRW: row.previous,
			CurrentVolumeKRW:  row.current,
			LeadingAsset:      row.leading.asset,
			LeadingAssetPP:    row.leading.contribution,
		})
	}
	sort.Slice(exchanges, func(i, j int) bool { return exchangeImpactLess(exchanges[i], exchanges[j]) })
	for i := range exchanges {
		exchanges[i].Rank = i + 1
	}
	return drivers, exchanges
}

func attributeMarkets(previous, current *Snapshot) []marketImpact {
	bithumbCoefficient, competitorCoefficient := coefficients(
		previous.BithumbVolumeKRW,
		previous.TotalVolumeKRW,
		current.BithumbVolumeKRW,
		current.TotalVolumeKRW,
	)
	keys := map[AssetKey]struct{}{}
	for key := range previous.Assets {
		keys[key] = struct{}{}
	}
	for key := range current.Assets {
		keys[key] = struct{}{}
	}
	result := make([]marketImpact, 0, len(keys))
	for key := range keys {
		coefficient := competitorCoefficient
		if key.Exchange == "bithumb" {
			coefficient = bithumbCoefficient
		}
		before, after := previous.Assets[key], current.Assets[key]
		result = append(result, marketImpact{
			asset: key.Asset, exchange: key.Exchange,
			previous: before, current: after,
			contribution: (after - before) * coefficient,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := math.Abs(result[i].contribution), math.Abs(result[j].contribution)
		if left != right {
			return left > right
		}
		if result[i].contribution != result[j].contribution {
			return result[i].contribution > result[j].contribution
		}
		if result[i].asset != result[j].asset {
			return result[i].asset < result[j].asset
		}
		return result[i].exchange < result[j].exchange
	})
	if len(result) > 0 {
		target := current.BithumbSharePct - previous.BithumbSharePct
		sum := 0.0
		for _, row := range result {
			sum += row.contribution
		}
		result[0].contribution += target - sum
	}
	return result
}

func assetRowsReconcile(snapshot *Snapshot) bool {
	if !snapshotTotalsReconcile(snapshot) || len(snapshot.Assets) == 0 {
		return false
	}
	total, bithumb := 0.0, 0.0
	for key, volume := range snapshot.Assets {
		if key.Exchange != strings.ToLower(strings.TrimSpace(key.Exchange)) || key.Exchange == "" ||
			key.Asset != strings.ToUpper(strings.TrimSpace(key.Asset)) || key.Asset == "" ||
			!finiteNonnegativeAttribution(volume) {
			return false
		}
		total += volume
		if key.Exchange == "bithumb" {
			bithumb += volume
		}
		if math.IsInf(total, 0) || math.IsInf(bithumb, 0) {
			return false
		}
	}
	return attributionVolumesEqual(total, snapshot.TotalVolumeKRW) &&
		attributionVolumesEqual(bithumb, snapshot.BithumbVolumeKRW)
}

func exchangeRowsReconcile(snapshot *Snapshot) bool {
	if !snapshotTotalsReconcile(snapshot) || len(snapshot.Exchanges) == 0 {
		return false
	}
	total, bithumb := 0.0, 0.0
	bithumbFound := false
	for exchange, row := range snapshot.Exchanges {
		if exchange != strings.ToLower(strings.TrimSpace(exchange)) || exchange == "" ||
			!finiteNonnegativeAttribution(row.VolumeKRW) {
			return false
		}
		total += row.VolumeKRW
		if exchange == "bithumb" {
			bithumb, bithumbFound = row.VolumeKRW, true
		}
		if math.IsInf(total, 0) {
			return false
		}
	}
	return bithumbFound && attributionVolumesEqual(total, snapshot.TotalVolumeKRW) &&
		attributionVolumesEqual(bithumb, snapshot.BithumbVolumeKRW)
}

func snapshotTotalsReconcile(snapshot *Snapshot) bool {
	if snapshot == nil || !finiteNonnegativeAttribution(snapshot.BithumbVolumeKRW) ||
		!finiteNonnegativeAttribution(snapshot.TotalVolumeKRW) || snapshot.TotalVolumeKRW <= 0 ||
		snapshot.BithumbVolumeKRW > snapshot.TotalVolumeKRW || math.IsNaN(snapshot.BithumbSharePct) ||
		math.IsInf(snapshot.BithumbSharePct, 0) {
		return false
	}
	wantShare := snapshot.BithumbVolumeKRW / snapshot.TotalVolumeKRW * 100
	return math.Abs(snapshot.BithumbSharePct-wantShare) <= attributionShareAbsoluteTolerancePP
}

func finiteNonnegativeAttribution(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func attributionVolumesEqual(left, right float64) bool {
	scale := math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
	return math.Abs(left-right) <= scale*attributionVolumeRelativeTolerance
}

func driverImpactLess(left, right Driver) bool {
	leftAbs, rightAbs := math.Abs(left.ContributionPP), math.Abs(right.ContributionPP)
	if leftAbs != rightAbs {
		return leftAbs > rightAbs
	}
	if left.ContributionPP != right.ContributionPP {
		return left.ContributionPP > right.ContributionPP
	}
	return left.Asset < right.Asset
}

func exchangeImpactLess(left, right ExchangeDriver) bool {
	leftAbs, rightAbs := math.Abs(left.ContributionPP), math.Abs(right.ContributionPP)
	if leftAbs != rightAbs {
		return leftAbs > rightAbs
	}
	if left.ContributionPP != right.ContributionPP {
		return left.ContributionPP > right.ContributionPP
	}
	return left.Exchange < right.Exchange
}

func coefficients(oldB, oldTotal, newB, newTotal float64) (float64, float64) {
	oldC, newC := oldTotal-oldB, newTotal-newB
	bithumb, competitor := unitPathCoefficients(oldB, oldC, newB, newC)
	return bithumb * 100, competitor * 100
}

// unitPathCoefficients integrates the exact derivatives of B/(B+C) along the
// straight line between the two endpoint volumes. The closed form avoids the
// gross factor-allocation error that fixed-step quadrature can produce when
// valid endpoint totals differ by many orders of magnitude.
func unitPathCoefficients(oldB, oldC, newB, newC float64) (float64, float64) {
	oldTotal, newTotal := oldB+oldC, newB+newC
	if oldTotal <= 0 || newTotal <= 0 {
		return 0, 0
	}
	change := newTotal - oldTotal
	scale := math.Max(oldTotal, newTotal)
	if math.Abs(change) > scale*1e-8 {
		deltaB := newB - oldB
		allocation := deltaB / change
		shareDelta := newB/newTotal - oldB/oldTotal
		logRatio := math.Log(newTotal) - math.Log(oldTotal)
		bithumb := ((1-allocation)*logRatio + shareDelta) / change
		competitor := (shareDelta - allocation*logRatio) / change
		return correctedUnitPathCoefficients(bithumb, competitor, oldB, oldC, newB, newC)
	}

	oldBNormalized, oldCNormalized := oldB/scale, oldC/scale
	newBNormalized, newCNormalized := newB/scale, newC/scale
	oldTotalNormalized, newTotalNormalized := oldTotal/scale, newTotal/scale
	inverseEndpointProduct := 1 / (oldTotalNormalized * newTotalNormalized)
	centeredMoment := unitPathCenteredMoment(oldTotalNormalized, newTotalNormalized)
	bithumb := ((oldCNormalized+newCNormalized)/2*inverseEndpointProduct +
		(newCNormalized-oldCNormalized)*centeredMoment) / scale
	competitor := -((oldBNormalized+newBNormalized)/2*inverseEndpointProduct +
		(newBNormalized-oldBNormalized)*centeredMoment) / scale
	return correctedUnitPathCoefficients(bithumb, competitor, oldB, oldC, newB, newC)
}

func correctedUnitPathCoefficients(bithumb, competitor, oldB, oldC, newB, newC float64) (float64, float64) {
	deltaB, deltaC := newB-oldB, newC-oldC
	target := newB/(newB+newC) - oldB/(oldB+oldC)
	for range 2 {
		residual := target - deltaB*bithumb - deltaC*competitor
		if residual == 0 {
			break
		}
		if math.Abs(deltaB) >= math.Abs(deltaC) && deltaB != 0 {
			bithumb += residual / deltaB
		} else if deltaC != 0 {
			competitor += residual / deltaC
		} else {
			break
		}
	}
	return bithumb, competitor
}

func unitPathCenteredMoment(oldTotal, newTotal float64) float64 {
	middle := (oldTotal + newTotal) / 2
	change := newTotal - oldTotal
	if change == 0 {
		return 0
	}
	x := change / (oldTotal + newTotal)
	if math.Abs(x) < 1e-3 {
		power, xSquared, sum := x, x*x, 0.0
		for order := 1; order <= 6; order++ {
			sum += float64(order) / float64(2*order+1) * power
			power *= xSquared
		}
		return -sum / (middle * middle)
	}
	logRatio := math.Log(newTotal) - math.Log(oldTotal)
	return (logRatio - middle*change/oldTotal/newTotal) / (change * change)
}
