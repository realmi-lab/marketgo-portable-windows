package reconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/services/collector/restbudget"
)

const (
	CloseGraceMS            = int64(5_000)
	ClockSampleLimit        = 2_048
	ClockSampleMaxAbsSkewMS = int64(10 * 60_000)
	DefaultClockSamples     = 3
	MaxJobAgeMS             = MinuteMS + int64(120_000) + MinuteMS
)

var RetryOffsetsMS = [...]int64{45_000, 120_000}

var RetryMarketCaps = map[domain.Exchange]int{
	domain.ExchangeUpbit: 5, domain.ExchangeCoinone: 50, domain.ExchangeGopax: 100,
	domain.ExchangeKorbit: 250, domain.ExchangeBithumb: 500,
}

// QuietMarketSampleCaps bounds the official-candle work created for markets
// that had neither WebSocket turnover nor a known observation gap in the
// target minute. Active and gap-affected markets are still reconciled first;
// the quiet remainder is sampled deterministically so every market is checked
// over time without enqueueing the full domestic universe every minute.
var QuietMarketSampleCaps = map[domain.Exchange]int{
	domain.ExchangeUpbit: 5, domain.ExchangeBithumb: 10, domain.ExchangeCoinone: 5,
	domain.ExchangeKorbit: 5, domain.ExchangeGopax: 5,
}

type CandleJob struct {
	Exchange            domain.Exchange
	Market              domain.MarketPair
	TargetStartMS       int64
	DueServerMS         int64
	Attempt             int
	ExpectedMarketCount int64
	Priority            int
	KnownGapSeconds     int64
	ObservedVolumeKRW   float64
	Sequence            int64
}

func (j CandleJob) requestPriority() int {
	if j.KnownGapSeconds > 0 {
		return restbudget.PriorityGapRepair
	}
	if j.Attempt > 0 {
		return min(j.Priority, restbudget.PriorityRetry)
	}
	return j.Priority
}

func (j CandleJob) key() string {
	return string(j.Exchange) + "\x00" + j.Market.Symbol + "\x00" + integer(j.TargetStartMS) + "\x00" + integer(int64(j.Attempt))
}

type ScheduleResult struct {
	Jobs                   []CandleJob
	Seals                  []MinuteSeal
	RecoveryCandidates     []RawReplayCandidate
	EligibleMarkets        int
	SampledQuietMarkets    int
	SuppressedQuietMarkets int
	SkippedMarkets         int
	SkippedExchanges       int
	SkippedAll             bool
}

type volumeIdentity struct {
	exchange domain.Exchange
	market   string
	minute   domain.UnixSecond
}

type candleJobProgress struct {
	attempt   int
	gapRepair bool
}

type rankedMarket struct {
	priority int
	volume   float64
	market   domain.MarketPair
	gap      int64
}

type retryBatchKey struct {
	exchange      domain.Exchange
	targetStartMS int64
}

type registryIdentity struct {
	symbol string
	base   string
	quote  string
}

type registryFingerprintCache struct {
	identities  []registryIdentity
	fingerprint string
}

type observationBoundarySnapshot struct {
	starts  [5]int64
	present [5]bool
}

type Scheduler struct {
	mu                   sync.Mutex
	registryMu           sync.Mutex
	accumulator          *Accumulator
	minimumClockSamples  int
	clockSamples         []int64
	clockCursor          int
	clockMedian          float64
	clockMedianAt        time.Time
	clockMedianSet       bool
	firstObserved        map[domain.Exchange]int64
	firstReady           map[domain.Exchange]int64
	useExplicitReadiness bool
	queues               map[domain.Exchange][]CandleJob
	jobKeys              map[string]struct{}
	jobHistory           map[volumeIdentity]candleJobProgress
	retryMarkets         map[retryBatchKey]map[string]struct{}
	registryFingerprints map[domain.Exchange]registryFingerprintCache
	sequence             int64
}

func NewScheduler(minimumClockSamples int, explicitReadiness bool) *Scheduler {
	if minimumClockSamples < 1 {
		minimumClockSamples = DefaultClockSamples
	}
	queues := make(map[domain.Exchange][]CandleJob, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		queues[exchange] = nil
	}
	return &Scheduler{
		accumulator: NewAccumulator(), minimumClockSamples: minimumClockSamples,
		firstObserved: make(map[domain.Exchange]int64), firstReady: make(map[domain.Exchange]int64),
		useExplicitReadiness: explicitReadiness, queues: queues, jobKeys: make(map[string]struct{}),
		jobHistory:           make(map[volumeIdentity]candleJobProgress),
		retryMarkets:         make(map[retryBatchKey]map[string]struct{}),
		registryFingerprints: make(map[domain.Exchange]registryFingerprintCache, len(domain.DomesticExchanges)),
	}
}

