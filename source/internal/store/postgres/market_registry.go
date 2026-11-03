package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/registry"
)

const (
	registryRefreshSuccess  = "SUCCESS"
	registryRefreshError    = "ERROR"
	registryEventDiscovered = "DISCOVERED"
	registryEventAdded      = "ADDED"
	registryEventRemoved    = "REMOVED"
)

type storedRegistryMarket struct {
	pair              domain.MarketPair
	active            bool
	firstDiscoveredMS int64
	currentSinceMS    *int64
}

type registryEvent struct {
	pair      domain.MarketPair
	eventType string
}

// LoadActiveMarketRegistry returns the last authoritative membership retained
// in market_registry_current. It is the startup fallback when an exchange's
// registry endpoint is temporarily unavailable.
func (r *Repository) LoadActiveMarketRegistry(ctx context.Context) ([]domain.MarketPair, error) {
	return loadActiveMarketRegistry(ctx, r.pool)
}

type registryRowsReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadActiveMarketRegistry(ctx context.Context, reader registryRowsReader) ([]domain.MarketPair, error) {
	rows, err := reader.Query(ctx, `
		SELECT exchange_name, market, base, quote, scope,
			maker_fee_rate, taker_fee_rate, fee_source
		FROM market_registry_current
		WHERE active
		ORDER BY exchange_name, market`)
	if err != nil {
		return nil, fmt.Errorf("load active market registry: %w", err)
	}
	defer rows.Close()
	markets := make([]domain.MarketPair, 0)
	for rows.Next() {
		var pair domain.MarketPair
		if err := rows.Scan(
			&pair.Exchange, &pair.Symbol, &pair.Base, &pair.Quote, &pair.Scope,
			&pair.MakerFee, &pair.TakerFee, &pair.FeeSource,
		); err != nil {
			return nil, fmt.Errorf("scan active market registry: %w", err)
		}
		markets = append(markets, pair)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active market registry: %w", err)
	}
	universe, err := domain.NewMarketUniverse(markets)
	if err != nil {
		return nil, fmt.Errorf("validate active market registry: %w", err)
	}
	return universe.Markets(), nil
}

