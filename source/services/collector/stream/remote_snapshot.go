package collector

import (
	"market-share-go/internal/domain"
	"slices"
	"time"
)

type remoteSnapshot struct {
	At        time.Time
	Exchanges map[domain.Exchange]ExchangeHealth
	Activity  map[string]MarketActivity
}

// RestoreRemoteSnapshot installs an immutable journal snapshot without opening
// sockets. Values and private fields are detached without JSON projection.
func (m *Manager) RestoreRemoteSnapshot(at time.Time, health map[domain.Exchange]ExchangeHealth, activity map[string]MarketActivity) {
	value := cloneRemoteSnapshot(&remoteSnapshot{At: at, Exchanges: health, Activity: activity})
	m.mu.Lock()
	defer m.mu.Unlock()
	m.remote = value
}
func cloneRemoteSnapshot(value *remoteSnapshot) *remoteSnapshot {
	if value == nil {
		return nil
	}
	result := remoteSnapshot{At: value.At}
	if value.Exchanges != nil {
		result.Exchanges = make(map[domain.Exchange]ExchangeHealth, len(value.Exchanges))
		for key, row := range value.Exchanges {
			row.SubscribedMarketSymbols = slices.Clone(row.SubscribedMarketSymbols)
			row.KRWSubscribedMarketSymbols = slices.Clone(row.KRWSubscribedMarketSymbols)
			row.Errors = slices.Clone(row.Errors)
			row.ConnectedMarketPercent = cloneSnapshotFloat(row.ConnectedMarketPercent)
			row.FreshMarketPercent = cloneSnapshotFloat(row.FreshMarketPercent)
			row.FreshSubscriptionPercent = cloneSnapshotFloat(row.FreshSubscriptionPercent)
			row.MaxMessageAgeSec = cloneSnapshotFloat(row.MaxMessageAgeSec)
			row.MaxDataAgeSec = cloneSnapshotFloat(row.MaxDataAgeSec)
			row.MaxTradeAgeSec = cloneSnapshotFloat(row.MaxTradeAgeSec)
			row.MaxOrderBookAgeSec = cloneSnapshotFloat(row.MaxOrderBookAgeSec)
			result.Exchanges[key] = row
		}
	}
	// Gob flattens a zero-valued pointer field into an omitted field. Market
	// activity distinguishes an observed age of zero from no observation, so
	// detach those values directly instead of round-tripping them through gob.
	if value.Activity != nil {
		result.Activity = make(map[string]MarketActivity, len(value.Activity))
		for key, row := range value.Activity {
			row.TradeAgeSec = cloneSnapshotFloat(row.TradeAgeSec)
			row.OrderBookAgeSec = cloneSnapshotFloat(row.OrderBookAgeSec)
			row.ChunkOrderBookAgeSec = cloneSnapshotFloat(row.ChunkOrderBookAgeSec)
			row.ChunkDataAgeSec = cloneSnapshotFloat(row.ChunkDataAgeSec)
			result.Activity[key] = row
		}
	}
	return &result
}

func cloneSnapshotFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (m *Manager) remoteSnapshotAt(now time.Time) *remoteSnapshot {
	if now.IsZero() {
		now = time.Now()
	}
	m.mu.Lock()
	value := m.remote
	m.mu.Unlock()
	result := cloneRemoteSnapshot(value)
	if result == nil {
		return nil
	}
	age := now.Sub(result.At).Seconds()
	if age < 0 {
		age = 0
	}
	for key, row := range result.Activity {
		for _, p := range []*float64{row.TradeAgeSec, row.OrderBookAgeSec, row.ChunkOrderBookAgeSec, row.ChunkDataAgeSec} {
			if p != nil {
				*p += age
			}
		}
		if age > 3 {
			row.Connected = false
		}
		result.Activity[key] = row
	}
	if age > 3 {
		for key, row := range result.Exchanges {
			row.RealtimeReady = false
			row.TradeStreamReady = false
			row.KRWRealtimeReady = false
			row.KRWTradeStreamReady = false
			row.OrderBookStreamReady = false
			row.TradeActivityFresh = false
			row.KRWTradeActivityFresh = false
			result.Exchanges[key] = row
		}
	}
	return result
}