func (s *Scheduler) ObserveAccepted(trade aggregate.AcceptedTrade) (bool, error) {
	exchange := domain.Exchange(trade.Key.Exchange)
	if trade.Key.Scope != string(domain.ScopeDomestic) || strings.ToUpper(trade.Key.Quote) != "KRW" || !isDomestic(exchange) {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.accumulator.IngestAccepted(trade); err != nil {
		return false, err
	}
	minute := int64(trade.EventSecond) / MinuteSeconds * MinuteMS
	if _, exists := s.firstObserved[exchange]; !exists {
		s.firstObserved[exchange] = minute
	}
	skew := int64(trade.ReceivedTS - trade.EventTS)
	if trade.EventTS > 0 && trade.ReceivedTS > 0 && abs64(skew) <= ClockSampleMaxAbsSkewMS {
		if len(s.clockSamples) < ClockSampleLimit {
			s.clockSamples = append(s.clockSamples, skew)
		} else {
			s.clockSamples[s.clockCursor] = skew
			s.clockCursor = (s.clockCursor + 1) % ClockSampleLimit
		}
	}
	return true, nil
}

func (s *Scheduler) ClockReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clockSamples) >= s.minimumClockSamples
}

func (s *Scheduler) EstimatedClockSkewMS() *float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, ok := s.estimatedClockSkewLocked(time.Now())
	if !ok {
		return nil
	}
	return &result
}

func (s *Scheduler) estimatedClockSkewLocked(now time.Time) (float64, bool) {
	if len(s.clockSamples) == 0 {
		return 0, false
	}
	if s.clockMedianSet && now.Sub(s.clockMedianAt) >= 0 && now.Sub(s.clockMedianAt) < time.Second {
		return s.clockMedian, true
	}
	s.clockMedian = medianLocked(s.clockSamples)
	s.clockMedianAt = now
	s.clockMedianSet = true
	return s.clockMedian, true
}

func (s *Scheduler) EstimatedServerNowMS(localNowMS int64) int64 {
	skew := s.EstimatedClockSkewMS()
	if skew == nil {
		return localNowMS
	}
	return localNowMS - int64(math.Round(*skew))
}

func (s *Scheduler) MarkReady(exchanges []domain.Exchange, serverNowMS int64) {
	boundary := (serverNowMS + MinuteMS - 1) / MinuteMS * MinuteMS
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, exchange := range exchanges {
		if isDomestic(exchange) {
			if _, exists := s.firstReady[exchange]; !exists {
				s.firstReady[exchange] = boundary
			}
		}
	}
}

func (s *Scheduler) ObservationBoundary(exchange domain.Exchange) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.useExplicitReadiness {
		value, ok := s.firstReady[exchange]
		return value, ok
	}
	value, ok := s.firstObserved[exchange]
	return value, ok
}

func (s *Scheduler) observationBoundaries() observationBoundarySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := s.firstObserved
	if s.useExplicitReadiness {
		values = s.firstReady
	}
	result := observationBoundarySnapshot{}
	for _, exchange := range domain.DomesticExchanges {
		index, ok := domesticExchangeIndex(exchange)
		if ok {
			result.starts[index], result.present[index] = values[exchange]
		}
	}
	return result
}

func (s *Scheduler) DrainClosed(serverNowMS int64, gaps []coverage.Gap) ([]WSTradeMinute, error) {
	before := domain.UnixSecond(max64(0, (serverNowMS-CloseGraceMS)/1000))
	s.mu.Lock()
	rows, err := s.accumulator.DrainClosed(before)
	skew, _ := s.estimatedClockSkewLocked(time.Now())
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	for index := range rows {
		gap := KnownGapSeconds(rows[index].Exchange, rows[index].Market, rows[index].MinuteStart, gaps, skew)
		if gap > 0 {
			rows[index].GapSeconds = gap
			rows[index].CoverageStatus = "PARTIAL"
		}
	}
	return rows, nil
}