// SaveMarketRegistryObservation stores one complete registry poll. Successful
// exchanges replace their active membership; failed exchanges record the
// failure without changing the last known-good membership.
func (r *Repository) SaveMarketRegistryObservation(
	ctx context.Context,
	snapshot registry.Snapshot,
	universe domain.MarketUniverse,
	exchanges []domain.Exchange,
) error {
	observedAtMS, err := registryObservedAtMS(snapshot.UpdatedAt)
	if err != nil {
		return err
	}
	if snapshot.Version > math.MaxInt64 {
		return fmt.Errorf("registry version %d exceeds PostgreSQL BIGINT", snapshot.Version)
	}
	exchanges, err = normalizedRegistryExchanges(exchanges)
	if err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin market registry observation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, exchange := range exchanges {
		exchangeSnapshot, ok := snapshot.Exchanges[exchange]
		if !ok {
			return fmt.Errorf("registry snapshot is missing %s", exchange)
		}
		if err := saveExchangeRegistryObservation(
			ctx, tx, observedAtMS, int64(snapshot.Version), exchange,
			exchangeSnapshot, universe.ForExchange(exchange),
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit market registry observation: %w", err)
	}
	return nil
}

func saveExchangeRegistryObservation(
	ctx context.Context,
	tx pgx.Tx,
	observedAtMS int64,
	version int64,
	exchange domain.Exchange,
	snapshot registry.ExchangeSnapshot,
	markets []domain.MarketPair,
) error {
	existing, err := loadStoredRegistryMarkets(ctx, tx, exchange)
	if err != nil {
		return err
	}
	previousCount := activeRegistryMarketCount(existing)

	status := registryRefreshSuccess
	authoritative := true
	errorText := strings.TrimSpace(snapshot.Error)
	if errorText != "" {
		status = registryRefreshError
		authoritative = false
	}
	duplicate, err := registryRefreshAlreadyStored(
		ctx, tx, observedAtMS, exchange, version, status, errorText,
	)
	if err != nil {
		return err
	}
	if duplicate {
		return nil
	}

	activeMarkets := markets
	observedCount := len(markets)
	discoveredCount, addedCount, removedCount := 0, 0, 0
	events := make([]registryEvent, 0)
	if authoritative {
		if len(markets) == 0 {
			return fmt.Errorf("successful %s registry observation has no markets", exchange)
		}
		if err := validateExchangeRegistrySnapshot(exchange, snapshot, markets); err != nil {
			return err
		}
		var hasBaseline bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM market_registry_refreshes
				WHERE exchange_name=$1 AND authoritative
			)`, exchange,
		).Scan(&hasBaseline); err != nil {
			return fmt.Errorf("read %s registry baseline: %w", exchange, err)
		}
		incoming := registryMarketMap(markets)
		for market, pair := range incoming {
			previous, exists := existing[market]
			switch {
			case !exists && !hasBaseline:
				discoveredCount++
				addedCount++
				events = append(events, registryEvent{pair: pair, eventType: registryEventDiscovered})
				events = append(events, registryEvent{pair: pair, eventType: registryEventAdded})
			case !exists || !previous.active:
				addedCount++
				events = append(events, registryEvent{pair: pair, eventType: registryEventAdded})
			}
		}
		for market, previous := range existing {
			if previous.active {
				if _, present := incoming[market]; !present {
					removedCount++
					events = append(events, registryEvent{pair: previous.pair, eventType: registryEventRemoved})
				}
			}
		}
	} else {
		observedCount = 0
		activeMarkets = activeStoredRegistryMarkets(existing)
	}

	quoteCounts, assetCount, publicFeeCount := registryMarketSummary(activeMarkets)
	quoteCountsJSON, err := json.Marshal(quoteCounts)
	if err != nil {
		return fmt.Errorf("encode %s registry quote counts: %w", exchange, err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO market_registry_refreshes(
			observed_at_ms, exchange_name, status, authoritative,
			registry_version, previous_market_count, observed_market_count,
			active_market_count, active_asset_count, quote_counts_json,
			public_fee_market_count, discovered_count, added_count,
			removed_count, error_text
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (observed_at_ms, exchange_name) DO UPDATE SET
			status=EXCLUDED.status,
			authoritative=EXCLUDED.authoritative,
			registry_version=EXCLUDED.registry_version,
			previous_market_count=EXCLUDED.previous_market_count,
			observed_market_count=EXCLUDED.observed_market_count,
			active_market_count=EXCLUDED.active_market_count,
			active_asset_count=EXCLUDED.active_asset_count,
			quote_counts_json=EXCLUDED.quote_counts_json,
			public_fee_market_count=EXCLUDED.public_fee_market_count,
			discovered_count=EXCLUDED.discovered_count,
			added_count=EXCLUDED.added_count,
			removed_count=EXCLUDED.removed_count,
			error_text=EXCLUDED.error_text`,
		observedAtMS, exchange, status, authoritative, version,
		previousCount, observedCount, len(activeMarkets), assetCount,
		string(quoteCountsJSON), publicFeeCount, discoveredCount,
		addedCount, removedCount, errorText,
	); err != nil {
		return fmt.Errorf("store %s registry refresh: %w", exchange, err)
	}
	if !authoritative {
		return nil
	}
	return applyAuthoritativeRegistry(ctx, tx, observedAtMS, exchange, markets, existing, events)
}

