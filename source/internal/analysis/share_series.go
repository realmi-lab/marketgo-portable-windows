package analysis

// ShareSeries is Bithumb's share of domestic trading in each of the last
// closed buckets of one size, oldest first, from the direct observations.
type ShareSeries struct {
	MarketScope string `json:"market_scope"`
	BucketSec   int64  `json:"bucket_sec"`
	// AsOfSecond is the last second of the newest bucket.
	AsOfSecond int64              `json:"as_of_second"`
	Points     []ShareSeriesPoint `json:"points"`
}

// ShareSeriesPoint is one bucket's trading and share, with the same status
// as a coin share (NO_TRADES, NOT_OBSERVED, UNRESOLVED_FX). A bucket that
// was not fully observed keeps the share of what was observed, with
// Complete false.
type ShareSeriesPoint struct {
	// BucketMS is the bucket's first second, in milliseconds.
	BucketMS    int64 `json:"bucket_ms"`
	Complete    bool  `json:"complete"`
	CoverageSec int64 `json:"coverage_sec"`
	CoinShareValue
}

// NewShareSeriesPoint is the point of the bucket starting at startSecond
// from its domestic rows.
func NewShareSeriesPoint(startSecond int64, rows []Row, complete bool, coverageSec int64) ShareSeriesPoint {
	canonical, valid := canonicalDomesticRows(rows)
	return ShareSeriesPoint{
		BucketMS: startSecond * 1_000, Complete: complete, CoverageSec: coverageSec,
		CoinShareValue: realtimeCoinShare(coinRows{rows: canonical, invalid: !valid}, complete),
	}
}