func (s *Scheduler) ScheduleTarget(targetStartMS int64, markets []domain.MarketPair, gaps []coverage.Gap, closedVolume map[volumeIdentity]float64) ScheduleResult {
	minute := domain.UnixSecond(targetStartMS / 1000)
	seenExchanges := make(map[domain.Exchange]struct{})
	rows := make([]rankedMarket, 0, len(markets))
	result := ScheduleResult{}
	skew := s.EstimatedClockSkewMS()
	skewValue := 0.0
	if skew != nil {
		skewValue = *skew
	}
	boundaries := s.observationBoundaries()
	for _, market := range markets {
		exchangeIndex, domestic := domesticExchangeIndex(market.Exchange)
		if market.Scope != domain.ScopeDomestic || strings.ToUpper(market.Quote) != "KRW" || !domestic {
			continue
		}
		boundary, observed := boundaries.starts[exchangeIndex], boundaries.present[exchangeIndex]
		if !observed || targetStartMS < boundary {
			result.SkippedMarkets++
			seenExchanges[market.Exchange] = struct{}{}
			continue
		}
		gap := KnownGapSeconds(market.Exchange, market.Symbol, minute, gaps, skewValue)
		volumeIdentity := volumeKey(market.Exchange, market.Symbol, minute)
		volume, observed := closedVolume[volumeIdentity]
		priority := restbudget.PriorityRoundRobin
		if gap > 0 {
			priority = restbudget.PriorityGapRepair
		} else if observed {
			priority = restbudget.PriorityActiveMarket
		}
		rows = append(rows, rankedMarket{priority, volume, market, gap})
	}
	result.SkippedExchanges = len(seenExchanges)
	result.EligibleMarkets = len(rows)
	result.SkippedAll = len(rows) == 0
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].priority != rows[j].priority {
			return rows[i].priority < rows[j].priority
		}
		if rows[i].volume != rows[j].volume {
			return rows[i].volume > rows[j].volume
		}
		return rows[i].market.Key() < rows[j].market.Key()
	})
	selected := selectScheduledRows(targetStartMS, rows)
	result.SampledQuietMarkets = selected.sampledQuiet
	result.SuppressedQuietMarkets = len(rows) - len(selected.rows)
	result.SkippedMarkets += result.SuppressedQuietMarkets
	result.SkippedAll = len(selected.rows) == 0
	scheduledCounts := make(map[domain.Exchange]int64, len(domain.DomesticExchanges))
	for _, row := range selected.rows {
		scheduledCounts[row.market.Exchange]++
	}
	result.Seals = s.buildMinuteSeals(targetStartMS, rows, scheduledCounts, closedVolume)
	result.RecoveryCandidates = buildRawReplayCandidates(targetStartMS, rows, closedVolume, gaps, skewValue)
	// Publish one exchange/minute batch atomically. Workers poll independently
	// of the service signal and must never observe only the first few markets;
	// otherwise they can finalize the same exchange minute once per partial
	// queue instead of once after the complete scheduled batch.
	s.mu.Lock()
	for _, row := range selected.rows {
		job := CandleJob{Exchange: row.market.Exchange, Market: row.market, TargetStartMS: targetStartMS, DueServerMS: targetStartMS + MinuteMS + CloseGraceMS, ExpectedMarketCount: scheduledCounts[row.market.Exchange], Priority: row.priority, KnownGapSeconds: row.gap, ObservedVolumeKRW: row.volume}
		if s.enqueueLocked(job) {
			result.Jobs = append(result.Jobs, job)
		}
	}
	s.mu.Unlock()
	return result
}