func applyAuthoritativeRegistry(
	ctx context.Context,
	tx pgx.Tx,
	observedAtMS int64,
	exchange domain.Exchange,
	markets []domain.MarketPair,
	existing map[string]storedRegistryMarket,
	events []registryEvent,
) error {
	batch := &pgx.Batch{}
	queued := 0
	incoming := registryMarketMap(markets)
	for _, pair := range markets {
		previous, exists := existing[pair.Symbol]
		if exists && previous.active && sameRegistryMarketPair(previous.pair, pair) {
			continue
		}
		batch.Queue(`
			INSERT INTO market_registry_current(
				exchange_name, market, base, quote, scope,
				maker_fee_rate, taker_fee_rate, fee_source, active,
				first_discovered_at_ms, current_since_ms,
				removed_at_ms, last_changed_at_ms
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,TRUE,$9,$9,NULL,$9)
			ON CONFLICT (exchange_name, market) DO UPDATE SET
				base=EXCLUDED.base,
				quote=EXCLUDED.quote,
				scope=EXCLUDED.scope,
				maker_fee_rate=EXCLUDED.maker_fee_rate,
				taker_fee_rate=EXCLUDED.taker_fee_rate,
				fee_source=EXCLUDED.fee_source,
				active=TRUE,
				current_since_ms=CASE
					WHEN market_registry_current.active THEN market_registry_current.current_since_ms
					ELSE EXCLUDED.current_since_ms
				END,
				removed_at_ms=NULL,
				last_changed_at_ms=CASE
					WHEN NOT market_registry_current.active
						OR market_registry_current.base IS DISTINCT FROM EXCLUDED.base
						OR market_registry_current.quote IS DISTINCT FROM EXCLUDED.quote
						OR market_registry_current.scope IS DISTINCT FROM EXCLUDED.scope
						OR market_registry_current.maker_fee_rate IS DISTINCT FROM EXCLUDED.maker_fee_rate
						OR market_registry_current.taker_fee_rate IS DISTINCT FROM EXCLUDED.taker_fee_rate
						OR market_registry_current.fee_source IS DISTINCT FROM EXCLUDED.fee_source
					THEN EXCLUDED.last_changed_at_ms
					ELSE market_registry_current.last_changed_at_ms
				END,
				updated_at=NOW()`,
			exchange, pair.Symbol, pair.Base, pair.Quote, pair.Scope,
			pair.MakerFee, pair.TakerFee, pair.FeeSource, observedAtMS,
		)
		queued++
	}
	for market, previous := range existing {
		if previous.active {
			if _, present := incoming[market]; !present {
				batch.Queue(`
					UPDATE market_registry_current SET
						active=FALSE,
						current_since_ms=NULL,
						removed_at_ms=$3,
						last_changed_at_ms=$3,
						updated_at=NOW()
					WHERE exchange_name=$1 AND market=$2 AND active`,
					exchange, market, observedAtMS,
				)
				queued++
			}
		}
	}
	for _, event := range events {
		batch.Queue(`
			INSERT INTO market_registry_events(
				observed_at_ms, exchange_name, market, base, quote, scope,
				event_type, maker_fee_rate, taker_fee_rate, fee_source
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (observed_at_ms, exchange_name, market, event_type) DO NOTHING`,
			observedAtMS, exchange, event.pair.Symbol, event.pair.Base,
			event.pair.Quote, event.pair.Scope, event.eventType,
			event.pair.MakerFee, event.pair.TakerFee, event.pair.FeeSource,
		)
		queued++
	}
	if queued == 0 {
		return nil
	}
	results := tx.SendBatch(ctx, batch)
	for index := 0; index < queued; index++ {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return fmt.Errorf("apply %s registry membership: %w", exchange, err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("close %s registry membership batch: %w", exchange, err)
	}
	return nil
}

func registryRefreshAlreadyStored(
	ctx context.Context,
	tx pgx.Tx,
	observedAtMS int64,
	exchange domain.Exchange,
	version int64,
	status string,
	errorText string,
) (bool, error) {
	var storedVersion int64
	var storedStatus, storedError string
	err := tx.QueryRow(ctx, `
		SELECT registry_version, status, error_text
		FROM market_registry_refreshes
		WHERE observed_at_ms=$1 AND exchange_name=$2`,
		observedAtMS, exchange,
	).Scan(&storedVersion, &storedStatus, &storedError)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s registry refresh identity: %w", exchange, err)
	}
	if storedVersion != version || storedStatus != status || storedError != errorText {
		return false, fmt.Errorf(
			"registry refresh identity collision for %s at %d",
			exchange, observedAtMS,
		)
	}
	return true, nil
}

