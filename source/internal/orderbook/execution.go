package orderbook

import (
	"math"

	"market-share-go/internal/domain"
)

type Execution struct {
	Side               string   `json:"side"`
	RequestedKRW       float64  `json:"requested_krw"`
	FilledKRW          float64  `json:"filled_krw"`
	UnfilledKRW        float64  `json:"unfilled_krw"`
	FillPercent        float64  `json:"fill_pct"`
	Complete           bool     `json:"complete"`
	AveragePrice       *float64 `json:"average_price"`
	AveragePriceKRW    *float64 `json:"average_price_krw"`
	WorstPrice         *float64 `json:"worst_price"`
	WorstPriceKRW      *float64 `json:"worst_price_krw"`
	SlippageBPS        *float64 `json:"slippage_bps"`
	LevelsUsed         int      `json:"levels_used"`
	FilledBaseQuantity float64  `json:"filled_base_quantity"`
}

func SimulateExecution(
	levels []domain.OrderBookLevel,
	side string,
	notionalKRW float64,
	quoteKRWRate float64,
) Execution {
	result := emptyExecution(side, notionalKRW)
	if len(levels) == 0 || result.RequestedKRW == 0 || !positive(quoteKRWRate) {
		return result
	}

	if side == "buy" {
		fillBuy(levels, result.RequestedKRW/quoteKRWRate, &result)
	} else {
		fillSell(levels, result.RequestedKRW/quoteKRWRate, &result)
	}
	finishExecution(levels[0].Price, quoteKRWRate, &result)
	return result
}

func emptyExecution(side string, notionalKRW float64) Execution {
	if !positive(notionalKRW) {
		notionalKRW = 0
	}
	return Execution{
		Side:         side,
		RequestedKRW: notionalKRW,
		UnfilledKRW:  notionalKRW,
	}
}

func fillBuy(levels []domain.OrderBookLevel, targetQuote float64, result *Execution) {
	remaining := targetQuote
	for _, level := range levels {
		takenQuote := math.Min(remaining, level.Price*level.Quantity)
		if takenQuote <= 0 {
			continue
		}
		result.FilledBaseQuantity += takenQuote / level.Price
		result.FilledKRW += takenQuote
		result.LevelsUsed++
		result.WorstPrice = pointer(level.Price)
		remaining -= takenQuote
		if remaining <= 1e-12 {
			break
		}
	}
}

func fillSell(levels []domain.OrderBookLevel, targetQuote float64, result *Execution) {
	benchmark := levels[0].Price
	remainingBase := targetQuote / benchmark
	for _, level := range levels {
		takenBase := math.Min(remainingBase, level.Quantity)
		if takenBase <= 0 {
			continue
		}
		result.FilledBaseQuantity += takenBase
		result.FilledKRW += takenBase * level.Price
		result.LevelsUsed++
		result.WorstPrice = pointer(level.Price)
		remainingBase -= takenBase
		if remainingBase <= 1e-12 {
			break
		}
	}
}

func finishExecution(benchmark, quoteKRWRate float64, result *Execution) {
	nativeFilled := result.FilledKRW
	if result.FilledBaseQuantity > 0 {
		result.AveragePrice = pointer(nativeFilled / result.FilledBaseQuantity)
	}

	if result.Side == "sell" {
		result.FilledKRW = result.FilledBaseQuantity * benchmark * quoteKRWRate
	} else {
		result.FilledKRW *= quoteKRWRate
	}
	if result.FilledKRW > result.RequestedKRW {
		result.FilledKRW = result.RequestedKRW
	}
	result.UnfilledKRW = math.Max(0, result.RequestedKRW-result.FilledKRW)
	if result.RequestedKRW > 0 {
		result.FillPercent = math.Min(100, result.FilledKRW/result.RequestedKRW*100)
	}
	result.Complete = result.FillPercent >= 99.999999

	if result.AveragePrice != nil {
		result.AveragePriceKRW = pointer(*result.AveragePrice * quoteKRWRate)
		slippage := (*result.AveragePrice/benchmark - 1) * 10_000
		if result.Side != "buy" {
			slippage = (1 - *result.AveragePrice/benchmark) * 10_000
		}
		result.SlippageBPS = pointer(math.Max(0, slippage))
	}
	if result.WorstPrice != nil {
		result.WorstPriceKRW = pointer(*result.WorstPrice * quoteKRWRate)
	}
}

func pointer(value float64) *float64 {
	return &value
}

func positive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}