// PreviewTarget recomputes seal and anomaly evidence without enqueueing
// official-candle jobs. MinuteService uses it for two bounded post-close audits
// so a stale connection detected after T+5 can conservatively revise a recent
// seal without rescanning unbounded history.
func (s *Scheduler) PreviewTarget(targetStartMS int64, markets []domain.MarketPair, gaps []coverage.Gap, closedVolume map[volumeIdentity]float64) ([]MinuteSeal, []RawReplayCandidate) {
	minute := domain.UnixSecond(targetStartMS / 1000)
	skew := s.EstimatedClockSkewMS()
	skewValue := 0.0
	if skew != nil {
		skewValue = *skew
	}
	boundaries := s.observationBoundaries()
	rows := make([]rankedMarket, 0, len(markets))
	activeCounts := make(map[domain.Exchange]int64, len(domain.DomesticExchanges))
	quietCounts := make(map[domain.Exchange]int64, len(domain.DomesticExchanges))
	for _, market := range markets {
		exchangeIndex, domestic := domesticExchangeIndex(market.Exchange)
		if market.Scope != domain.ScopeDomestic || strings.ToUpper(market.Quote) != "KRW" || !domestic {
			continue
		}
		boundary, observed := boundaries.starts[exchangeIndex], boundaries.present[exchangeIndex]
		if !observed || targetStartMS < boundary {
			continue
		}
		gap := KnownGapSeconds(market.Exchange, market.Symbol, minute, gaps, skewValue)
		volume, observed := closedVolume[volumeKey(market.Exchange, market.Symbol, minute)]
		priority := restbudget.PriorityRoundRobin
		if gap > 0 {
			priority = restbudget.PriorityGapRepair
		} else if observed {
			priority = restbudget.PriorityActiveMarket
		}
		rows = append(rows, rankedMarket{priority: priority, volume: volume, market: market, gap: gap})
		if priority == restbudget.PriorityRoundRobin {
			quietCounts[market.Exchange]++
		} else {
			activeCounts[market.Exchange]++
		}
	}
	scheduledCounts := make(map[domain.Exchange]int64, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		quiet := quietCounts[exchange]
		cap := int64(QuietMarketSampleCaps[exchange])
		if quiet < cap {
			cap = quiet
		}
		scheduledCounts[exchange] = activeCounts[exchange] + cap
	}
	return s.buildMinuteSeals(targetStartMS, rows, scheduledCounts, closedVolume),
		buildRawReplayCandidates(targetStartMS, rows, closedVolume, gaps, skewValue)
}