func loadStoredRegistryMarkets(
	ctx context.Context,
	tx pgx.Tx,
	exchange domain.Exchange,
) (map[string]storedRegistryMarket, error) {
	rows, err := tx.Query(ctx, `
		SELECT market, base, quote, scope, maker_fee_rate, taker_fee_rate,
			fee_source, active, first_discovered_at_ms, current_since_ms
		FROM market_registry_current
		WHERE exchange_name=$1
		FOR UPDATE`, exchange)
	if err != nil {
		return nil, fmt.Errorf("read %s registry membership: %w", exchange, err)
	}
	defer rows.Close()
	result := make(map[string]storedRegistryMarket)
	for rows.Next() {
		var market storedRegistryMarket
		market.pair.Exchange = exchange
		if err := rows.Scan(
			&market.pair.Symbol, &market.pair.Base, &market.pair.Quote,
			&market.pair.Scope, &market.pair.MakerFee, &market.pair.TakerFee,
			&market.pair.FeeSource, &market.active, &market.firstDiscoveredMS,
			&market.currentSinceMS,
		); err != nil {
			return nil, fmt.Errorf("scan %s registry membership: %w", exchange, err)
		}
		result[market.pair.Symbol] = market
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s registry membership: %w", exchange, err)
	}
	return result, nil
}

func registryObservedAtMS(updatedAt float64) (int64, error) {
	if math.IsNaN(updatedAt) || math.IsInf(updatedAt, 0) || updatedAt <= 0 ||
		updatedAt > float64(math.MaxInt64)/1000 {
		return 0, fmt.Errorf("invalid registry timestamp %v", updatedAt)
	}
	return int64(math.Round(updatedAt * 1000)), nil
}

