package backfill

// This path stores minute-resolution evidence only. It never produces trades,
// volume buckets, observation seals, or exact WebSocket coverage.
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/internal/reconcile"
	"market-share-go/services/collector/official"
	"market-share-go/services/collector/restbudget"
)

const MinuteBackfillAttempt = 3

type MinuteSelection struct {
	From, To time.Time
	GapIDs   []int64
	After    string
	Limit    int
}

func (s MinuteSelection) Validate(now time.Time) error {
	if s.From.IsZero() || s.To.IsZero() || !s.From.Equal(s.From.Truncate(time.Minute)) || !s.To.Equal(s.To.Truncate(time.Minute)) {
		return errors.New("minute range requires exact RFC3339 minute boundaries")
	}
	if !s.To.After(s.From) || s.To.Sub(s.From) > 48*time.Hour {
		return errors.New("minute range must be positive and at most 48 hours")
	}
	if s.To.After(now.UTC().Add(-2 * time.Minute).Truncate(time.Minute)) {
		return errors.New("minute range must end at least two minutes before the current minute")
	}
	if s.Limit < 1 || s.Limit > 200 || len(s.GapIDs) > 200 {
		return errors.New("minute batch and gap ID limits must be between 1 and 200")
	}
	seen := map[int64]bool{}
	for _, id := range s.GapIDs {
		if id < 1 || seen[id] {
			return errors.New("gap IDs must be distinct and positive")
		}
		seen[id] = true
	}
	_, err := s.AfterKey()
	return err
}

type MinuteCandidate struct {
	Exchange    domain.Exchange   `json:"exchange"`
	Market      string            `json:"market"`
	Base        string            `json:"base"`
	MinuteStart domain.UnixSecond `json:"minute_start"`
	GapSeconds  int64             `json:"gap_seconds"`
	GapIDs      []int64           `json:"gap_ids"`
}

func (c MinuteCandidate) Key() string {
	return fmt.Sprintf("%020d/%s/%s", c.MinuteStart, c.Exchange, c.Market)
}
func ExactMinuteExchange(exchange domain.Exchange) bool {
	return exchange == domain.ExchangeUpbit || exchange == domain.ExchangeBithumb || exchange == domain.ExchangeCoinone
}

type minuteCursor struct {
	From, To int64
	Key      string
	Scope    string
}

