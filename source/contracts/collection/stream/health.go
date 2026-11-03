package collector

type ExchangeHealth struct {
	Enabled                          bool     `json:"enabled"`
	DisabledReason                   string   `json:"disabled_reason"`
	OrderBookEnabled                 bool     `json:"orderbook_enabled"`
	OrderBookDisabledReason          string   `json:"orderbook_disabled_reason"`
	MarketCount                      int      `json:"market_count"`
	AssetCount                       int      `json:"asset_count"`
	SubscribedMarketCount            int      `json:"subscribed_market_count"`
	SubscribedMarketSymbols          []string `json:"-"`
	KRWMarketCount                   int      `json:"krw_market_count"`
	KRWSubscribedMarketCount         int      `json:"krw_subscribed_market_count"`
	KRWSubscribedMarketSymbols       []string `json:"-"`
	ConnectedMarketCount             int      `json:"connected_market_count"`
	KRWConnectedMarketCount          int      `json:"krw_connected_market_count"`
	ConnectedMarketPercent           *float64 `json:"connected_market_pct"`
	FreshMarketCount                 int      `json:"fresh_market_count"`
	FreshTradeMarketCount            int      `json:"fresh_trade_market_count"`
	FreshOrderBookMarketCount        int      `json:"fresh_orderbook_market_count"`
	ObservedTradeMarketCount         int      `json:"observed_trade_market_count"`
	ObservedOrderBookMarketCount     int      `json:"observed_orderbook_market_count"`
	FreshSubscriptionMarketCount     int      `json:"fresh_subscription_market_count"`
	FreshMarketPercent               *float64 `json:"fresh_market_pct"`
	FreshSubscriptionPercent         *float64 `json:"fresh_subscription_market_pct"`
	ChunkCount                       int      `json:"chunk_count"`
	KRWChunkCount                    int      `json:"krw_chunk_count"`
	ConnectedChunks                  int      `json:"connected_chunks"`
	KRWConnectedChunks               int      `json:"krw_connected_chunks"`
	FreshChunks                      int      `json:"fresh_chunks"`
	KRWFreshChunks                   int      `json:"krw_fresh_chunks"`
	StaleChunks                      int      `json:"stale_chunks"`
	KRWStaleChunks                   int      `json:"krw_stale_chunks"`
	NeverReceivedChunks              int      `json:"never_received_chunks"`
	NeverReceivedTradeChunks         int      `json:"never_received_trade_chunks"`
	KRWNeverReceivedTradeChunks      int      `json:"krw_never_received_trade_chunks"`
	NeverReceivedOrderBookChunks     int      `json:"never_received_orderbook_chunks"`
	ActiveSubscriptionErrorChunks    int      `json:"active_subscription_error_chunks"`
	KRWActiveSubscriptionErrorChunks int      `json:"krw_active_subscription_error_chunks"`
	ActiveTradeParseErrorChunks      int      `json:"active_trade_parse_error_chunks"`
	KRWActiveTradeParseErrorChunks   int      `json:"krw_active_trade_parse_error_chunks"`
	RealtimeReady                    bool     `json:"realtime_ready"`
	TradeStreamReady                 bool     `json:"trade_stream_ready"`
	KRWRealtimeReady                 bool     `json:"krw_realtime_ready"`
	KRWTradeStreamReady              bool     `json:"krw_trade_stream_ready"`
	KRWTradeActivityFresh            bool     `json:"krw_trade_activity_fresh"`
	TradeActivityFresh               bool     `json:"trade_activity_fresh"`
	OrderBookStreamReady             bool     `json:"orderbook_stream_ready"`
	Messages                         uint64   `json:"messages"`
	Trades                           uint64   `json:"trades"`
	TradeMatches                     uint64   `json:"-"`
	UnknownTradeSides                uint64   `json:"-"`
	KRWUnknownTradeSides             uint64   `json:"-"`
	TradeMessages                    uint64   `json:"trade_messages"`
	TradeSnapshotsSkipped            uint64   `json:"trade_snapshots_skipped"`
	UnparsedTradeEvents              uint64   `json:"unparsed_trade_events"`
	KRWUnparsedTradeEvents           uint64   `json:"krw_unparsed_trade_events"`
	UnparsedTradeMatches             uint64   `json:"-"`
	OrderBooks                       uint64   `json:"orderbooks"`
	ParseErrors                      uint64   `json:"parse_errors"`
	SubscriptionErrors               uint64   `json:"subscription_errors"`
	Reconnects                       uint64   `json:"reconnects"`
	DisconnectedSeconds              float64  `json:"disconnected_seconds_across_chunks"`
	LastMessageTS                    float64  `json:"last_message_ts"`
	OldestChunkMessageTS             float64  `json:"oldest_chunk_message_ts"`
	// OldestChunkTradeTS is internal evidence for retroactive coverage
	// invalidation when order books remain active but a trade channel stalls.
	OldestChunkTradeTS    float64  `json:"-"`
	KRWOldestChunkDataTS  float64  `json:"-"`
	KRWOldestChunkTradeTS float64  `json:"-"`
	MaxMessageAgeSec      *float64 `json:"max_message_age_sec"`
	MaxDataAgeSec         *float64 `json:"max_data_age_sec"`
	MaxTradeAgeSec        *float64 `json:"max_trade_age_sec"`
	MaxOrderBookAgeSec    *float64 `json:"max_orderbook_age_sec"`
	LastTradeTS           float64  `json:"last_trade_ts"`
	LastOrderBookTS       float64  `json:"last_orderbook_ts"`
	LastDisconnectError   string   `json:"last_disconnect_error"`
	LastDisconnectErrorTS float64  `json:"last_disconnect_error_ts"`
	Errors                []string `json:"errors"`
	Transport             string   `json:"transport"`
	RESTFallbackEnabled   bool     `json:"rest_fallback_enabled"`
}

type MarketActivity struct {
	Connected            bool     `json:"connected"`
	SubscriptionError    bool     `json:"subscription_error"`
	TradeAgeSec          *float64 `json:"trade_age_sec"`
	OrderBookAgeSec      *float64 `json:"orderbook_age_sec"`
	ChunkOrderBookAgeSec *float64 `json:"chunk_orderbook_age_sec"`
	ChunkDataAgeSec      *float64 `json:"chunk_data_age_sec"`
}