func normalizedRegistryExchanges(exchanges []domain.Exchange) ([]domain.Exchange, error) {
	seen := make(map[domain.Exchange]struct{}, len(exchanges))
	result := make([]domain.Exchange, 0, len(exchanges))
	for _, exchange := range exchanges {
		if !exchange.Valid() {
			return nil, fmt.Errorf("invalid registry exchange %q", exchange)
		}
		if _, exists := seen[exchange]; exists {
			continue
		}
		seen[exchange] = struct{}{}
		result = append(result, exchange)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func validateExchangeRegistrySnapshot(
	exchange domain.Exchange,
	snapshot registry.ExchangeSnapshot,
	markets []domain.MarketPair,
) error {
	quotes, assets, publicFees := registryMarketSummary(markets)
	if snapshot.MarketCount != len(markets) || snapshot.AssetCount != assets ||
		snapshot.PublicFeeMarketCount != publicFees || !sameRegistryQuoteCounts(snapshot.Quotes, quotes) {
		return fmt.Errorf("%s registry snapshot and universe do not match", exchange)
	}
	return nil
}

func registryMarketMap(markets []domain.MarketPair) map[string]domain.MarketPair {
	result := make(map[string]domain.MarketPair, len(markets))
	for _, market := range markets {
		result[market.Symbol] = market
	}
	return result
}

func sameRegistryMarketPair(left, right domain.MarketPair) bool {
	return left.Exchange == right.Exchange && left.Symbol == right.Symbol &&
		left.Base == right.Base && left.Quote == right.Quote && left.Scope == right.Scope &&
		left.FeeSource == right.FeeSource && sameRegistryOptionalFloat(left.MakerFee, right.MakerFee) &&
		sameRegistryOptionalFloat(left.TakerFee, right.TakerFee)
}

func sameRegistryOptionalFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func activeRegistryMarketCount(markets map[string]storedRegistryMarket) int {
	count := 0
	for _, market := range markets {
		if market.active {
			count++
		}
	}
	return count
}

func activeStoredRegistryMarkets(markets map[string]storedRegistryMarket) []domain.MarketPair {
	result := make([]domain.MarketPair, 0, len(markets))
	for _, market := range markets {
		if market.active {
			result = append(result, market.pair)
		}
	}
	return result
}

func registryMarketSummary(markets []domain.MarketPair) (map[string]int, int, int) {
	quotes := make(map[string]int)
	assets := make(map[string]struct{})
	publicFees := 0
	for _, market := range markets {
		quotes[market.Quote]++
		assets[market.Base] = struct{}{}
		if market.TakerFee != nil {
			publicFees++
		}
	}
	return quotes, len(assets), publicFees
}

func sameRegistryQuoteCounts(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for quote, count := range left {
		if right[quote] != count {
			return false
		}
	}
	return true
}

func (r *Repository) MarketRegistryHistory(
	ctx context.Context,
	filter registry.HistoryFilter,
) (registry.History, error) {
	filter, err := normalizeRegistryHistoryFilter(filter)
	if err != nil {
		return registry.History{}, err
	}
	refreshes, err := r.marketRegistryRefreshHistory(ctx, filter)
	if err != nil {
		return registry.History{}, err
	}
	events, err := r.marketRegistryEventHistory(ctx, filter)
	if err != nil {
		return registry.History{}, err
	}
	return registry.History{
		Refreshes:   refreshes,
		Events:      events,
		GeneratedAt: float64(time.Now().UnixNano()) / 1e9,
	}, nil
}

func normalizeRegistryHistoryFilter(filter registry.HistoryFilter) (registry.HistoryFilter, error) {
	if filter.FromMS < 0 || filter.ToMS < 0 ||
		filter.FromMS > 0 && filter.ToMS > 0 && filter.FromMS > filter.ToMS {
		return filter, fmt.Errorf("invalid market registry time range")
	}
	if filter.Exchange != "" && !filter.Exchange.Valid() {
		return filter, fmt.Errorf("invalid market registry exchange %q", filter.Exchange)
	}
	filter.EventType = strings.ToUpper(strings.TrimSpace(filter.EventType))
	switch filter.EventType {
	case "", registryEventDiscovered, registryEventAdded, registryEventRemoved:
	default:
		return filter, fmt.Errorf("invalid market registry event type %q", filter.EventType)
	}
	filter.Asset = strings.ToUpper(strings.TrimSpace(filter.Asset))
	if filter.Limit < 1 {
		filter.Limit = 100
	}
	if filter.Limit > 200 {
		return filter, fmt.Errorf("market registry history limit exceeds 200")
	}
	return filter, nil
}

func (r *Repository) marketRegistryRefreshHistory(
	ctx context.Context,
	filter registry.HistoryFilter,
) ([]registry.RefreshObservation, error) {
	var query strings.Builder
	query.WriteString(`
		SELECT observed_at_ms, exchange_name, status, authoritative,
			registry_version, previous_market_count, observed_market_count,
			active_market_count, active_asset_count, quote_counts_json,
			public_fee_market_count, discovered_count, added_count,
			removed_count, error_text
		FROM market_registry_refreshes
		WHERE TRUE`)
	args := make([]any, 0, 4)
	addRegistryHistoryCondition(&query, &args, " AND observed_at_ms >= $%d", filter.FromMS, filter.FromMS > 0)
	addRegistryHistoryCondition(&query, &args, " AND observed_at_ms <= $%d", filter.ToMS, filter.ToMS > 0)
	addRegistryHistoryCondition(&query, &args, " AND exchange_name = $%d", filter.Exchange, filter.Exchange != "")
	args = append(args, filter.Limit)
	query.WriteString(fmt.Sprintf(" ORDER BY observed_at_ms DESC, exchange_name LIMIT $%d", len(args)))

	rows, err := r.pool.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query market registry refresh history: %w", err)
	}
	defer rows.Close()
	result := make([]registry.RefreshObservation, 0)
	for rows.Next() {
		var item registry.RefreshObservation
		var quotesJSON []byte
		if err := rows.Scan(
			&item.ObservedAtMS, &item.Exchange, &item.Status, &item.Authoritative,
			&item.RegistryVersion, &item.PreviousMarketCount, &item.ObservedMarketCount,
			&item.ActiveMarketCount, &item.ActiveAssetCount, &quotesJSON,
			&item.PublicFeeCount, &item.DiscoveredCount, &item.AddedCount,
			&item.RemovedCount, &item.Error,
		); err != nil {
			return nil, fmt.Errorf("scan market registry refresh history: %w", err)
		}
		if err := json.Unmarshal(quotesJSON, &item.QuoteCounts); err != nil {
			return nil, fmt.Errorf("decode market registry quote counts: %w", err)
		}
		if item.QuoteCounts == nil {
			item.QuoteCounts = map[string]int{}
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate market registry refresh history: %w", err)
	}
	return result, nil
}

func (r *Repository) marketRegistryEventHistory(
	ctx context.Context,
	filter registry.HistoryFilter,
) ([]registry.LifecycleEvent, error) {
	var query strings.Builder
	query.WriteString(`
		SELECT event.observed_at_ms, event.exchange_name, event.market,
			event.base, event.quote, event.scope, event.event_type,
			EXISTS (
				SELECT 1 FROM market_registry_events discovered
				WHERE discovered.observed_at_ms=event.observed_at_ms
					AND discovered.exchange_name=event.exchange_name
					AND discovered.market=event.market
					AND discovered.event_type='DISCOVERED'
			),
			event.maker_fee_rate, event.taker_fee_rate, event.fee_source
		FROM market_registry_events event
		WHERE TRUE`)
	args := make([]any, 0, 6)
	addRegistryHistoryCondition(&query, &args, " AND event.observed_at_ms >= $%d", filter.FromMS, filter.FromMS > 0)
	addRegistryHistoryCondition(&query, &args, " AND event.observed_at_ms <= $%d", filter.ToMS, filter.ToMS > 0)
	addRegistryHistoryCondition(&query, &args, " AND event.exchange_name = $%d", filter.Exchange, filter.Exchange != "")
	addRegistryHistoryCondition(&query, &args, " AND event.event_type = $%d", filter.EventType, filter.EventType != "")
	addRegistryHistoryCondition(&query, &args, " AND event.base = $%d", filter.Asset, filter.Asset != "")
	args = append(args, filter.Limit)
	query.WriteString(fmt.Sprintf(" ORDER BY event.observed_at_ms DESC, event.event_id DESC LIMIT $%d", len(args)))

	rows, err := r.pool.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("query market registry event history: %w", err)
	}
	defer rows.Close()
	result := make([]registry.LifecycleEvent, 0)
	for rows.Next() {
		var item registry.LifecycleEvent
		if err := rows.Scan(
			&item.ObservedAtMS, &item.Exchange, &item.Market, &item.Base,
			&item.Quote, &item.Scope, &item.EventType, &item.InitialDiscovery,
			&item.MakerFee, &item.TakerFee, &item.FeeSource,
		); err != nil {
			return nil, fmt.Errorf("scan market registry event history: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate market registry event history: %w", err)
	}
	return result, nil
}

func addRegistryHistoryCondition(
	query *strings.Builder,
	args *[]any,
	clause string,
	value any,
	include bool,
) {
	if !include {
		return
	}
	*args = append(*args, value)
	query.WriteString(fmt.Sprintf(clause, len(*args)))
}