func minuteGapScope(ids []int64) string {
	values := append([]int64(nil), ids...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return fmt.Sprint(values)
}

func (s MinuteSelection) Cursor(key string) string {
	if key == "" {
		return ""
	}
	raw, _ := json.Marshal(minuteCursor{s.From.Unix(), s.To.Unix(), key, minuteGapScope(s.GapIDs)})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (s MinuteSelection) AfterKey() (string, error) {
	if s.After == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s.After)
	var cursor minuteCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.From != s.From.Unix() || cursor.To != s.To.Unix() || cursor.Key == "" || cursor.Scope != minuteGapScope(s.GapIDs) {
		return "", errors.New("cursor does not match the fixed minute range")
	}
	return cursor.Key, nil
}

// GapSecondsForMinute unions overlapping gaps; duplicate or nested gap IDs do
// not multiply either a candidate or its known missing seconds.
func GapSecondsForMinute(gaps []ObservationGap, exchange domain.Exchange, market string, minute domain.UnixSecond) (int64, []int64) {
	var seconds [60]bool
	ids := map[int64]bool{}
	for _, gap := range gaps {
		affected := len(gap.AffectedExchanges) == 0
		for _, ex := range gap.AffectedExchanges {
			affected = affected || ex == exchange
		}
		if !affected {
			continue
		}
		affected = len(gap.AffectedMarkets) == 0
		for _, symbol := range gap.AffectedMarkets {
			affected = affected || strings.EqualFold(strings.TrimSpace(symbol), market)
		}
		if !affected {
			continue
		}
		start, end := max(gap.Start, minute), min(gap.End, minute+59)
		if end < start {
			continue
		}
		ids[gap.ID] = true
		for second := start; second <= end; second++ {
			seconds[second-minute] = true
		}
	}
	var count int64
	for _, present := range seconds {
		if present {
			count++
		}
	}
	result := make([]int64, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return count, result
}

func PreserveObservedMinute(ws *reconcile.WSTradeMinute, seal *reconcile.MinuteSeal) bool {
	if seal != nil && seal.Status == reconcile.SealComplete && seal.GapSeconds == 0 {
		return true
	}
	return ws != nil && ws.GapSeconds == 0 && ws.CoverageStatus == string(reconcile.SealComplete)
}

type MinuteStore interface {
	ApplyMinuteEvidence(context.Context, MinuteCandidate, reconcile.OfficialMinuteCandle) (string, error)
}
type MinuteClient interface {
	GetBytes(context.Context, restbudget.Request, int64) ([]byte, error)
}
type MinuteBatchReport struct {
	Status               string            `json:"status"`
	DryRun               bool              `json:"dry_run"`
	Candidates           int               `json:"candidates"`
	Requests             int               `json:"requests"`
	CacheHits            int               `json:"cache_hits"`
	ValidatedCacheHits   int               `json:"validated_cache_hits"`
	DocumentedNoTrade    int               `json:"documented_no_trade_omissions"`
	Validated            int               `json:"validated"`
	Applied              int               `json:"applied"`
	Preserved            int               `json:"preserved"`
	Missing              int               `json:"missing"`
	PrecisionDifferences int               `json:"precision_differences"`
	NextCursor           string            `json:"next_cursor"`
	Results              []MinuteRowReport `json:"results"`
}
type MinuteRowReport struct {
	MinuteCandidate
	Status string `json:"status"`
}

func RunMinuteBatch(ctx context.Context, selection MinuteSelection, candidates []MinuteCandidate, client MinuteClient, store MinuteStore, apply bool, requestLimit int, now func() time.Time) (MinuteBatchReport, error) {
	var provider MinuteProvider
	if client != nil {
		provider = directMinuteProvider{client: client}
	}
	return RunMinuteBatchWithProvider(ctx, selection, candidates, provider, store, apply, requestLimit, now)
}

func RunMinuteBatchWithProvider(ctx context.Context, selection MinuteSelection, candidates []MinuteCandidate, provider MinuteProvider, store MinuteStore, apply bool, requestLimit int, now func() time.Time) (MinuteBatchReport, error) {
	report := MinuteBatchReport{Status: "COMPLETE", DryRun: !apply, Candidates: len(candidates), NextCursor: selection.After, Results: []MinuteRowReport{}}
	if now == nil {
		now = time.Now
	}
	if err := selection.Validate(now()); err != nil {
		return report, err
	}
	if requestLimit < 1 || requestLimit > 200 || provider == nil || (apply && store == nil) {
		return report, errors.New("invalid minute runner dependencies or request limit")
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if !ExactMinuteExchange(candidate.Exchange) || candidate.MinuteStart%60 != 0 || int64(candidate.MinuteStart) < selection.From.Unix() || int64(candidate.MinuteStart)+60 > selection.To.Unix() || candidate.GapSeconds < 1 || candidate.GapSeconds > 60 || candidate.Base == "" || len(candidate.GapIDs) == 0 {
			return report, errors.New("invalid or unsupported minute candidate")
		}
		if seen[candidate.Key()] {
			return report, errors.New("duplicate market-minute candidate")
		}
		seen[candidate.Key()] = true
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			report.Status = "STOPPED"
			return report, err
		}
		fetched, err := provider.FetchMinute(ctx, candidate, requestLimit-report.Requests)
		report.Requests += fetched.Requests
		if errors.Is(err, ErrMinuteRequestLimit) {
			report.Status = "BATCH_LIMIT"
			break
		}
		if err != nil {
			report.Status = "STOPPED"
			return report, fmt.Errorf("fetch %s: %w", candidate.Key(), err)
		}
		candle := fetched.Candle
		if fetched.CacheHit {
			report.CacheHits++
		}
		status := "VALIDATED"
		if candle == nil {
			status = "OFFICIAL_MINUTE_UNAVAILABLE"
			if fetched.NoTradeProven {
				status = "DOCUMENTED_NO_TRADE_OMISSION"
				report.DocumentedNoTrade++
			}
			report.Missing++
		} else {
			if candle.QuoteVolumeKRW == nil || candle.NotionalMethod != official.ExactQuoteNotional {
				return report, errors.New("official minute has no exact quote volume")
			}
			fetchedAt := fetched.FetchedAt
			if fetchedAt.IsZero() {
				fetchedAt = now().UTC()
			}
			row, err := reconcile.CandleFromOfficial(*candle, candidate.Base, "KRW", MinuteBackfillAttempt, fetchedAt)
			if err != nil {
				return report, err
			}
			report.Validated++
			if fetched.CacheHit {
				report.ValidatedCacheHits++
			}
			if apply {
				status, err = store.ApplyMinuteEvidence(ctx, candidate, row)
				if err != nil {
					report.Status = "STOPPED"
					return report, fmt.Errorf("apply %s: %w", candidate.Key(), err)
				}
				if status == "APPLIED" || status == "APPLIED_SOURCE_PRECISION_DIFFERENCE" || status == "APPLIED_REPLACEMENT" {
					report.Applied++
				} else {
					report.Preserved++
				}
				if status == "APPLIED_SOURCE_PRECISION_DIFFERENCE" || status == "ALREADY_APPLIED_SOURCE_PRECISION_DIFFERENCE" {
					report.PrecisionDifferences++
				}
			}
		}
		report.Results = append(report.Results, MinuteRowReport{candidate, status})
		report.NextCursor = selection.Cursor(candidate.Key())
	}
	if (report.Missing > 0 || report.PrecisionDifferences > 0) && report.Status == "COMPLETE" {
		report.Status = "PARTIAL"
	}
	return report, nil
}
