package backfill

import (
	"context"
	"fmt"
	"market-share-go/internal/backfill/alignedstage"
	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
	"market-share-go/services/collector/restbudget"
	"net/url"
	"time"
)

var ErrMinuteRequestLimit = alignedstage.ErrRequestLimit

type MinuteFetch struct {
	Candle        *official.MinuteCandle
	Requests      int
	CacheHit      bool
	NoTradeProven bool
	FetchedAt     time.Time
}
type MinuteProvider interface {
	FetchMinute(context.Context, MinuteCandidate, int) (MinuteFetch, error)
}
type directMinuteProvider struct{ client MinuteClient }

func (p directMinuteProvider) FetchMinute(ctx context.Context, candidate MinuteCandidate, remaining int) (MinuteFetch, error) {
	result := MinuteFetch{}
	if remaining < 1 {
		return result, ErrMinuteRequestLimit
	}
	request, err := official.BuildCandleRequest(candidate.Exchange, candidate.Market, (int64(candidate.MinuteStart)+60)*1000)
	if err != nil {
		return result, err
	}
	query := url.Values{}
	for key, value := range request.Params {
		query.Set(key, fmt.Sprint(value))
	}
	group := "public"
	if candidate.Exchange == domain.ExchangeUpbit {
		group = "candle"
	}
	raw, err := p.client.GetBytes(ctx, restbudget.Request{Exchange: string(candidate.Exchange), Group: group, URL: request.URL, Query: query, Priority: restbudget.PriorityRoundRobin, Cost: 1}, 2<<20)
	result.Requests = 1
	if err != nil {
		return result, err
	}
	result.Candle, err = official.ParseClosedCandle(candidate.Exchange, candidate.Market, raw, (int64(candidate.MinuteStart)+60)*1000)
	return result, err
}

type CachedMinuteProvider struct {
	Cache     *alignedstage.PageCache
	Fetcher   alignedstage.Fetcher
	Selection MinuteSelection
	Now       func() time.Time
}

func (p CachedMinuteProvider) FetchMinute(ctx context.Context, candidate MinuteCandidate, remaining int) (MinuteFetch, error) {
	now := p.Now
	if now == nil {
		now = time.Now
	}
	m := domain.MarketPair{Exchange: candidate.Exchange, Symbol: candidate.Market, Base: candidate.Base, Quote: "KRW", Scope: domain.ScopeDomestic}
	result, err := p.Cache.Get(ctx, m, int64(candidate.MinuteStart)*1000, p.Selection.From.UnixMilli(), p.Selection.To.UnixMilli(), remaining, p.Fetcher, now)
	return MinuteFetch{Candle: result.Candle, Requests: result.Requests, CacheHit: result.Hit, NoTradeProven: result.NoTradeProven, FetchedAt: result.FetchedAt}, err
}