func buildRawReplayCandidates(targetStartMS int64, eligible []rankedMarket, closedVolume map[volumeIdentity]float64, gaps []coverage.Gap, clockSkewMS float64) []RawReplayCandidate {
	minute := domain.UnixSecond(targetStartMS / 1000)
	type replayEvidence struct {
		market domain.MarketPair
		gap    int64
	}
	evidence := make(map[domain.Exchange]map[string]replayEvidence, len(domain.DomesticExchanges))
	for _, row := range eligible {
		if row.gap <= 0 {
			continue
		}
		if _, observed := closedVolume[volumeKey(row.market.Exchange, row.market.Symbol, minute)]; observed {
			// Replaying into an immutable partial ws_trade_minutes row would not
			// prove a complete replacement. Only markets with no minute row are
			// safe automatic candidates; partial rows stay explicit for manual
			// exact backfill/reconciliation.
			continue
		}
		rows := evidence[row.market.Exchange]
		if rows == nil {
			rows = make(map[string]replayEvidence)
			evidence[row.market.Exchange] = rows
		}
		rows[row.market.Key()] = replayEvidence{market: row.market, gap: row.gap}
	}
	skewMS := int64(math.Round(clockSkewMS))
	startMS := max64(1, targetStartMS+skewMS-RawReplayClockJitterMS)
	endMS := targetStartMS + MinuteMS + skewMS + RawReplayClockJitterMS
	result := make([]RawReplayCandidate, 0, len(evidence))
	for _, exchange := range domain.DomesticExchanges {
		rows := evidence[exchange]
		if len(rows) == 0 {
			continue
		}
		keys := make([]string, 0, len(rows))
		for key := range rows {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		pairs := make([]domain.MarketPair, 0, len(keys))
		hash := sha256.New()
		for _, key := range keys {
			row := rows[key]
			pairs = append(pairs, row.market)
			_, _ = hash.Write([]byte(key))
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write([]byte(integer(row.gap)))
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write([]byte(rawReplayGapEvidenceFingerprint(exchange, row.market.Symbol, minute, gaps, clockSkewMS)))
			_, _ = hash.Write([]byte{'\n'})
		}
		candidate := RawReplayCandidate{
			Exchange: exchange, MinuteStart: minute, Markets: pairs,
			ReceivedStartNS:     startMS * int64(time.Millisecond),
			ReceivedEndNS:       endMS * int64(time.Millisecond),
			EvidenceFingerprint: hex.EncodeToString(hash.Sum(nil)),
		}
		if candidate.Normalize() == nil {
			result = append(result, candidate)
		}
	}
	return result
}

func rawReplayGapEvidenceFingerprint(exchange domain.Exchange, market string, minute domain.UnixSecond, gaps []coverage.Gap, clockSkewMS float64) string {
	minuteStart, minuteEnd := int64(minute), int64(minute)+MinuteSeconds-1
	skewSeconds := int64(math.Round(clockSkewMS / 1000))
	records := make([]string, 0)
	for _, gap := range gaps {
		gapStart := max64(minuteStart, gap.Start-skewSeconds)
		gapEnd := minuteEnd
		if gap.End != nil {
			gapEnd = min64(minuteEnd, *gap.End-skewSeconds)
		}
		if gapStart > gapEnd || !gapAffectsExchange(gap, exchange) || !gapAffectsMarket(gap, market) {
			continue
		}
		records = append(records, canonicalRawReplayGapEvidence(gap))
	}
	sort.Strings(records)
	hash := sha256.New()
	for _, record := range records {
		_, _ = hash.Write([]byte(record))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalRawReplayGapEvidence(gap coverage.Gap) string {
	var builder strings.Builder
	if gap.PersistenceToken != 0 {
		appendRawReplayEvidenceField(&builder, "token")
		appendRawReplayEvidenceField(&builder, strconv.FormatUint(gap.PersistenceToken, 10))
	} else {
		appendRawReplayEvidenceField(&builder, "id")
		appendRawReplayEvidenceField(&builder, integer(gap.PersistenceID))
	}
	appendRawReplayEvidenceField(&builder, integer(gap.Start))
	if gap.End == nil {
		appendRawReplayEvidenceField(&builder, "open")
	} else {
		appendRawReplayEvidenceField(&builder, integer(*gap.End))
	}
	appendRawReplayEvidenceField(&builder, strings.ToUpper(strings.TrimSpace(gap.Status)))
	appendRawReplayEvidenceStrings(&builder, gap.Context.AffectedExchanges, func(value string) string {
		return strings.ToLower(strings.TrimSpace(value))
	})
	appendRawReplayEvidenceStrings(&builder, gap.Context.AffectedMarkets, normalMarket)
	appendRawReplayEvidenceStrings(&builder, gap.Context.ReasonCodes, func(value string) string {
		return strings.ToUpper(strings.TrimSpace(value))
	})
	appendRawReplayEvidenceFloat(&builder, gap.Context.ObservedVolumeKRW)
	appendRawReplayEvidenceFloat(&builder, gap.Context.ObservedTotalVolumeKRW)
	appendRawReplayEvidenceFloat(&builder, gap.Context.ObservedVolumeAtRisk)
	appendRawReplayEvidenceField(&builder, integer(gap.Context.WeightWindowSec))
	return builder.String()
}

func appendRawReplayEvidenceStrings(builder *strings.Builder, values []string, normalize func(string) string) {
	canonical := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalize(value)
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		canonical = append(canonical, value)
	}
	sort.Strings(canonical)
	for _, value := range canonical {
		appendRawReplayEvidenceField(builder, value)
	}
	appendRawReplayEvidenceField(builder, "#")
}

func appendRawReplayEvidenceFloat(builder *strings.Builder, value *float64) {
	if value == nil {
		appendRawReplayEvidenceField(builder, "nil")
		return
	}
	appendRawReplayEvidenceField(builder, strconv.FormatFloat(*value, 'g', -1, 64))
}

func appendRawReplayEvidenceField(builder *strings.Builder, value string) {
	builder.WriteString(integer(int64(len(value))))
	builder.WriteByte(':')
	builder.WriteString(value)
}

func (s *Scheduler) buildMinuteSeals(targetStartMS int64, eligible []rankedMarket, scheduledCounts map[domain.Exchange]int64, closedVolume map[volumeIdentity]float64) []MinuteSeal {
	minute := domain.UnixSecond(targetStartMS / 1000)
	type sealBuilder struct {
		markets    int64
		observed   int64
		unknown    int64
		gapMarkets int64
		maxGap     int64
	}
	builders := make(map[domain.Exchange]*sealBuilder, len(domain.DomesticExchanges))
	for _, row := range eligible {
		builder := builders[row.market.Exchange]
		if builder == nil {
			builder = &sealBuilder{}
			builders[row.market.Exchange] = builder
		}
		builder.markets++
		_, observed := closedVolume[volumeKey(row.market.Exchange, row.market.Symbol, minute)]
		if observed {
			builder.observed++
		}
		if row.gap > 0 {
			builder.gapMarkets++
			if !observed {
				builder.unknown++
			}
			if row.gap > builder.maxGap {
				builder.maxGap = row.gap
			}
		}
	}
	seals := make([]MinuteSeal, 0, len(builders))
	for _, exchange := range domain.DomesticExchanges {
		builder := builders[exchange]
		if builder == nil {
			continue
		}
		registryCount := builder.markets
		absent := registryCount - builder.observed
		status := SealComplete
		coverageSeconds := MinuteSeconds
		observedZero, unknown := absent, int64(0)
		if builder.maxGap > 0 {
			status = SealPartial
			coverageSeconds = MinuteSeconds - builder.maxGap
			unknown = builder.unknown
			observedZero = absent - unknown
			if coverageSeconds == 0 && builder.gapMarkets == registryCount {
				status = SealMissing
			}
		}
		seals = append(seals, MinuteSeal{
			Exchange: exchange, MinuteStart: minute, Status: status,
			CoverageSeconds: coverageSeconds, GapSeconds: builder.maxGap,
			RegistryMarketCount: registryCount, ObservedMarketCount: builder.observed,
			ObservedZeroCount: observedZero, UnknownMarketCount: unknown,
			GapMarketCount: builder.gapMarkets, ScheduledMarketCount: scheduledCounts[exchange],
			RegistryFingerprint: s.registryFingerprint(exchange, eligible),
		})
	}
	return seals
}

func (s *Scheduler) registryFingerprint(exchange domain.Exchange, markets []rankedMarket) string {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	entry, cached := s.registryFingerprints[exchange]
	index := 0
	unchanged := cached
	for _, row := range markets {
		if row.market.Exchange != exchange {
			continue
		}
		identity := normalizeRegistryIdentity(row.market)
		if unchanged && (index >= len(entry.identities) || entry.identities[index] != identity) {
			unchanged = false
		}
		index++
	}
	if unchanged && index == len(entry.identities) {
		return entry.fingerprint
	}

	identities := make([]registryIdentity, 0, index)
	for _, row := range markets {
		if row.market.Exchange == exchange {
			identities = append(identities, normalizeRegistryIdentity(row.market))
		}
	}
	sorted := append([]registryIdentity(nil), identities...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].symbol != sorted[j].symbol {
			return sorted[i].symbol < sorted[j].symbol
		}
		if sorted[i].base != sorted[j].base {
			return sorted[i].base < sorted[j].base
		}
		return sorted[i].quote < sorted[j].quote
	})
	hash := sha256.New()
	for _, identity := range sorted {
		_, _ = hash.Write([]byte(identity.symbol))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(identity.base))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(identity.quote))
		_, _ = hash.Write([]byte{'\n'})
	}
	fingerprint := hex.EncodeToString(hash.Sum(nil))
	s.registryFingerprints[exchange] = registryFingerprintCache{identities: identities, fingerprint: fingerprint}
	return fingerprint
}

