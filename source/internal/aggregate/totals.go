package aggregate

import (
	"errors"
	"math"
)

const (
	volumeZeroTolerance = 1e-8
	krwZeroTolerance    = 1e-4
)

type VolumeKey struct {
	Scope    string
	Exchange string
	Market   string
	Base     string
	Quote    string
}

type Totals struct {
	NativeVolume    float64
	KRWVolume       float64
	TradeCount      uint64
	UnresolvedCount uint64
	BuyKRWVolume    float64
	SellKRWVolume   float64
	BaseVolume      float64
}

func (t *Totals) Add(value Totals) {
	t.NativeVolume += value.NativeVolume
	t.KRWVolume += value.KRWVolume
	t.TradeCount += value.TradeCount
	t.UnresolvedCount += value.UnresolvedCount
	t.BuyKRWVolume += value.BuyKRWVolume
	t.SellKRWVolume += value.SellKRWVolume
	t.BaseVolume += value.BaseVolume
}

func (t Totals) Validate() error {
	for _, value := range []float64{t.NativeVolume, t.KRWVolume, t.BuyKRWVolume, t.SellKRWVolume, t.BaseVolume} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return errors.New("aggregate totals must be finite and nonnegative")
		}
	}
	if t.TradeCount > math.MaxInt64 || t.UnresolvedCount > math.MaxInt64 {
		return errors.New("aggregate counters exceed PostgreSQL BIGINT capacity")
	}
	return nil
}

func (t *Totals) CheckedAdd(value Totals) bool {
	if t.Validate() != nil || value.Validate() != nil ||
		math.MaxUint64-t.TradeCount < value.TradeCount ||
		math.MaxUint64-t.UnresolvedCount < value.UnresolvedCount {
		return false
	}
	result := *t
	result.Add(value)
	if result.Validate() != nil {
		return false
	}
	*t = result
	return true
}

func (t *Totals) Subtract(value Totals) {
	t.NativeVolume = nonnegative(t.NativeVolume - value.NativeVolume)
	t.KRWVolume = nonnegative(t.KRWVolume - value.KRWVolume)
	t.TradeCount = subtractUint(t.TradeCount, value.TradeCount)
	t.UnresolvedCount = subtractUint(t.UnresolvedCount, value.UnresolvedCount)
	t.BuyKRWVolume = nonnegative(t.BuyKRWVolume - value.BuyKRWVolume)
	t.SellKRWVolume = nonnegative(t.SellKRWVolume - value.SellKRWVolume)
	t.BaseVolume = nonnegative(t.BaseVolume - value.BaseVolume)
}

func (t Totals) Empty() bool {
	return t.TradeCount == 0 && t.UnresolvedCount == 0 &&
		math.Abs(t.NativeVolume) <= volumeZeroTolerance &&
		math.Abs(t.KRWVolume) <= krwZeroTolerance &&
		math.Abs(t.BuyKRWVolume) <= krwZeroTolerance &&
		math.Abs(t.SellKRWVolume) <= krwZeroTolerance &&
		math.Abs(t.BaseVolume) <= volumeZeroTolerance
}

func nonnegative(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func subtractUint(left, right uint64) uint64 {
	if right > left {
		return 0
	}
	return left - right
}
