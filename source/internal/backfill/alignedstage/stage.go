package alignedstage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
)

type Manifest struct {
	Version                    int                 `json:"version"`
	EndpointMS                 int64               `json:"endpoint_ms"`
	CohortEndpointMS           int64               `json:"cohort_endpoint_ms"`
	CohortObservedMS           int64               `json:"cohort_observed_ms"`
	CohortSource               string              `json:"cohort_source"`
	ExecutableSHA256           string              `json:"executable_sha256"`
	HistoricalUniverseVerified bool                `json:"historical_universe_verified"`
	Scope                      string              `json:"scope"`
	Markets                    []domain.MarketPair `json:"markets"`
}

func (m Manifest) Validate() error {
	if m.Version != 1 || m.EndpointMS <= 24*HourMS || m.EndpointMS%HourMS != 0 || m.CohortEndpointMS%HourMS != 0 || m.CohortObservedMS <= 0 || m.CohortSource == "" || len(m.ExecutableSHA256) != 64 || m.HistoricalUniverseVerified || m.Scope != "PERSISTED_COHORT_RETROSPECTIVE_STAGE_ONLY" {
		return errors.New("invalid or overclaimed stage manifest")
	}
	if len(m.Markets) == 0 || len(m.Markets) > 5000 {
		return errors.New("stage cohort must contain 1..5000 markets")
	}
	previous := ""
	for _, market := range m.Markets {
		key := string(market.Exchange) + "/" + market.Symbol
		if key <= previous || market.Quote != "KRW" || market.Scope != domain.ScopeDomestic || market.Base == "" || market.Symbol == "" || (market.Exchange != domain.ExchangeBithumb && market.Exchange != domain.ExchangeUpbit && market.Exchange != domain.ExchangeCoinone) {
			return errors.New("invalid or duplicate frozen market")
		}
		previous = key
	}
	return nil
}

type Receipt struct {
	ManifestSHA         string `json:"manifest_sha256"`
	MarketIndex         int    `json:"market_index"`
	Attempt             int    `json:"attempt"`
	StartMS             int64  `json:"start_ms"`
	EndMS               int64  `json:"end_ms"`
	FetchedAtMS         int64  `json:"fetched_at_ms"`
	HTTPStatus          int    `json:"http_status"`
	RetryAtMS           int64  `json:"retry_at_ms"`
	RequestURL          string `json:"request_url"`
	Raw                 []byte `json:"raw_response"`
	RawSHA              string `json:"raw_sha256"`
	RawResponseComplete bool   `json:"raw_response_complete"`
	Error               string `json:"error,omitempty"`
}
type FetchResult struct {
	Raw        []byte
	HTTPStatus int
	RetryAt    time.Time
	RequestURL string
}
type Fetcher interface {
	Fetch(context.Context, domain.MarketPair, int64, int64) (FetchResult, error)
}

type Checkpoint struct {
	ManifestSHA     string   `json:"manifest_sha256"`
	MarketIndex     int      `json:"market_index"`
	PageEndMS       int64    `json:"page_end_ms"`
	Attempts        int      `json:"attempts"`
	Accepted        []string `json:"accepted_receipts"`
	CompletedPages  int      `json:"completed_pages"`
	CompleteMarkets int      `json:"complete_markets"`
	PartialMarkets  int      `json:"partial_markets"`
	MissingMinutes  int      `json:"missing_minutes"`
	CompletedHashes []string `json:"completed_market_sha256"`
	RetryAtMS       int64    `json:"retry_at_ms"`
	LastError       string   `json:"last_error,omitempty"`
}
type Report struct {
	Status                     string     `json:"status"`
	Requests                   int        `json:"requests"`
	ReplayedReceipts           int        `json:"replayed_receipts"`
	HistoricalUniverseVerified bool       `json:"historical_universe_verified"`
	PublishedEndpoints         int        `json:"published_endpoints"`
	Checkpoint                 Checkpoint `json:"checkpoint"`
}
type Stage struct {
	Dir      string
	Manifest Manifest
	Hash     string
	State    Checkpoint
	unlock   func()
}

func (s *Stage) Close() {
	if s.unlock != nil {
		s.unlock()
		s.unlock = nil
	}
}

func Open(dir string, initialize *Manifest) (*Stage, error) {
	return OpenContext(context.Background(), dir, initialize)
}