func normalizeRegistryIdentity(market domain.MarketPair) registryIdentity {
	return registryIdentity{
		symbol: strings.ToUpper(strings.TrimSpace(market.Symbol)),
		base:   strings.ToUpper(strings.TrimSpace(market.Base)),
		quote:  strings.ToUpper(strings.TrimSpace(market.Quote)),
	}
}

type selectedScheduleRows struct {
	rows         []rankedMarket
	sampledQuiet int
}

func selectScheduledRows(targetStartMS int64, rows []rankedMarket) selectedScheduleRows {
	result := selectedScheduleRows{rows: make([]rankedMarket, 0, len(rows))}
	quietByExchange := make(map[domain.Exchange][]rankedMarket)
	for _, row := range rows {
		if row.priority != restbudget.PriorityRoundRobin {
			result.rows = append(result.rows, row)
			continue
		}
		quietByExchange[row.market.Exchange] = append(quietByExchange[row.market.Exchange], row)
	}
	minuteIndex := targetStartMS / MinuteMS
	for _, exchange := range domain.DomesticExchanges {
		quiet := quietByExchange[exchange]
		cap := QuietMarketSampleCaps[exchange]
		if cap <= 0 || len(quiet) == 0 {
			continue
		}
		if cap > len(quiet) {
			cap = len(quiet)
		}
		offset := int(((minuteIndex % int64(len(quiet))) * int64(cap)) % int64(len(quiet)))
		for index := 0; index < cap; index++ {
			result.rows = append(result.rows, quiet[(offset+index)%len(quiet)])
		}
		result.sampledQuiet += cap
	}
	sort.SliceStable(result.rows, func(i, j int) bool {
		if result.rows[i].priority != result.rows[j].priority {
			return result.rows[i].priority < result.rows[j].priority
		}
		if result.rows[i].volume != result.rows[j].volume {
			return result.rows[i].volume > result.rows[j].volume
		}
		return result.rows[i].market.Key() < result.rows[j].market.Key()
	})
	return result
}

func (s *Scheduler) Enqueue(job CandleJob) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enqueueLocked(job)
}

// ScheduleGapRepair admits one bounded post-close repair per market-minute.
// Reuse a queued request, or append the next immutable observation attempt;
// never replay attempt zero over different stored evidence.
func (s *Scheduler) ScheduleGapRepair(job CandleJob) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := volumeKey(job.Exchange, job.Market.Symbol, domain.UnixSecond(job.TargetStartMS/1000))
	previous, exists := s.jobHistory[key]
	if previous.gapRepair || job.KnownGapSeconds <= 0 {
		return false
	}
	for index, queued := range s.queues[job.Exchange] {
		if queued.Market.Symbol == job.Market.Symbol && queued.TargetStartMS == job.TargetStartMS {
			queued.KnownGapSeconds = clampGap(job.KnownGapSeconds)
			queued.Priority = restbudget.PriorityGapRepair
			s.queues[job.Exchange][index] = queued
			previous.gapRepair = true
			s.jobHistory[key] = previous
			return false
		}
	}
	if exists {
		job.Attempt = previous.attempt + 1
	}
	if job.Attempt > len(RetryOffsetsMS) {
		return false
	}
	job.Priority = restbudget.PriorityGapRepair
	if !s.enqueueLocked(job) {
		return false
	}
	progress := s.jobHistory[key]
	progress.gapRepair = true
	s.jobHistory[key] = progress
	return true
}

func (s *Scheduler) enqueueLocked(job CandleJob) bool {
	job.KnownGapSeconds = clampGap(job.KnownGapSeconds)
	if job.ObservedVolumeKRW < 0 || math.IsNaN(job.ObservedVolumeKRW) {
		job.ObservedVolumeKRW = 0
	}
	if _, exists := s.jobKeys[job.key()]; exists {
		return false
	}
	s.sequence++
	job.Sequence = s.sequence
	s.jobKeys[job.key()] = struct{}{}
	key := volumeKey(job.Exchange, job.Market.Symbol, domain.UnixSecond(job.TargetStartMS/1000))
	progress := s.jobHistory[key]
	progress.attempt = max(progress.attempt, job.Attempt)
	progress.gapRepair = progress.gapRepair || job.KnownGapSeconds > 0
	s.jobHistory[key] = progress
	s.queues[job.Exchange] = append(s.queues[job.Exchange], job)
	return true
}

func (s *Scheduler) Next(exchange domain.Exchange, serverNowMS int64) (*CandleJob, []CandleJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneRetryMarketsLocked(serverNowMS)
	queue := s.queues[exchange]
	kept := queue[:0]
	dropped := make([]CandleJob, 0)
	for _, job := range queue {
		if serverNowMS > job.TargetStartMS+MaxJobAgeMS {
			delete(s.jobKeys, job.key())
			dropped = append(dropped, job)
		} else {
			kept = append(kept, job)
		}
	}
	s.queues[exchange] = kept
	selected := -1
	for index, job := range kept {
		if job.DueServerMS > serverNowMS {
			continue
		}
		if selected < 0 || jobLess(job, kept[selected]) {
			selected = index
		}
	}
	if selected < 0 {
		return nil, dropped
	}
	job := kept[selected]
	s.queues[exchange] = append(kept[:selected], kept[selected+1:]...)
	delete(s.jobKeys, job.key())
	return &job, dropped
}

func (s *Scheduler) ScheduleRetry(job CandleJob) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.Attempt >= len(RetryOffsetsMS) {
		return false
	}
	key := retryBatchKey{exchange: job.Exchange, targetStartMS: job.TargetStartMS}
	admitted := s.retryMarkets[key]
	if admitted == nil {
		admitted = make(map[string]struct{})
	}
	_, already := admitted[job.Market.Symbol]
	if !already && len(admitted) >= RetryMarketCaps[job.Exchange] {
		return false
	}
	next := job
	next.Attempt++
	if previous, exists := s.jobHistory[volumeKey(job.Exchange, job.Market.Symbol, domain.UnixSecond(job.TargetStartMS/1000))]; exists && previous.attempt >= next.Attempt {
		return false
	}
	next.DueServerMS = job.TargetStartMS + MinuteMS + RetryOffsetsMS[next.Attempt-1]
	next.Priority = next.requestPriority()
	if !s.enqueueLocked(next) {
		return false
	}
	if !already {
		admitted[job.Market.Symbol] = struct{}{}
		s.retryMarkets[key] = admitted
	}
	return true
}