func OpenContext(ctx context.Context, dir string, initialize *Manifest) (*Stage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dir) {
		return nil, errors.New("absolute protected stage directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("stage directory must be private (0700)")
	}
	release, err := lock(dir)
	if err != nil {
		return nil, fmt.Errorf("stage already owned or unavailable: %w", err)
	}
	s := &Stage{Dir: dir, unlock: release}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	if initialize != nil {
		if err := initialize.Validate(); err != nil {
			return nil, err
		}
		if err := writeJSON(filepath.Join(dir, "manifest.json"), initialize, true); err != nil {
			return nil, err
		}
	}
	if err := readJSON(filepath.Join(dir, "manifest.json"), &s.Manifest); err != nil {
		return nil, err
	}
	if err := s.Manifest.Validate(); err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(s.Manifest)
	s.Hash = digest(encoded)
	err = readJSON(filepath.Join(dir, "checkpoint.json"), &s.State)
	if os.IsNotExist(err) {
		s.State = Checkpoint{ManifestSHA: s.Hash, PageEndMS: s.Manifest.EndpointMS, Accepted: []string{}}
		err = s.save()
	}
	if err != nil {
		return nil, err
	}
	if s.State.ManifestSHA != s.Hash || s.State.MarketIndex < 0 || s.State.MarketIndex > len(s.Manifest.Markets) || s.State.PageEndMS > s.Manifest.EndpointMS || s.State.PageEndMS < s.Manifest.EndpointMS-24*HourMS || s.State.PageEndMS%MinuteMS != 0 {
		return nil, errors.New("checkpoint identity or bounds mismatch")
	}
	if len(s.State.CompletedHashes) != s.State.MarketIndex || s.State.CompleteMarkets+s.State.PartialMarkets != s.State.MarketIndex {
		return nil, errors.New("completed market checkpoint mismatch")
	}
	for index, expected := range s.State.CompletedHashes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := readEvidence(filepath.Join(dir, "markets", fmt.Sprintf("%05d.json", index)))
		if err != nil || digest(data) != expected {
			return nil, errors.New("completed market evidence hash mismatch")
		}
		var summary MarketSummary
		if err := json.Unmarshal(data, &summary); err != nil || summary.ManifestSHA != s.Hash || len(summary.Receipts) != len(summary.ReceiptHashes) || len(summary.Receipts) != 8 {
			return nil, errors.New("completed receipt anchors invalid")
		}
		for i, name := range summary.Receipts {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if filepath.Base(name) != name || strings.Contains(name, "..") {
				return nil, errors.New("invalid completed receipt path")
			}
			receiptData, err := readEvidence(filepath.Join(dir, "receipts", name))
			if err != nil || digest(receiptData) != summary.ReceiptHashes[i] {
				return nil, errors.New("completed receipt missing or corrupted")
			}
		}
	}
	ok = true
	return s, nil
}

type MarketSummary struct {
	ManifestSHA                string            `json:"manifest_sha256"`
	Market                     domain.MarketPair `json:"market"`
	HistoricalUniverseVerified bool              `json:"historical_universe_verified"`
	Status                     string            `json:"status"`
	Receipts                   []string          `json:"receipts"`
	ReceiptHashes              []string          `json:"receipt_sha256"`
	Hours                      []Hour            `json:"hours"`
}

func (s *Stage) save() error {
	return writeJSON(filepath.Join(s.Dir, "checkpoint.json"), s.State, false)
}

func (s *Stage) readReceipt(name string) (Receipt, error) {
	var r Receipt
	if filepath.Base(name) != name || strings.Contains(name, "..") {
		return r, errors.New("invalid receipt path")
	}
	if err := readJSON(filepath.Join(s.Dir, "receipts", name), &r); err != nil {
		return r, err
	}
	if r.ManifestSHA != s.Hash || r.RawSHA != digest(r.Raw) || r.MarketIndex != s.State.MarketIndex {
		return r, errors.New("receipt evidence hash or market mismatch")
	}
	return r, nil
}
func (s *Stage) priorPages() (map[int64]official.MinuteCandle, []Page, error) {
	known := map[int64]official.MinuteCandle{}
	pages := []Page{}
	end := s.Manifest.EndpointMS
	for _, name := range s.State.Accepted {
		r, err := s.readReceipt(name)
		if err != nil {
			return nil, nil, err
		}
		if r.Error != "" || !r.RawResponseComplete || r.EndMS != end || r.StartMS != max(s.Manifest.EndpointMS-24*HourMS, end-199*MinuteMS) {
			return nil, nil, errors.New("accepted receipt chain is invalid")
		}
		p, err := DecodePage(s.Manifest.Markets[s.State.MarketIndex], r.Raw, r.StartMS, r.EndMS, s.Manifest.EndpointMS-24*HourMS, known)
		if err != nil {
			return nil, nil, err
		}
		for at, c := range p.Candles {
			known[at] = c
		}
		pages = append(pages, p)
		end = r.StartMS
	}
	if end != s.State.PageEndMS {
		return nil, nil, errors.New("checkpoint does not follow durable receipts")
	}
	return known, pages, nil
}