func (s *Scheduler) HasQueuedBatch(exchange domain.Exchange, targetStartMS int64, attempt int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range s.queues[exchange] {
		if job.TargetStartMS == targetStartMS && job.Attempt == attempt {
			return true
		}
	}
	return false
}

func (s *Scheduler) pruneRetryMarketsLocked(serverNowMS int64) {
	for key := range s.jobHistory {
		if serverNowMS > int64(key.minute)*1000+MaxJobAgeMS {
			delete(s.jobHistory, key)
		}
	}
	for key := range s.retryMarkets {
		if serverNowMS > key.targetStartMS+MaxJobAgeMS {
			delete(s.retryMarkets, key)
		}
	}
}

func (s *Scheduler) QueueLen(exchange domain.Exchange) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queues[exchange])
}

func (s *Scheduler) OpenMinuteCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accumulator.Len()
}

func KnownGapSeconds(exchange domain.Exchange, market string, minute domain.UnixSecond, gaps []coverage.Gap, clockSkewMS float64) int64 {
	start, end := int64(minute), int64(minute)+59
	skewSeconds := int64(math.Round(clockSkewMS / 1000))
	affected := make(map[int64]struct{})
	for _, gap := range gaps {
		gapStart := max64(start, gap.Start-skewSeconds)
		gapEnd := end
		if gap.End != nil {
			gapEnd = min64(end, *gap.End-skewSeconds)
		}
		if gapStart > gapEnd || !gapAffectsExchange(gap, exchange) || !gapAffectsMarket(gap, market) {
			continue
		}
		for second := gapStart; second <= gapEnd; second++ {
			affected[second] = struct{}{}
		}
	}
	if len(affected) > 60 {
		return 60
	}
	return int64(len(affected))
}

func gapAffectsExchange(gap coverage.Gap, exchange domain.Exchange) bool {
	if len(gap.Context.AffectedExchanges) == 0 {
		return true
	}
	for _, value := range gap.Context.AffectedExchanges {
		if strings.EqualFold(strings.TrimSpace(value), string(exchange)) {
			return true
		}
	}
	return false
}
func gapAffectsMarket(gap coverage.Gap, market string) bool {
	if len(gap.Context.AffectedMarkets) == 0 {
		return true
	}
	wanted := normalMarket(market)
	for _, value := range gap.Context.AffectedMarkets {
		parts := strings.FieldsFunc(value, func(r rune) bool { return r == ':' || r == '|' })
		if len(parts) > 0 && normalMarket(parts[len(parts)-1]) == wanted {
			return true
		}
	}
	return false
}
func normalMarket(value string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
}
func volumeKey(exchange domain.Exchange, market string, minute domain.UnixSecond) volumeIdentity {
	return volumeIdentity{exchange: exchange, market: market, minute: minute}
}
func jobLess(a, b CandleJob) bool {
	if a.TargetStartMS != b.TargetStartMS {
		return a.TargetStartMS > b.TargetStartMS
	}
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if a.ObservedVolumeKRW != b.ObservedVolumeKRW {
		return a.ObservedVolumeKRW > b.ObservedVolumeKRW
	}
	if a.DueServerMS != b.DueServerMS {
		return a.DueServerMS < b.DueServerMS
	}
	return a.Sequence < b.Sequence
}
func isDomestic(exchange domain.Exchange) bool {
	_, ok := domesticExchangeIndex(exchange)
	return ok
}
func domesticExchangeIndex(exchange domain.Exchange) (int, bool) {
	switch exchange {
	case domain.ExchangeUpbit:
		return 0, true
	case domain.ExchangeBithumb:
		return 1, true
	case domain.ExchangeCoinone:
		return 2, true
	case domain.ExchangeKorbit:
		return 3, true
	case domain.ExchangeGopax:
		return 4, true
	default:
		return 0, false
	}
}
func medianLocked(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]int64(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	m := len(copyValues) / 2
	if len(copyValues)%2 == 0 {
		return float64(copyValues[m-1])/2 + float64(copyValues[m])/2
	}
	return float64(copyValues[m])
}
func abs64(value int64) int64 {
	if value == math.MinInt64 {
		return math.MaxInt64
	}
	if value < 0 {
		return -value
	}
	return value
}
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