func (s *Stage) Run(ctx context.Context, fetcher Fetcher, maxRequests int, now func() time.Time) (Report, error) {
	report := Report{Status: "BATCH_LIMIT"}
	finish := func(err error) (Report, error) { report.Checkpoint = s.State; return report, err }
	if maxRequests < 1 || maxRequests > 200 {
		return finish(errors.New("request limit must be 1..200"))
	}
	if s.State.RetryAtMS > now().UnixMilli() {
		report.Status = "RETRY_WAIT"
		return finish(nil)
	}
	for s.State.MarketIndex < len(s.Manifest.Markets) {
		if err := ctx.Err(); err != nil {
			report.Status = "STOPPED"
			return finish(err)
		}
		known, pages, err := s.priorPages()
		if err != nil {
			report.Status = "CONFLICT"
			return finish(err)
		}
		if s.State.PageEndMS == s.Manifest.EndpointMS-24*HourMS {
			hours, err := Rollup(pages, s.Manifest.EndpointMS-24*HourMS, s.Manifest.EndpointMS)
			if err != nil {
				return finish(err)
			}
			missing := 0
			for _, h := range hours {
				missing += h.MissingMinutes
			}
			summary := MarketSummary{ManifestSHA: s.Hash, Market: s.Manifest.Markets[s.State.MarketIndex], Status: "COMPLETE", Receipts: append([]string{}, s.State.Accepted...), Hours: hours}
			for _, name := range summary.Receipts {
				if err := ctx.Err(); err != nil {
					return finish(err)
				}
				data, err := readEvidence(filepath.Join(s.Dir, "receipts", name))
				if err != nil {
					return finish(err)
				}
				summary.ReceiptHashes = append(summary.ReceiptHashes, digest(data))
			}
			if missing > 0 {
				summary.Status = "PARTIAL"
			}
			if err := writeJSON(filepath.Join(s.Dir, "markets", fmt.Sprintf("%05d.json", s.State.MarketIndex)), summary, true); err != nil {
				return finish(err)
			}
			encoded, err := os.ReadFile(filepath.Join(s.Dir, "markets", fmt.Sprintf("%05d.json", s.State.MarketIndex)))
			if err != nil {
				return finish(err)
			}
			s.State.CompletedHashes = append(s.State.CompletedHashes, digest(encoded))
			if missing > 0 {
				s.State.PartialMarkets++
			} else {
				s.State.CompleteMarkets++
			}
			s.State.MarketIndex++
			s.State.PageEndMS = s.Manifest.EndpointMS
			s.State.Accepted = []string{}
			s.State.LastError = ""
			s.State.RetryAtMS = 0
			if err := s.save(); err != nil {
				return finish(err)
			}
			continue
		}
		end := s.State.PageEndMS
		start := max(s.Manifest.EndpointMS-24*HourMS, end-199*MinuteMS)
		name := fmt.Sprintf("%05d-%013d-%08d.json", s.State.MarketIndex, end, s.State.Attempts+1)
		r, err := s.readReceipt(name)
		if os.IsNotExist(err) {
			if report.Requests >= maxRequests {
				return finish(nil)
			}
			result, fetchErr := fetcher.Fetch(ctx, s.Manifest.Markets[s.State.MarketIndex], start, end)
			report.Requests++
			r = Receipt{ManifestSHA: s.Hash, MarketIndex: s.State.MarketIndex, Attempt: s.State.Attempts + 1, StartMS: start, EndMS: end, FetchedAtMS: now().UnixMilli(), HTTPStatus: result.HTTPStatus, RequestURL: result.RequestURL, Raw: result.Raw, RawSHA: digest(result.Raw), RawResponseComplete: fetchErr == nil}
			if fetchErr != nil {
				r.Error = fetchErr.Error()
				r.RetryAtMS = max(now().Add(time.Minute).UnixMilli(), result.RetryAt.UnixMilli())
			}
			if err := writeJSON(filepath.Join(s.Dir, "receipts", name), r, true); err != nil {
				return finish(err)
			}
		} else if err != nil {
			report.Status = "CONFLICT"
			return finish(err)
		} else {
			report.ReplayedReceipts++
		}
		if r.StartMS != start || r.EndMS != end || r.Attempt != s.State.Attempts+1 {
			return finish(errors.New("pending receipt identity mismatch"))
		}
		if r.Error != "" {
			s.State.Attempts = r.Attempt
			s.State.LastError = r.Error
			s.State.RetryAtMS = r.RetryAtMS
			if err := s.save(); err != nil {
				return finish(err)
			}
			report.Status = "RETRY_WAIT"
			return finish(errors.New(r.Error))
		}
		p, err := DecodePage(s.Manifest.Markets[s.State.MarketIndex], r.Raw, start, end, s.Manifest.EndpointMS-24*HourMS, known)
		if err != nil {
			report.Status = "SOURCE_CONFLICT"
			return finish(err)
		}
		s.State.Attempts = r.Attempt
		s.State.CompletedPages++
		s.State.MissingMinutes += len(p.Missing)
		s.State.Accepted = append(s.State.Accepted, name)
		s.State.PageEndMS = start
		s.State.LastError = ""
		s.State.RetryAtMS = 0
		if err := s.save(); err != nil {
			return finish(err)
		}
	}
	report.Status = "STAGED_COMPLETE"
	if s.State.PartialMarkets > 0 {
		report.Status = "STAGED_PARTIAL"
	}
	return finish(nil)
}

func SortMarkets(markets []domain.MarketPair) {
	sort.Slice(markets, func(i, j int) bool {
		return string(markets[i].Exchange)+"/"+markets[i].Symbol < string(markets[j].Exchange)+"/"+markets[j].Symbol
	})
}
