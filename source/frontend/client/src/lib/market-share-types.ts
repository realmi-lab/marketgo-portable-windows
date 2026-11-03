export type MarketScope = "KRW" | "ALL_DOMESTIC_CONVERTED";

export interface MarketShareDataSource {
  historical_used: boolean;
  live_used: boolean;
  historical_watermark: number | null;
  live_watermark: number | null;
  gap_seconds?: number | null;
  overlap_resolved: boolean;
  historical_authoritative_until: number | null;
  live_authoritative_from: number | null;
  backfill_loaded_through?: number | null;
  backfill_read_mode?: string;
  websocket_coverage_sec?: number;
  official_rest_coverage_sec?: number;
  official_rest_exact_coverage_sec?: number;
  official_rest_estimated_coverage_sec?: number;
  official_rest_status?: string;
  official_rest_used?: boolean;
  recovered_gap_ids?: number[];
  recovery_mode?: string;
  official_reconciled?: boolean;
  volume_basis?: "WEBSOCKET_PLUS_EXACT_OFFICIAL_MINUTE_RESIDUAL" | string;
  official_minute_supplement_status?: "READY" | "UNAVAILABLE" | string;
  official_minute_supplement_used?: boolean;
  official_minute_replacement_used?: boolean;
  official_minute_replacement_count?: number;
  official_minute_replacement_volume_krw?: number;
  official_minute_replacement_adjustment_krw?: number;
  official_minute_supplement_volume_krw?: number;
  official_minute_supplement_bithumb_volume_krw?: number;
  official_minute_supplement_row_count?: number;
  official_minute_supplement_exchange_count?: number;
  official_minute_supplement_exchanges?: string[];
  official_minute_supplement_gap_sec?: number;
  official_minute_supplement_subminute_estimate?: boolean;
  official_minute_supplement_conflict_count?: number;
  official_minute_supplement_precision_difference_count?: number;
  official_minute_supplement_precision_policy?: string;
  official_minute_supplement_policy?: string;
  official_minute_supplement_unavailable_exchanges?: string[];
  official_candle_supplement_interval_sec?: 60 | 600 | number;
  official_24h_ticker_used?: boolean;
  official_24h_ticker_status?: string;
  official_24h_ticker_as_of_ms?: number;
  official_24h_ticker_collection_span_ms?: number;
  official_24h_ticker_exchange_count?: number;
  registry_fallback_active?: boolean;
  excluded_exchanges?: string[];
  micro_gap_tolerance_sec?: number;
  micro_gap_tolerated?: boolean;
  // The production analysis does not send these five (only the browser replay
  // fixture does). Declaring them required let the report footer print them
  // unchecked and end in a bare separator. The aggregation version the server
  // does send is context.aggregation_version.
  bar_type?: "SECOND_AGGREGATE";
  aggregation_version?: string;
  fx_method_version?: string;
  asset_registry_version?: string;
  domestic_exchange_set_version?: string;
}

export interface MarketShareReadiness {
  phase:
    | "STOPPED"
    | "REGISTRY"
    | "DEGRADED"
    | "AGGREGATING"
    | "CONNECTING"
    | "OBSERVING"
    | "READY";
  enabled_exchange_count: number;
  configured_exchange_count: number;
  ready_exchange_count: number;
  ready_exchanges: string[];
  blocking_exchanges: string[];
  registered_market_count: number;
  connected_market_count: number;
  connected_market_pct: number | null;
  aggregator_consumer_alive: boolean;
  active_trade_parse_error_chunks?: number;
  elapsed_sec: number;
  updated_at: number;
}

export interface MarketShareSummary {
  domestic_volume_krw: number;
  /** @deprecated Policy-disabled compatibility number; never treat zero as an observation. */
  global_context_volume_krw: number;
  bithumb_volume_krw: number;
  bithumb_share_pct: number | null;
  domestic_trade_count: number;
  bithumb_trade_count: number;
  bithumb_trade_count_share_pct: number | null;
  domestic_unresolved_fx_count: number;
  global_unresolved_fx_count: number;
  domestic_asset_count: number;
  bithumb_listed_asset_count: number;
  bithumb_listing_coverage_pct: number | null;
  bithumb_volume_weighted_listing_coverage_pct: number | null;
  unlisted_competitor_volume_krw: number;
  upbit_listed_asset_count: number;
  upbit_listing_coverage_pct: number | null;
  upbit_volume_weighted_listing_coverage_pct: number | null;
  upbit_unlisted_competitor_volume_krw: number;
  analyzed_asset_count: number;
  analysis_has_asset_limit: boolean;
  selected_window_complete: boolean;
  selected_window_coverage_sec: number;
  selected_window_required_sec: number;
  bithumb_shared_listing?: {
    status: string;
    asset_count: number;
    current_domestic_volume_krw: number;
    current_bithumb_volume_krw: number;
    current_share_pct: number | null;
    previous_domestic_volume_krw: number | null;
    previous_bithumb_volume_krw: number | null;
    previous_share_pct: number | null;
    share_change_pp: number | null;
    domestic_volume_coverage_pct: number | null;
  };
}

export interface MarketShareOfficialExchangeTicker24h {
  exchange: string;
  status: "LIVE" | "STALE" | "UNAVAILABLE";
  source: string;
  volume_krw: number | null;
  /** Server-derived share of the complete official domestic denominator. */
  share_pct?: number | null;
  market_count: number;
  as_of: number | null;
  exchange_timestamp_ms: number | null;
  last_error?: string | null;
  window_start_ms?: number;
  window_end_ms?: number;
  bar_interval_sec?: number;
  required_bar_count?: number;
}

export interface MarketShareOfficialDomestic24h {
  status: "LIVE" | "STALE" | "PARTIAL";
  source: "OFFICIAL_DOMESTIC_ALIGNED_1H_CANDLES_24H" | string;
  as_of: number;
  started_at: number;
  collection_span_ms: number;
  expected_exchange_count: number;
  ready_exchange_count: number;
  total_volume_krw: number | null;
  bithumb_volume_krw: number | null;
  bithumb_share_pct: number | null;
  exchanges: Record<string, MarketShareOfficialExchangeTicker24h>;
  window_start_ms?: number;
  window_end_ms?: number;
  bar_interval_sec?: number;
  required_bar_count?: number;
  included_exchanges?: string[];
  excluded_exchanges?: Record<string, string>;
}

export interface MarketShareOfficialMovementSnapshot {
  as_of_ms: number;
  status: string;
  source: string;
  collection_span_ms: number;
  total_volume_krw: number;
  bithumb_volume_krw: number;
  bithumb_share_pct: number;
  exchanges: Record<
    string,
    {
      exchange: string;
      status: string;
      source: string;
      volume_krw: number;
      market_count: number;
      exchange_timestamp_ms: number | null;
    }
  >;
}

export interface MarketShareOfficialMovementDriver {
  rank: number;
  asset: string;
  contribution_pp: number;
  bithumb_volume_change_krw: number;
  competitor_volume_change_krw: number;
  previous_bithumb_volume_krw: number;
  current_bithumb_volume_krw: number;
  previous_competitor_volume_krw: number;
  current_competitor_volume_krw: number;
}

export interface MarketShareOfficialMovementExchangeDriver {
  rank: number;
  exchange: string;
  contribution_pp: number;
  volume_change_krw: number;
  previous_volume_krw: number;
  current_volume_krw: number;
  leading_asset: string;
  leading_asset_contribution_pp: number;
}

export interface MarketShareOfficialMovementNotice {
  exchange: string;
  notice_id: string;
  title: string;
  url: string;
  category: string;
  published_ts_ms: number;
  detected_ts_ms: number;
  anchor_ts_ms: number;
  relation: string;
  matched_assets: string[];
}

export interface MarketShareOfficialMovementFlowEvidence {
  snapshot_kind: "CONFIRMED" | "EXTREME" | "ENDED" | string;
  official_as_of_ms: number;
  flow_as_of_ms: number;
  lag_ms: number;
  payload: MarketShareRealtimeFlowState;
}

export interface MarketShareOfficialMovementEvent {
  event_id: string;
  source?: string;
  direction: "UP" | "DOWN" | string;
  state: "ACTIVE" | "ENDED" | string;
  started_at_ms: number;
  confirmed_at_ms: number | null;
  updated_at_ms: number;
  ended_at_ms: number | null;
  baseline_share_pct: number;
  current_share_pct: number;
  extreme_share_pct: number;
  change_pp: number;
  extreme_change_pp: number;
  threshold_pp: number;
  quality_status: string;
  drivers: MarketShareOfficialMovementDriver[];
  exchange_drivers: MarketShareOfficialMovementExchangeDriver[];
  notices: MarketShareOfficialMovementNotice[];
  flow_evidence: MarketShareOfficialMovementFlowEvidence[];
}

export interface MarketShareOfficialMovementState {
  version: "OFFICIAL_SHARE_MONITOR_V1" | string;
  generated_at_ms: number;
  current: MarketShareOfficialMovementSnapshot | null;
  movement: MarketShareOfficialMovementEvent | null;
  recent_events: MarketShareOfficialMovementEvent[];
  trend: Array<{
    as_of_ms: number;
    bithumb_share_pct: number;
    status: string;
  }>;
  data_quality: {
    source?: string;
    sample_interval_sec?: number;
    event_threshold_pp?: number;
    release_confirm_samples?: number;
    baseline_strategy?: string;
    restore_max_age_sec?: number;
    max_observation_gap_sec?: number;
    notice_poll_sec?: number;
  };
}

export interface MarketShareOfficialTicker1mPoint {
  bucket_ms: number;
  observed_at_ms: number;
  status: string;
  total_volume_krw: number;
  bithumb_volume_krw: number;
  bithumb_share_pct: number;
  asset_row_count: number;
}

export interface MarketShareOfficialAlignedHistoryResponse {
  contract_version: "OFFICIAL_ALIGNED_1H_HISTORY_V1" | string;
  status: "LIVE" | "PARTIAL" | "UNAVAILABLE" | string;
  source: "OFFICIAL_DOMESTIC_ALIGNED_1H_CANDLES_24H" | string;
  interval_sec: 3600 | number;
  from_ms: number;
  to_ms: number;
  expected_point_count: number;
  observed_point_count: number;
  missing_point_count: number;
  items: MarketShareOfficialTicker1mPoint[];
}

export interface MarketShareOfficialTickerAssetExpiration {
  asset: string;
  status: "INFERRED" | "UNAVAILABLE_SOURCE_SKEW" | string;
  ticker_bithumb_net_change_krw: number;
  ticker_competitor_net_change_krw: number;
  incoming_bithumb_volume_krw: number;
  incoming_competitor_volume_krw: number;
  estimated_expiring_bithumb_krw: number | null;
  estimated_expiring_competitor_krw: number | null;
}

export interface MarketShareOfficialTickerExchangeExpiration {
  exchange: string;
  status: "INFERRED" | "UNAVAILABLE_SOURCE_SKEW" | string;
  ticker_net_change_krw: number;
  incoming_volume_krw: number;
  estimated_expiring_volume_krw: number | null;
}

/** Aligned, completed one-hour candle trend. This is not a one-minute signal. */
export interface MarketShareOfficialAligned1hState {
  version: "OFFICIAL_ALIGNED_1H_CANDLE_TREND_V1" | string;
  generated_at_ms: number;
  interval_sec: number;
  status: "LIVE" | "WARMING" | "GAP" | "UNAVAILABLE" | string;
  reason: string;
  previous: MarketShareOfficialTicker1mPoint | null;
  current: MarketShareOfficialTicker1mPoint | null;
  share_delta_pp: number | null;
  domestic_volume_delta_krw: number | null;
  bithumb_volume_delta_krw: number | null;
  drivers: MarketShareOfficialMovementDriver[];
  exchange_drivers: MarketShareOfficialMovementExchangeDriver[];
  trend: MarketShareOfficialTicker1mPoint[];
  expiration_estimate: {
    status: "INFERRED" | "PARTIAL" | "UNAVAILABLE" | string;
    reason: string;
    source: "INFERRED_FROM_ALIGNED_1H_CANDLE_DELTA_AND_WS_INCOMING" | string;
    interval_start_ms: number;
    interval_end_ms: number;
    required_sec: number;
    coverage_sec: number;
    coverage_status: string;
    totals: {
      incoming_bithumb_volume_krw: number;
      incoming_competitor_volume_krw: number;
      estimated_expiring_bithumb_krw: number | null;
      estimated_expiring_competitor_krw: number | null;
      estimated_expiring_share_pct: number | null;
    };
    assets: MarketShareOfficialTickerAssetExpiration[];
    exchanges: MarketShareOfficialTickerExchangeExpiration[];
  };
  data_quality: {
    source?: string;
    sampling?: string;
    sample_count?: number;
    asset_details_ready?: boolean;
    asset_details_reason?: string;
    previous_asset_row_count?: number;
    current_asset_row_count?: number;
  };
}

/** @deprecated Historical API/type name; the payload contains aligned 1h candles. */
export type MarketShareOfficialTicker1mState =
  MarketShareOfficialAligned1hState;

/** Exact selected-hour comparison used by the interactive official trend. */
export interface MarketShareOfficialAlignedContribution {
  window_sec: number;
  status: "LIVE" | "WARMING" | "GAP" | "UNAVAILABLE" | string;
  reason: string;
  previous: MarketShareOfficialTicker1mPoint | null;
  current: MarketShareOfficialTicker1mPoint | null;
  share_delta_pp: number | null;
  domestic_volume_delta_krw: number | null;
  bithumb_volume_delta_krw: number | null;
  asset_drivers: MarketShareOfficialMovementDriver[];
  exchange_drivers: MarketShareOfficialMovementExchangeDriver[];
  observed_sample_count: number;
  expected_sample_count: number;
  missing_sample_count: number;
  endpoint_comparison_available: boolean;
}

export interface MarketShareOfficialBinanceAsset24h {
  asset: string;
  market: string;
  quote_asset: "USDT";
  /** Authoritative rolling 24-hour quote turnover reported by Binance. */
  quote_volume_usdt: number;
  last_price_usdt: number;
  best_bid_price_usdt: number;
  best_bid_quantity: number;
  best_ask_price_usdt: number;
  best_ask_quantity: number;
  /** Estimated KRW value; null when a valid USDT/KRW conversion is unavailable. */
  estimated_volume_krw: number | null;
  /** Estimated KRW price; null when a valid USDT/KRW conversion is unavailable. */
  estimated_last_price_krw: number | null;
  /** Bithumb USDT/KRW-converted Binance best bid and ask. */
  estimated_best_bid_krw: number | null;
  estimated_best_ask_krw: number | null;
  premium_identity_status:
    | "VERIFIED_EXACT_MARKETS"
    | "SYMBOL_MATCH_UNVERIFIED"
    | "EXACT_MARKET_PROOF_MISSING"
    | "CONTRACT_IDENTITY_CONFLICT"
    | "BITHUMB_KRW_MARKET_UNAVAILABLE"
    | "AMBIGUOUS_BITHUMB_KRW_MARKET";
  premium_identity_verified: boolean;
  premium_bithumb_market?: string;
  /** Exact LIVE midpoint for premium_bithumb_market; never an asset-level median. */
  premium_bithumb_mid_krw: number | null;
  /** Server-derived Bithumb premium against estimated_last_price_krw. */
  premium_bps: number | null;
  premium_status:
    | "LIVE"
    | "STALE_BINANCE_SUMMARY"
    | "STALE_BINANCE_CLOSE_TIME"
    | "IDENTITY_NOT_ELIGIBLE"
    | "BITHUMB_PRICE_UNAVAILABLE"
    | "BITHUMB_PRICE_QUARANTINED";
  open_time_ms: number;
  close_time_ms: number;
}

export interface MarketShareOfficialBinance24h {
  status: "LIVE" | "STALE" | "PARTIAL" | "UNAVAILABLE";
  source: "BINANCE_SPOT_OFFICIAL_24H_TICKER_USDT";
  quote_asset: "USDT";
  window_sec: 86400;
  as_of: number;
  expected_market_count: number;
  ready_market_count: number;
  /** Lower bound when status is PARTIAL. */
  observed_quote_volume_usdt: number;
  /** Exact total only when the expected market set is complete. */
  total_quote_volume_usdt: number | null;
  estimated_total_volume_krw: number | null;
  usdt_krw_rate: number | null;
  conversion_as_of: number | null;
  conversion_status: "RESOLVED" | "UNRESOLVED";
  assets: MarketShareOfficialBinanceAsset24h[];
  last_error: string | null;
}

export interface MarketShareMomentum {
  window: string;
  duration_sec: number;
  share_pct: number | null;
  volume_krw: number;
  volume_per_sec_krw: number;
  market_volume_per_sec_krw: number;
  rate_denominator_sec: number;
  status: string;
  ws_status?: "LIVE" | "DEGRADED";
  coverage_status?: "CONFIRMED" | "PROVISIONAL" | "PARTIAL" | "UNAVAILABLE";
  coverage_sec: number;
  gap_seconds?: number;
  data_source?: MarketShareDataSource;
  window_complete: boolean;
}

export interface MarketShareRollingAnomaly {
  status: string;
  median_pp: number | null;
  mad_pp: number | null;
  robust_z: number | null;
}

export interface MarketShareRollingHorizon {
  window: string;
  duration_sec: number;
  status: string;
  rolling_24h_share_pct: number | null;
  previous_rolling_24h_share_pct: number | null;
  rolling_24h_delta_pp: number | null;
  recent_flow_share_pct: number | null;
  expiring_flow_share_pct: number | null;
  recent_flow_volume_krw?: number | null;
  recent_bithumb_volume_krw?: number | null;
  recent_competitor_volume_krw?: number | null;
  expiring_flow_volume_krw?: number | null;
  expiring_bithumb_volume_krw?: number | null;
  expiring_competitor_volume_krw?: number | null;
  replacement_gap_pp: number | null;
  incoming_effect_pp: number | null;
  expiring_effect_pp: number | null;
  total_effect_pp: number | null;
  reconciliation_error_pp: number | null;
  anomaly?: MarketShareRollingAnomaly;
  coverage?: Record<
    string,
    {
      status: string;
      coverage_sec: number;
      required_sec: number;
      gap_seconds: number;
    }
  >;
}

export interface MarketShareRollingDriver {
  base?: string;
  exchange?: string;
  incoming_effect_pp: number;
  expiring_effect_pp: number;
  contribution_pp: number;
  recent_bithumb_volume_krw: number;
  recent_competitor_volume_krw: number;
  expiring_bithumb_volume_krw: number;
  expiring_competitor_volume_krw: number;
  previous_volume_krw: number;
  recent_volume_krw: number;
  recent_vs_expiring_volume_change_pct: number | null;
  reason: string;
}

export interface MarketShareRollingObservedFlowDriver {
  base?: string;
  exchange?: string;
  recent_bithumb_volume_krw: number | null;
  recent_competitor_volume_krw: number | null;
  recent_volume_krw: number | null;
  recent_flow_share_pct: number | null;
  expiring_bithumb_volume_krw: number | null;
  expiring_competitor_volume_krw: number | null;
  expiring_volume_krw: number | null;
  expiring_flow_share_pct: number | null;
  replacement_gap_pp: number | null;
  recent_vs_expiring_volume_change_pct: number | null;
  reason: string;
}

export interface MarketShareRollingNoticeBriefLine {
  label: string;
  value: string;
}

export interface MarketShareRollingNoticeSchedule {
  kind: string;
  start_ts_ms: number;
  end_ts_ms: number;
  timezone?: string;
  evidence?: string;
  precision?: "SECOND" | "DAY" | "RECURRING" | string;
}

export interface MarketShareRollingNoticeBrief {
  kind: string;
  detail_available: boolean;
  analysis_status: string;
  period_start_ts_ms: number | null;
  period_end_ts_ms: number | null;
  period_precision?: "SECOND" | "DAY" | "RECURRING" | string;
  target: string | null;
  reward: string | null;
  operation: string | null;
  assets?: string[];
  schedules?: MarketShareRollingNoticeSchedule[];
  timeline_schedules?: MarketShareRollingNoticeSchedule[];
  lines: MarketShareRollingNoticeBriefLine[];
}

export interface MarketShareNoticeImageConsistency {
  status: "MATCHED" | "PARTIAL" | "CONFLICT" | "UNVERIFIED" | string;
  semantic_types: string[];
  matched_signals: string[];
  conflict_signals: string[];
  unverified_signals?: string[];
  reason: string;
}

export interface MarketShareRollingNotice {
  exchange: string;
  notice_id: string;
  title: string;
  url: string;
  category: string;
  published_ts_ms: number;
  updated_ts_ms?: number;
  anchor_ts_ms: number | null;
  assets: string[];
  relation: string;
  related: boolean;
  related_label: "관련 있음" | "관련 없음";
  relation_reason: string;
  timing_label: string;
  content_basis: string;
  content_priority_score: number;
  priority_label: string;
  content_related: boolean;
  time_related: boolean;
  content_schedule_related: boolean;
  schedule_active_now: boolean;
  schedule_related: boolean;
  market_context: Array<Record<string, unknown>>;
  image_consistency?: MarketShareNoticeImageConsistency;
  brief: MarketShareRollingNoticeBrief;
  causal_claim: false;
}

export interface MarketShareActiveNoticeNetworkNotice {
  id: string;
  exchange: string;
  title: string;
  url: string;
  category: string;
  assets: string[];
  start_ms: number;
  end_ms: number | null;
}

export interface MarketShareActiveNoticeNetworkAsset {
  asset: string;
  contribution_pp: number | null;
  current_domestic_volume_krw: number;
  current_bithumb_volume_krw: number;
  current_bithumb_share_pct: number | null;
  current_competitor_volume_krw: number;
  bithumb_volume_change_krw: number | null;
  competitor_volume_change_krw: number | null;
}

export interface MarketShareActiveNoticeNetworkEdge {
  asset: string;
  exchange: string;
  current_volume_krw: number;
  current_domestic_share_pct: number | null;
}

export interface MarketShareActiveNoticeNetwork {
  version: "ACTIVE_NOTICE_SHARE_NETWORK_V1";
  as_of_ms: number;
  window_sec: number;
  status: string;
  reason: string;
  previous_bithumb_share_pct: number | null;
  current_bithumb_share_pct: number | null;
  share_delta_pp: number | null;
  active_notices: MarketShareActiveNoticeNetworkNotice[];
  assets: MarketShareActiveNoticeNetworkAsset[];
  edges: MarketShareActiveNoticeNetworkEdge[];
  causal_claim: false;
}

export interface MarketShareRollingMicrostructure {
  asset: string;
  contribution_pp: number | null;
  status: string | null;
  diagnosis: string;
  bithumb_depth_krw: number | null;
  competitor_depth_krw: number | null;
  bithumb_liquidity_capture_pct: number | null;
  spread_gap_bps: number | null;
  buy_slippage_gap_bps: number | null;
  bithumb_buy_price_gap_bps: number | null;
  bithumb_orderbook_max_age_sec: number | null;
  competitor_orderbook_max_age_sec: number | null;
  price_validation_status: string | null;
}

export interface MarketShareRollingBridge {
  window: string;
  duration_sec?: number;
  status: string;
  previous_share_pct: number | null;
  current_share_pct: number | null;
  incoming_effect_pp: number | null;
  expiring_effect_pp: number | null;
  total_effect_pp: number | null;
  reconciliation_error_pp: number | null;
  asset_drivers: MarketShareRollingDriver[];
  exchange_drivers: MarketShareRollingDriver[];
  observed_asset_drivers?: MarketShareRollingObservedFlowDriver[];
  observed_exchange_drivers?: MarketShareRollingObservedFlowDriver[];
  microstructure?: MarketShareRollingMicrostructure[];
}

export interface MarketShareRollingBriefing {
  status: string;
  window: string | null;
  text: string;
}

export interface MarketShareHermesQuestionAnswer {
  answer: string;
  key_points: string[];
  caveat: string;
  source:
    "HERMES" | "HERMES_SOL_FALLBACK" | "SERVER_BRIEFING_FALLBACK" | string;
  model?: string;
  as_of_ms: number;
}

export interface GroundedMarketQuestionWindow {
  start_ms: number;
  end_ms: number;
  domestic_volume_krw: number | null;
  bithumb_volume_krw: number | null;
  bithumb_share_pct: number | null;
  domestic_trade_count: number | null;
  bithumb_trade_count: number | null;
}

export interface GroundedMarketQuestionIntervalCoverage {
  status: string;
  coverage_sec: number;
  required_sec: number;
  gap_seconds: number;
  window_complete: boolean;
}

export interface GroundedMarketQuestionAssetMetrics {
  asset: string;
  canonical_asset_id: string;
  contract_identity_status: string;
  contract_identity_verified: boolean;
  observed_exchanges: string[];
  current_bithumb_capture_pct: number | null;
  previous_bithumb_capture_pct: number | null;
  bithumb_capture_change_pp: number | null;
  current_share_within_bithumb_pct: number | null;
  previous_share_within_bithumb_pct: number | null;
  share_within_bithumb_change_pp: number | null;
  current_total_share_contribution_pct: number | null;
  previous_total_share_contribution_pct: number | null;
  total_share_contribution_change_pp: number | null;
  change_attribution_pp: number | null;
  current_aggressor_buy_pct: number | null;
  previous_aggressor_buy_pct: number | null;
  aggressor_buy_change_pp: number | null;
  current_directional_coverage_pct: number | null;
  previous_directional_coverage_pct: number | null;
  current_bithumb_aggressor_buy_pct: number | null;
  previous_bithumb_aggressor_buy_pct: number | null;
  bithumb_aggressor_buy_change_pp: number | null;
  current_bithumb_directional_coverage_pct: number | null;
  previous_bithumb_directional_coverage_pct: number | null;
}

export interface GroundedMarketQuestionAssetDriver {
  asset: string;
  contribution_pp: number;
  bithumb_volume_change_krw: number;
  competitor_volume_change_krw: number;
}

export interface GroundedMarketQuestionExchangeDriver {
  exchange: string;
  contribution_pp: number;
  volume_change_krw: number;
}

export interface GroundedMarketQuestionDailyEndpointPoint {
  bucket_ms: number;
  observed_at_ms: number;
  status: string;
  reason?: string;
  exact_endpoint_available: boolean;
  asset_details_available: boolean;
  total_volume_krw: number | null;
  bithumb_volume_krw: number | null;
  bithumb_share_pct: number | null;
  asset_row_count: number;
}

export interface GroundedMarketQuestionDailyEndpointDriver {
  rank: number;
  asset: string;
  contribution_pp: number;
  bithumb_volume_change_krw: number;
  competitor_volume_change_krw: number;
  previous_bithumb_volume_krw: number;
  current_bithumb_volume_krw: number;
  previous_competitor_volume_krw: number;
  current_competitor_volume_krw: number;
  previous_close_price_krw: number | null;
  current_close_price_krw: number | null;
  close_price_change_krw: number | null;
  close_price_change_pct: number | null;
}

export interface GroundedMarketQuestionDailyEndpointTransition {
  from_bucket_ms: number;
  to_bucket_ms: number;
  window_sec: number;
  status: string;
  reason?: string;
  endpoint_comparison_available: boolean;
  asset_attribution_available: boolean;
  path_complete: boolean;
  expected_hourly_point_count: number;
  observed_hourly_point_count: number;
  missing_bucket_ms: number[];
  share_delta_pp: number | null;
  domestic_volume_delta_krw: number | null;
  bithumb_volume_delta_krw: number | null;
  positive_drivers: GroundedMarketQuestionDailyEndpointDriver[];
  negative_drivers: GroundedMarketQuestionDailyEndpointDriver[];
  exchange_drivers: GroundedMarketQuestionExchangeDriver[];
}

export interface GroundedMarketQuestionDailyPricePoint {
  bucket_ms: number;
  close_price_krw: number | null;
}

export interface GroundedMarketQuestionDailyPricePath {
  asset: string;
  status: string;
  direction: string;
  change_pct: number | null;
  points: GroundedMarketQuestionDailyPricePoint[];
}

export interface GroundedMarketQuestionDailyRecentInterval {
  status: string;
  reason?: string;
  source: string;
  window_sec: number;
  as_of_ms: number;
  previous: GroundedMarketQuestionWindow;
  current: GroundedMarketQuestionWindow;
  share_delta_pp: number | null;
  domestic_volume_delta_krw: number | null;
  bithumb_volume_delta_krw: number | null;
  positive_drivers: GroundedMarketQuestionAssetDriver[];
  negative_drivers: GroundedMarketQuestionAssetDriver[];
  driver_contribution_sum_pp: number | null;
  driver_residual_pp: number | null;
  current_coverage: GroundedMarketQuestionIntervalCoverage;
  previous_coverage: GroundedMarketQuestionIntervalCoverage;
}

export interface GroundedMarketQuestionDailyEndpointSeries {
  contract_version: string;
  status: string;
  reason?: string;
  points: GroundedMarketQuestionDailyEndpointPoint[];
  transitions: GroundedMarketQuestionDailyEndpointTransition[];
  cumulative: GroundedMarketQuestionDailyEndpointTransition;
  price_paths: GroundedMarketQuestionDailyPricePath[];
  recent_interval?: GroundedMarketQuestionDailyRecentInterval;
  path_complete: boolean;
  expected_hourly_point_count: number;
  observed_hourly_point_count: number;
  missing_bucket_ms: number[];
  non_live_bucket_ms: number[];
  included_exchanges: string[];
  excluded_exchanges: Record<string, string>;
}

export interface GroundedMarketQuestionTargetExchange {
  exchange: string;
  previous_volume_krw: number | null;
  current_volume_krw: number | null;
  volume_delta_krw: number | null;
  previous_share_pct: number | null;
  current_share_pct: number | null;
  share_delta_pp: number | null;
  previous_trade_count: number | null;
  current_trade_count: number | null;
  official_ticker_status?: string;
  official_24h_volume_krw: number | null;
  official_24h_share_pct: number | null;
}

export interface GroundedMarketQuestionRealtimeAssetContext {
  asset: string;
  domestic_volume_krw: number;
  bithumb_volume_krw: number;
  bithumb_share_pct: number | null;
  bithumb_buy_pct: number | null;
  top_competitor: string;
  top_competitor_volume_krw: number;
  orderbook?: MarketShareRealtimeOrderbook;
}

export interface GroundedMarketQuestionRealtimeContext {
  as_of_ms: number;
  age_sec: number | null;
  status: string;
  signal_status: string;
  reason: string;
  direction: string;
  primary_window_sec: number;
  share_delta_pp: number | null;
  windows: MarketShareRealtimeFlowWindow[];
  asset?: GroundedMarketQuestionRealtimeAssetContext;
}

export interface GroundedMarketQuestionOrderbookContext {
  asset: string;
  source: string;
  status: string;
  as_of_ms: number;
  age_sec: number | null;
  imbalance: number | null;
  spread_bps: number | null;
  bid_depth_krw: number | null;
  ask_depth_krw: number | null;
  buy_slippage_bps: number | null;
  sell_slippage_bps: number | null;
}

export interface GroundedMarketQuestionNoticeFact {
  exchange: string;
  notice_id: string;
  title: string;
  anchor_ms: number;
  assets: string[];
  relation: string;
  period?: {
    kind: string;
    start_ms: number;
    end_ms: number;
    timezone?: string;
    precision?: string;
    relation: string;
    applies_to_question_window: boolean;
  };
  causal_claim: false;
}

export interface GroundedMarketQuestionAuxiliaryFacts {
  requested: string[];
  realtime?: GroundedMarketQuestionRealtimeContext;
  orderbook?: GroundedMarketQuestionOrderbookContext;
  notices?: {
    window_start_ms: number;
    window_end_ms: number;
    source_count: number;
    healthy_source_count: number;
    matches: GroundedMarketQuestionNoticeFact[];
    causal_claim: false;
  };
}

export interface GroundedMarketQuestionPathSummary {
  status: string;
  reason: string;
  point_count: number;
  expected_point_count: number;
  start_ms: number;
  end_ms: number;
  min_share_pct: number | null;
  min_share_at_ms: number;
  max_share_pct: number | null;
  max_share_at_ms: number;
  largest_step_delta_pp: number | null;
  largest_step_start_ms: number;
  largest_step_end_ms: number;
  largest_step_abs_share_pct: number | null;
  rising_step_count: number;
  falling_step_count: number;
  flat_step_count: number;
  direction: string;
  dominant_direction_share_pct: number | null;
  observed_segments: Array<{
    start_ms: number;
    end_ms: number;
    point_count: number;
    start_share_pct: number;
    end_share_pct: number;
    share_delta_pp: number;
    min_share_pct: number;
    min_share_at_ms: number;
    max_share_pct: number;
    max_share_at_ms: number;
    largest_step_delta_pp: number;
    largest_step_start_ms: number;
    largest_step_end_ms: number;
  }>;
}

export type GroundedMarketQuestionFulfillmentStatus =
  | "FULFILLED"
  | "PARTIAL"
  | "UNSUPPORTED"
  | "NEEDS_CLARIFICATION";

export type GroundedMarketQuestionValueState =
  | "CONFIRMED_VALUE"
  | "CONFIRMED_ZERO"
  | "UNKNOWN"
  | "NOT_APPLICABLE";

export type GroundedMarketQuestionIdentityStatus =
  | "CONFIRMED"
  | "UNAVAILABLE"
  | "NOT_APPLICABLE";

export interface GroundedMarketQuestionEvidenceBundleRevision {
  status: GroundedMarketQuestionIdentityStatus;
  id?: string;
  analysis_context_id?: string;
  input_revision?: string;
  analysis_version?: number;
  aggregation_version?: number;
  db_snapshot_identity?: string;
  memory_generation?: string;
  reason?: string;
}

export interface GroundedMarketQuestionEvidenceBundle {
  contract_version: string;
  bundle_id: string;
  catalog_version: string;
  created_at_ms: number;
  question_contract: {
    question_digest: string;
    metric: string;
    comparison: string;
    target_exchange?: string;
    target_asset?: string;
    requested_explanation: string[];
    requirements: Array<{
      id: string;
      kind: string;
      required: boolean;
      status:
        | "SATISFIED"
        | "UNAVAILABLE"
        | "UNSUPPORTED"
        | "NEEDS_CLARIFICATION";
      fact_ids: string[];
      reason?: string;
    }>;
  };
  fulfillment: {
    status: GroundedMarketQuestionFulfillmentStatus;
    requirements: Array<{
      id: string;
      kind: string;
      required: boolean;
      status:
        | "SATISFIED"
        | "UNAVAILABLE"
        | "UNSUPPORTED"
        | "NEEDS_CLARIFICATION";
      fact_ids: string[];
      reason?: string;
    }>;
  };
  time: {
    basis: string;
    aggregation_semantics: string;
    requested_window_sec?: number;
    actual_window_sec?: number;
    alignment_delta_ms?: number;
    requested: {
      start_ms?: number;
      end_ms?: number;
      as_of_ms?: number;
    };
    actual: {
      start_ms?: number;
      end_ms?: number;
      as_of_ms?: number;
      observed_at_ms?: number;
    };
    timezone: string;
    status: string;
    reason?: string;
  };
  scope: {
    requested_market_scope: string;
    actual_market_scope?: string;
    requested_asset_set?: string;
    actual_asset_set?: string;
    requested_exchanges: string[] | null;
    included_exchanges: string[] | null;
    excluded_exchanges: Array<{ exchange: string; reason: string }> | null;
    same_asset_set: boolean;
    status: "EXACT" | "SUBSTITUTED_PARTIAL" | "UNAVAILABLE";
    reason?: string;
  };
  coverage: {
    basis: string;
    status: GroundedMarketQuestionIdentityStatus;
    confirmed_units?: number;
    required_units?: number;
    gap_units?: number;
    unit?: string;
    reason?: string;
  };
  denominators: Array<{
    id: string;
    meaning: string;
    scope: string;
    value: {
      state: GroundedMarketQuestionValueState;
      decimal?: string;
      unit?: string;
    };
    registry_id?: string;
    source_id: string;
  }>;
  sources: Array<{
    id: string;
    evidence_layer: string;
    tool?: string;
    status: GroundedMarketQuestionIdentityStatus;
    observed_at_ms?: number;
    reason?: string;
  }>;
  registry: {
    status: GroundedMarketQuestionIdentityStatus;
    identity?: string;
    as_of_ms?: number;
    source?: string;
    reason?: string;
  };
  revision: GroundedMarketQuestionEvidenceBundleRevision;
  facts: Array<{
    id: string;
    metric: string;
    role: string;
    subject: {
      market_scope?: string;
      exchange?: string;
      asset?: string;
    };
    value: {
      state: GroundedMarketQuestionValueState;
      decimal?: string;
      unit?: string;
    };
    time: {
      role: string;
      start_ms?: number;
      end_ms?: number;
      as_of_ms?: number;
    };
    denominator_ids: string[];
    source_id: string;
    aggregation_policy: string;
    quality: string;
    relation?: { operation: string; operand_fact_ids: string[] };
    text?: string;
  }>;
  contribution: {
    status: string;
    method?: string;
    method_version?: string;
    component_fact_ids: string[];
    other_fact_id?: string;
    residual_fact_id?: string;
    residual_kind?: string;
    reason?: string;
  };
  notice: {
    status: "NOT_QUERIED" | "FAILED" | "NOT_FOUND" | "FOUND";
    source_count: number;
    healthy_source_count: number;
    fact_ids: string[];
    causal_claim: boolean;
    reason?: string;
  };
  replay: {
    recalculation_mode: string;
    retained_bundle_status: GroundedMarketQuestionIdentityStatus;
    original_as_known_status: GroundedMarketQuestionIdentityStatus;
    reason?: string;
  };
  cache: { calculation_key: string; explanation_key?: string };
  warnings: string[];
}

export interface GroundedMarketQuestionConversationScope {
  contract: string;
  token: string;
  query: GroundedMarketQuestionResponse["query"];
  evidence_bundle_id: string;
  revision: GroundedMarketQuestionEvidenceBundleRevision;
  created_at_ms: number;
  fulfillment_status: GroundedMarketQuestionFulfillmentStatus;
  as_of_ms: number;
}

export type GroundedMarketQuestionStageStatus =
  | "READY"
  | "ACCEPTED"
  | "PARTIAL"
  | "FAILED"
  | "TIMEOUT"
  | "CANCELED"
  | "UNVERIFIED";

export interface GroundedMarketQuestionOrchestration {
  mode: string;
  overall_status: string;
  quantitative_status: string;
  explanation_status: string;
  validation_status: "SUPPORTED" | "CONFLICT" | "UNDETERMINED";
  blocks: Array<{
    index: number;
    text?: string;
    status: "SUPPORTED" | "CONFLICT" | "UNDETERMINED";
    reason?: string;
    claim_refs: string[];
  }>;
  calls: {
    plan: number;
    explanation: number;
    repair: number;
    total: number;
    max_total: number;
  };
  timing_ms: {
    request_started: number;
    planning_completed?: number;
    quantitative_ready?: number;
    explanation_started?: number;
    explanation_completed?: number;
    validation_completed?: number;
    queue_duration?: number;
    plan_duration?: number;
    retrieval_calculation_duration?: number;
    serialization_duration?: number;
    model_duration?: number;
    validation_duration?: number;
    total?: number;
    evidence_bytes?: number;
  };
}

export interface GroundedMarketQuestionStageEvent {
  sequence: number;
  stage: "QUANTITATIVE_READY" | "EXPLANATION_READY" | "TERMINAL";
  status: GroundedMarketQuestionStageStatus;
  emitted_at_ms: number;
  response: GroundedMarketQuestionResponse;
}

export interface GroundedMarketQuestionResponse {
  request_id?: string;
  contract: "GROUNDED_MARKET_QUESTION_V2";
  query: {
    intent: string;
    time_value: number;
    time_unit: string;
    comparison: string;
    metric: string;
    exchange?: string;
    asset?: string;
    market_scope: string;
    end_policy: string;
    target_date?: string;
    target_dates?: string[];
    target_start_ms?: number;
    target_end_ms?: number;
    requested_explanation: string[];
    peer_exchange?: string;
    requested_path_sec?: number;
    analysis_depth?: string;
    question_parts?: string[];
  };
  facts: {
    evidence_layer: string;
    window_sec: number;
    previous: GroundedMarketQuestionWindow | null;
    current: GroundedMarketQuestionWindow | null;
    share_delta_pp: number | null;
    share_relative_change_pct: number | null;
    domestic_volume_delta_krw: number | null;
    domestic_volume_change_pct: number | null;
    bithumb_volume_delta_krw: number | null;
    bithumb_volume_change_pct: number | null;
    path_summary?: GroundedMarketQuestionPathSummary;
    price_relationship_status: string;
    price_relationship_reason: string;
    price_relationships: Array<{
      rank: number;
      asset: string;
      pair_count: number;
      correlation: number;
      direction: string;
    }>;
    asset_metrics?: GroundedMarketQuestionAssetMetrics;
    drivers: GroundedMarketQuestionAssetDriver[];
    exchange_drivers: GroundedMarketQuestionExchangeDriver[];
    driver_contribution_sum_pp: number | null;
    driver_residual_pp: number | null;
    target_exchange?: GroundedMarketQuestionTargetExchange;
    daily_endpoint_series?: GroundedMarketQuestionDailyEndpointSeries;
    auxiliary?: GroundedMarketQuestionAuxiliaryFacts;
  };
  quality: {
    status: string;
    endpoint_comparison_available: boolean;
    path_complete: boolean;
    current_coverage: {
      coverage_sec: number;
      required_sec: number;
      gap_seconds: number;
      window_complete: boolean;
    };
    previous_coverage: {
      coverage_sec: number;
      required_sec: number;
      gap_seconds: number;
      window_complete: boolean;
    };
    warnings: string[];
  };
  prose: {
    answer: string;
    key_points: string[];
    caveat: string;
  };
  provider: {
    source: string;
    model?: string;
    fallback_used: boolean;
    as_of_ms: number;
  };
  provenance: {
    analysis_version: number;
    aggregation_version: number;
    answer_policy_version: string;
    retrieval_strategy: string;
    persistent_store: string;
    model_generated_sql: boolean;
    raw_database_rows_sent_to_model: boolean;
    official_bucket_ms: number;
    comparison_end_ms: number;
    as_of_kind: string;
    display_timezone: string;
  };
  evidence_bundle?: GroundedMarketQuestionEvidenceBundle;
  orchestration?: GroundedMarketQuestionOrchestration;
  conversation_scope?: GroundedMarketQuestionConversationScope;
}

export type NoticeQuestionPersona =
  | "AUTO"
  | "FACT_RESEARCHER"
  | "VOLUME_ANALYST"
  | "EVENT_BRIEFER"
  | "BITHUMB_STYLE_WRITER";

export interface MarketShareHermesNoticeQuestionAnswer {
  answer: string;
  key_points: string[];
  caveat: string;
  source: string;
  model?: string;
  as_of_ms: number;
  persona: NoticeQuestionPersona;
  sources: Array<{
    exchange: string;
    notice_id: string;
    title: string;
    url: string;
    published_ts_ms: number;
  }>;
}

export interface MarketShareClosedMinuteAssetChange {
  base: string;
  previous_bithumb_volume_krw: number;
  current_bithumb_volume_krw: number;
  bithumb_volume_change_krw: number;
  previous_competitor_volume_krw: number;
  current_competitor_volume_krw: number;
  competitor_volume_change_krw: number;
  previous_domestic_volume_krw: number;
  current_domestic_volume_krw: number;
  domestic_volume_change_krw: number;
  reason: string;
}

export interface MarketShareClosedMinuteComparison {
  window: string;
  duration_sec: number;
  status: string;
  closed_at_ms: number;
  previous_share_pct: number | null;
  current_share_pct: number | null;
  share_delta_pp: number | null;
  previous_bithumb_volume_krw: number;
  previous_domestic_volume_krw: number;
  current_bithumb_volume_krw: number;
  current_domestic_volume_krw: number;
  coverage: {
    previous: Record<string, unknown>;
    current: Record<string, unknown>;
  };
  asset_changes: MarketShareClosedMinuteAssetChange[];
}

export interface MarketShareRolling24h {
  version: string;
  market_scope: string;
  as_of_ms: number;
  window_sec: 86400;
  rolling_24h: {
    current_share_pct: number | null;
    bithumb_volume_krw: number | null;
    domestic_volume_krw: number | null;
    status: string;
    coverage: {
      status: string;
      coverage_sec: number;
      required_sec: number;
      gap_seconds: number;
      websocket_coverage_sec?: number;
      official_rest_coverage_sec?: number;
      official_rest_exact_coverage_sec?: number;
      official_rest_estimated_coverage_sec?: number;
      official_rest_status?: string;
      official_rest_used?: boolean;
      recovered_gap_count?: number;
      recovered_gap_ids?: number[];
      recovery_mode?: string;
      official_reconciled?: boolean;
    };
  };
  horizons: MarketShareRollingHorizon[];
  closed_minute_comparisons?: {
    source: "WEBSOCKET_TRADE_CLOSED_MINUTE_GROUPS";
    bar_sec: 60;
    as_of_ms: number | null;
    comparisons: MarketShareClosedMinuteComparison[];
  };
  bridge: MarketShareRollingBridge;
  bridges?: MarketShareRollingBridge[];
  movement: {
    movement_id: string | null;
    direction: "UP" | "DOWN" | "STABLE";
    state: "STARTING" | "EXPANDING" | "EASING" | "RECOVERING" | "STABLE";
    current_status: string;
    started_at_ms: number | null;
    last_updated_at_ms: number;
    duration_seconds: number;
    share_at_start_pct: number | null;
    current_share_pct: number | null;
    extreme_share_pct: number | null;
    total_change_pp: number | null;
  };
  trend: Array<{
    as_of_ms: number;
    rolling_24h_share_pct: number | null;
    recent_flow_share_pct?: number | null;
    expiring_flow_share_pct?: number | null;
    incoming_effect_pp?: number | null;
    expiring_effect_pp?: number | null;
    quality_status?: string;
  }>;
  briefing: MarketShareRollingBriefing;
  data_quality: {
    status: string;
    current_coverage_sec: number;
    current_required_sec: number;
    retained_history_required_sec: number;
    zero_filled: false;
    movement_samples?: number;
    baseline_status?: string;
    movement_status?: string;
  };
  evidence: {
    domestic_notices: MarketShareRollingNotice[];
    domestic_notice_counts?: Record<string, number>;
    domestic_notice_available_counts?: Record<string, number>;
    domestic_notice_sources?: Record<
      string,
      {
        status: string;
        fetched_count: number;
        history_status: string;
        last_success_ts: number;
        error?: string | null;
      }
    >;
    causal_claim: false;
    notice_window_start_ms?: number | null;
  };
}

export interface MarketShareExchange {
  official_replacement_volume_krw?: number;
  trade_detail_incomplete?: boolean;
  exchange: string;
  volume_krw: number;
  share_pct: number | null;
  trade_count: number;
  trade_count_share_pct: number | null;
  trades_per_sec: number;
  average_trade_krw: number | null;
  buy_volume_krw: number;
  sell_volume_krw: number;
  aggressor_buy_pct: number | null;
  registered_market_count: number;
  registered_asset_count: number;
  listed_asset_domestic_volume_coverage_pct?: number | null;
  traded_market_count: number;
  unresolved_fx_count: number;
  hhi: number | null;
  leading_asset: string;
  leading_asset_share_pct: number | null;
  top_5_share_pct: number | null;
  top_10_share_pct: number | null;
}

export interface MarketShareQuote {
  quote: string;
  volume_krw: number;
  market_share_pct: number | null;
  bithumb_volume_krw: number;
  bithumb_capture_pct: number | null;
  registered_market_count: number;
  bithumb_market_count: number;
  traded_market_count: number;
  unresolved_fx_count: number;
  exchanges: string[];
}

export type MarketShareFindingKind =
  | "LISTING_GAP"
  | "CAPTURE_GAP"
  | "QUOTE_GAP"
  | "GLOBAL_INTEREST_GAP"
  | "MOMENTUM"
  | "NOTICE_CONTEXT"
  | "SHARE_LOSS_DRIVER"
  | "SHARE_GAIN_DRIVER"
  | "LIQUIDITY_GAP"
  | "SPREAD_GAP"
  | "SLIPPAGE_GAP"
  | "NOTICE_REACTION"
  | "EXCLUSIVE_LISTING_STRENGTH"
  | "UPBIT_LISTING_PRESSURE";

export type MarketShareEvidenceLevel = "MEASURED" | "DIAGNOSTIC" | "CONTEXTUAL";

export interface MarketShareFinding {
  kind: MarketShareFindingKind;
  title: string;
  detail: string;
  basis?: string[];
  asset?: string;
  quote?: string;
  value_krw?: number;
  value_pct?: number | null;
  value_bps?: number;
  confidence_pct?: number | null;
  evidence_level: MarketShareEvidenceLevel;
  evidence_label: string;
  claim_scope: string;
  causal_claim: false;
}

export interface MarketShareEvidenceLayer {
  level: MarketShareEvidenceLevel;
  label: string;
  description: string;
  claim_scope: string;
  causal_claim: false;
  items: MarketShareFinding[];
}

export type MarketShareAssetExchangeNetworkNodeKind = "ASSET" | "EXCHANGE";

export type MarketShareAssetExchangeNetworkVolumeDirection =
  "INCREASE" | "DECREASE" | "UNCHANGED";

export type MarketShareAssetExchangeNetworkShareEffect =
  "RAISES_BITHUMB_SHARE" | "LOWERS_BITHUMB_SHARE" | "NEUTRAL";

export interface MarketShareAssetExchangeNetworkNode {
  id: string;
  kind: MarketShareAssetExchangeNetworkNodeKind;
  label: string;
  current_volume_krw: number;
  previous_volume_krw: number;
  volume_change_krw: number;
  contribution_pp: number;
  connection_count: number;
}

export interface MarketShareAssetExchangeNetworkEdge {
  rank: number;
  id: string;
  source_id: string;
  target_id: string;
  asset: string;
  exchange: string;
  current_volume_krw: number;
  previous_volume_krw: number;
  volume_change_krw: number;
  volume_change_pct: number | null;
  contribution_pp: number;
  volume_direction: MarketShareAssetExchangeNetworkVolumeDirection;
  share_effect: MarketShareAssetExchangeNetworkShareEffect;
}

export interface MarketShareAssetExchangeNetwork {
  status: "CONFIRMED" | "UNAVAILABLE" | string;
  unavailable_reason?: string;
  basis: string;
  relationship: string;
  selection: string;
  total_asset_count: number;
  returned_asset_count: number;
  total_edge_count: number;
  returned_edge_count: number;
  nodes: MarketShareAssetExchangeNetworkNode[];
  edges: MarketShareAssetExchangeNetworkEdge[];
}

export interface MarketShareChangeAsset {
  asset: string;
  asset_identity?: MarketShareAssetIdentity;
  contribution_pp: number;
  bithumb_direct_effect_pp: number;
  competitor_effect_pp: number;
  domestic_market_size_effect_pp: number;
  current_domestic_volume_krw: number;
  previous_domestic_volume_krw: number;
  domestic_volume_change_krw: number;
  domestic_volume_change_pct: number | null;
  current_bithumb_volume_krw: number;
  previous_bithumb_volume_krw: number;
  bithumb_volume_change_krw: number;
  bithumb_volume_change_pct: number | null;
  current_upbit_volume_krw: number;
  previous_upbit_volume_krw: number;
  upbit_volume_change_krw: number;
  upbit_volume_change_pct: number | null;
  current_competitor_volume_krw: number;
  previous_competitor_volume_krw: number;
  competitor_volume_change_krw: number;
  current_bithumb_capture_pct: number | null;
  previous_bithumb_capture_pct: number | null;
  bithumb_capture_change_pp: number | null;
  current_domestic_share_pct: number | null;
  previous_domestic_share_pct: number | null;
  current_trade_count: number;
  previous_trade_count: number;
  /**
   * This coin's terms of MIDPOINT_MIX_CAPTURE_V1 (see change_attribution);
   * null when the window's split is not given.
   */
  mix_effect_pp?: number | null;
  capture_effect_pp?: number | null;
  contribution_method?: "NORMALIZED_SHARE_EFFECT" | string;
  contribution_note?: string;
}

export interface MarketShareChangeExchange {
  exchange: string;
  contribution_pp: number;
  current_volume_krw: number;
  previous_volume_krw: number;
  volume_change_krw: number;
  volume_change_pct: number | null;
  current_share_pct: number | null;
  previous_share_pct: number | null;
  share_change_pp: number | null;
}

export interface MarketShareChangeQuote {
  quote: string;
  contribution_pp: number;
  current_volume_krw: number;
  previous_volume_krw: number;
  volume_change_krw: number;
  volume_change_pct: number | null;
  current_bithumb_volume_krw: number;
  previous_bithumb_volume_krw: number;
  bithumb_volume_change_krw: number;
  current_bithumb_capture_pct: number | null;
  previous_bithumb_capture_pct: number | null;
}

export interface MarketShareExpertCheck {
  code: string;
  evidence_level: "DIAGNOSTIC";
  status: string;
  title: string;
  detail: string;
  basis: string[];
  evidence_label: string;
  value: number | null;
  unit: string;
  causal_claim: false;
}

export type MarketShareMajorAsset = MarketShareChangeAsset & {
  listing_segment: string;
  domestic_exchanges: string[];
};

export interface MarketShareListingStructureHeadline {
  structure: string;
  label: string;
  asset_count: number;
  current_domestic_volume_krw: number;
  previous_domestic_volume_krw: number;
  domestic_volume_change_krw: number;
  current_bithumb_volume_krw: number;
  previous_bithumb_volume_krw: number;
  bithumb_volume_change_krw: number;
  contribution_pp: number;
  leading_assets: MarketShareMajorAsset[];
}

export type MarketShareVolumeRelationship =
  "SHARE_UP_BOTH_VOLUMES_DOWN" | "SHARE_DOWN_BOTH_VOLUMES_UP" | "";

export interface MarketShareExpertBrief {
  status: string;
  comparison: string;
  primary_driver: string;
  measured_summary: Array<{ title: string; detail: string; basis?: string[] }>;
  diagnostic_summary: Array<{
    title: string;
    detail: string;
    basis?: string[];
  }>;
  contextual_summary: Array<{
    title: string;
    detail: string;
    basis?: string[];
  }>;
  market_comparison: Array<{
    key: string;
    label: string;
    previous_volume_krw: number | null;
    current_volume_krw: number;
    change_krw: number | null;
    change_pct: number | null;
  }>;
  share_bridge: Array<{
    key: string;
    label: string;
    value_pp: number | null;
    kind: "TOTAL" | "CHANGE";
  }>;
  exchange_comparison: MarketShareChangeExchange[];
  quote_comparison: MarketShareChangeQuote[];
  breadth: {
    asset_count: number;
    positive_asset_count: number;
    negative_asset_count: number;
    flat_asset_count: number;
    positive_contribution_pp: number;
    negative_contribution_pp: number;
    total_absolute_contribution_pp: number;
    top_1_absolute_contribution_share_pct: number | null;
    top_5_absolute_contribution_share_pct: number | null;
  };
  listing_segments: Array<{
    segment: string;
    label: string;
    asset_count: number;
    current_domestic_volume_krw: number;
    previous_domestic_volume_krw: number;
    current_bithumb_volume_krw: number;
    previous_bithumb_volume_krw: number;
    domestic_volume_change_krw: number;
    bithumb_volume_change_krw: number;
    contribution_pp: number;
  }>;
  attribution_headline?: {
    primary_asset: MarketShareMajorAsset | null;
    dominant_listing_structure: MarketShareListingStructureHeadline | null;
    volume_relationship?: MarketShareVolumeRelationship;
  };
  major_assets: MarketShareMajorAsset[];
  major_asset_coverage_pct: number | null;
  positive_assets: MarketShareMajorAsset[];
  positive_asset_coverage_pct: number | null;
  negative_assets: MarketShareMajorAsset[];
  negative_asset_coverage_pct: number | null;
  all_assets_in_change_attribution: number;
  checks: MarketShareExpertCheck[];
  causal_claim: false;
}

export type BithumbGapType =
  | "UNLISTED"
  | "NO_RECENT_BITHUMB_VOLUME"
  | "BELOW_VENUE_BASELINE"
  | "AT_OR_ABOVE_BASELINE"
  | "NO_RECENT_DOMESTIC_VOLUME";

export interface MarketShareAsset {
  official_minute_replacement_used?: boolean;
  official_minute_replacement_volume_krw?: number;
  official_minute_replacement_exchanges?: string[];
  trade_detail_incomplete?: boolean;
  asset: string;
  asset_identity?: MarketShareAssetIdentity;
  listed_on_bithumb: boolean;
  gap_type: BithumbGapType;
  domestic_volume_krw: number | null;
  global_volume_krw: number | null;
  bithumb_volume_krw: number;
  upbit_volume_krw: number;
  upbit_capture_pct: number | null;
  bithumb_minus_upbit_capture_pp: number | null;
  competitor_volume_krw: number;
  bithumb_capture_pct: number | null;
  capture_gap_vs_venue_pp: number | null;
  domestic_volume_share_pct: number | null;
  global_volume_share_pct: number | null;
  global_minus_domestic_interest_pp: number | null;
  competitor_leader: string;
  competitor_leader_volume_krw: number;
  domestic_exchanges: string[];
  domestic_exchange_count: number;
  exclusive_on_bithumb: boolean;
  shared_bithumb_upbit: boolean;
  bithumb_market_count: number;
  bithumb_trade_count: number;
  domestic_trade_count: number;
  bithumb_trade_count_capture_pct: number | null;
  bithumb_base_quantity: number;
  domestic_base_quantity: number;
  bithumb_base_quantity_capture_pct: number | null;
  quotes: string[];
  share_change_contribution_pp: number | null;
  domestic_volume_change_krw: number | null;
  bithumb_volume_change_krw: number | null;
  microstructure: MarketShareAssetMicrostructure;
}

export interface MarketShareAssetIdentity {
  normalized_symbol: string;
  canonical_asset_id: string;
  contract_identity_status: "UNVERIFIED" | "VERIFIED" | "CONFLICT" | string;
  contract_identity_verified: boolean;
  source: string;
}

export interface MarketShareAssetMicrostructure {
  asset: string;
  status: string;
  live_market_count: number;
  bithumb_live_market_count: number;
  bithumb_orderbook_max_age_sec: number | null;
  competitor_orderbook_max_age_sec: number | null;
  domestic_depth_krw: number | null;
  bithumb_depth_krw: number | null;
  competitor_depth_krw: number | null;
  bithumb_liquidity_capture_pct: number | null;
  bithumb_median_spread_bps: number | null;
  best_competitor_spread_bps: number | null;
  spread_gap_bps: number | null;
  bithumb_buy_slippage_bps: number | null;
  bithumb_sell_slippage_bps: number | null;
  bithumb_buy_fill_pct: number | null;
  bithumb_sell_fill_pct: number | null;
  bithumb_krw_market?: string;
  bithumb_best_bid_krw?: number | null;
  bithumb_best_ask_krw?: number | null;
  bithumb_buy_vwap_krw?: number | null;
  bithumb_sell_vwap_krw?: number | null;
  bithumb_buy_base_quantity?: number | null;
  bithumb_sell_base_quantity?: number | null;
  best_competitor_buy_slippage_bps: number | null;
  buy_slippage_gap_bps: number | null;
  best_bithumb_ask_krw: number | null;
  best_competitor_ask_krw: number | null;
  bithumb_buy_price_gap_bps: number | null;
  global_live_market_count: number;
  global_depth_krw: number | null;
  global_reference_mid_krw: number | null;
  global_reference_exchange_count: number;
  global_reference_exchanges: string[];
  global_reference_dispersion_bps: number | null;
  global_best_bid_krw: number | null;
  global_best_ask_krw: number | null;
  bithumb_mid_krw: number | null;
  bithumb_global_premium_bps: number | null;
  global_reference_method: string;
  price_validation: {
    status: "NOT_EVALUATED" | "PROVISIONAL" | "QUARANTINED";
    reason: string;
    observed_gap_bps: number | null;
    quarantine_threshold_bps: number;
    buy_exchange: string | null;
    sell_exchange: string | null;
    max_orderbook_age_sec: number | null;
  };
}

export interface MarketShareLiquidityExchange {
  exchange: string;
  registered_market_count: number;
  live_market_count: number;
  live_market_pct: number | null;
  registered_asset_count: number;
  live_asset_count: number;
  live_asset_pct: number | null;
  fully_live_asset_count: number;
  partial_live_asset_count: number;
  unavailable_asset_count: number;
  registered_asset_selected_volume_krw: number;
  live_asset_selected_volume_krw: number;
  live_asset_selected_volume_coverage_pct: number | null;
  buy_complete_asset_pct: number | null;
  sell_complete_asset_pct: number | null;
  median_asset_spread_bps: number | null;
  average_buy_asset_slippage_bps: number | null;
  average_sell_asset_slippage_bps: number | null;
  average_buy_asset_slippage_scenarios: Array<{
    notional_krw: number;
    average_slippage_bps: number | null;
    complete_asset_count: number;
    complete_asset_pct: number | null;
  }>;
  average_sell_asset_slippage_scenarios: Array<{
    notional_krw: number;
    average_slippage_bps: number | null;
    complete_asset_count: number;
    complete_asset_pct: number | null;
  }>;
  depth_krw: number | null;
  cumulative_depth_krw: Record<"1" | "3" | "5" | "10", number | null>;
  bid_depth_krw: number | null;
  ask_depth_krw: number | null;
  liquidity_share_pct: number | null;
  median_spread_bps: number | null;
  average_spread_bps: number | null;
  average_imbalance: number | null;
  average_update_rate_hz: number | null;
  average_event_latency_ms: number | null;
  buy_complete_market_pct: number | null;
  sell_complete_market_pct: number | null;
  average_buy_slippage_bps: number | null;
  average_sell_slippage_bps: number | null;
  known_bid_order_count: number;
  known_ask_order_count: number;
  sequence_gap_count: number;
}

export interface MarketShareSlippageListingGroup {
  key: "BITHUMB_EXCLUSIVE" | "BITHUMB_UPBIT_COMMON";
  label: string;
  registered_asset_count: number;
  exchanges: Array<{
    exchange: string;
    registered_asset_count: number;
    live_asset_count: number;
    average_buy_asset_slippage_scenarios: MarketShareLiquidityExchange["average_buy_asset_slippage_scenarios"];
    average_sell_asset_slippage_scenarios: MarketShareLiquidityExchange["average_sell_asset_slippage_scenarios"];
  }>;
}

export interface MarketShareAssetExchangeSlippage {
  asset: string;
  exchange: string;
  market: string;
  selected_volume_krw: number;
  status: string;
  age_sec: number | null;
  depth_krw: number | null;
  buy_complete: boolean;
  sell_complete: boolean;
  buy_fill_pct: number;
  sell_fill_pct: number;
  buy_slippage_bps: number | null;
  sell_slippage_bps: number | null;
  best_bid_krw: number | null;
  best_ask_krw: number | null;
  buy_vwap_krw: number | null;
  sell_vwap_krw: number | null;
}

export interface MarketShareLiquidity {
  analysis_depth: number;
  reference_notional_krw: number;
  observed_depth_only: boolean;
  extrapolation_used: boolean;
  domestic_depth_krw: number | null;
  bithumb_depth_krw: number | null;
  exchanges: MarketShareLiquidityExchange[];
  slippage_listing_groups?: MarketShareSlippageListingGroup[];
  asset_exchanges?: MarketShareAssetExchangeSlippage[];
  assets?: MarketShareAssetMicrostructure[];
  comparable_policy: {
    analysis_depth: 10;
    both_sides_complete_required: true;
    fresh_orderbook_required: true;
    fx_required: true;
    asset_weight: "SELECTED_WINDOW_DOMESTIC_VOLUME";
    canonical_policy: "NORMALIZED_BASE_SYMBOL";
    identity_status?: string;
    identity_source?: string;
    contract_identity_verified: boolean;
  };
  comparable_exchanges: Array<{
    exchange: string;
    shared_asset_count: number;
    comparable_asset_count: number;
    comparable_asset_pct: number | null;
    selected_volume_krw: number;
    selected_volume_coverage_pct: number | null;
    weighted_median_bithumb_buy_slippage_bps: number | null;
    weighted_median_competitor_buy_slippage_bps: number | null;
    weighted_median_buy_slippage_gap_bps: number | null;
    weighted_median_bithumb_sell_slippage_bps: number | null;
    weighted_median_competitor_sell_slippage_bps: number | null;
    weighted_median_sell_slippage_gap_bps: number | null;
    weighted_median_bithumb_spread_bps: number | null;
    weighted_median_competitor_spread_bps: number | null;
    weighted_median_spread_gap_bps: number | null;
    bithumb_displayed_depth_krw: number;
    competitor_displayed_depth_krw: number;
    bithumb_displayed_depth_share_pct: number | null;
    weighting_status: string;
  }>;
  price_anomaly_policy: {
    quarantine_threshold_bps: number;
    contract_identity_required_for_release: true;
    released_price_signals: false;
  };
  quarantined_assets: Array<{
    asset: string;
    status: "QUARANTINED";
    reason: string;
    observed_gap_bps: number;
    quarantine_threshold_bps: number;
    buy_exchange: string | null;
    sell_exchange: string | null;
    max_orderbook_age_sec: number | null;
  }>;
  global_depth_krw: number | null;
  global_exchanges: MarketShareLiquidityExchange[];
  global_orderbook_status?: "AVAILABLE" | "DISABLED" | "UNAVAILABLE";
}

export interface FxQuoteAsset {
  rate_krw: number;
  executable_buy_rate_krw: number | null;
  executable_sell_rate_krw: number | null;
  executable_spread_bps: number | null;
  executable_status:
    "LIVE" | "PARTIAL" | "UNRESOLVED" | "CROSSED_EXECUTABLE_PATHS";
  path: string[];
  hop_count: number;
  max_age_ms: number;
  source_count: number;
  max_dispersion_pct: number;
}

export interface CollectorCoverage {
  enabled: boolean;
  disabled_reason: string;
  orderbook_enabled: boolean;
  orderbook_disabled_reason: string;
  market_count: number;
  asset_count: number;
  subscribed_market_count: number;
  connected_market_count: number;
  connected_market_pct: number | null;
  fresh_market_count: number;
  fresh_trade_market_count: number;
  fresh_orderbook_market_count: number;
  observed_trade_market_count: number;
  observed_orderbook_market_count: number;
  fresh_subscription_market_count: number;
  fresh_market_pct: number | null;
  fresh_subscription_market_pct: number | null;
  chunk_count: number;
  connected_chunks: number;
  fresh_chunks: number;
  stale_chunks: number;
  never_received_chunks: number;
  never_received_trade_chunks: number;
  never_received_orderbook_chunks: number;
  active_subscription_error_chunks: number;
  active_trade_parse_error_chunks: number;
  realtime_ready: boolean;
  trade_stream_ready: boolean;
  trade_activity_fresh: boolean;
  krw_trade_stream_ready?: boolean;
  krw_trade_activity_fresh?: boolean;
  orderbook_stream_ready: boolean;
  messages: number;
  trades: number;
  trade_messages: number;
  trade_snapshots_skipped: number;
  unparsed_trade_events: number;
  orderbooks: number;
  parse_errors: number;
  subscription_errors: number;
  reconnects: number;
  disconnected_seconds_across_chunks: number;
  max_message_age_sec: number | null;
  max_data_age_sec: number | null;
  max_trade_age_sec: number | null;
  max_orderbook_age_sec: number | null;
  last_trade_ts: number;
  last_orderbook_ts: number;
  errors: string[];
  transport: "WEBSOCKET_ONLY";
  rest_fallback_enabled: false;
}

export interface NoticeMarketContext {
  asset: string;
  domestic_volume_krw: number | null;
  bithumb_capture_pct: number | null;
  upbit_capture_pct: number | null;
  current_window_contribution_pp: number | null;
  contribution_scope: "CURRENT_SELECTED_WINDOW_NOT_EVENT_CAUSAL";
  /** @deprecated Always null; this contextual value is not notice impact. */
  share_change_contribution_pp: null;
  exclusive_on_bithumb: boolean;
  shared_bithumb_upbit: boolean;
  gap_type: BithumbGapType;
}

export interface ExchangeNotice {
  exchange: string;
  notice_id: string;
  title: string;
  url: string;
  category: string;
  published_ts_ms: number;
  detected_ts_ms: number;
  timestamp_precision: "SECOND" | "DAY" | "DETECTED";
  assets: string[];
  actions?: Array<{
    subject: string;
    verb: string;
    evidence: string;
  }>;
  /** Deterministic official-title facts, kept separate from grounded LLM actions. */
  title_actions?: Array<{
    subject: string;
    verb: string;
    evidence: string;
  }>;
  source_type: string;
  updated_ts_ms: number;
  summary: string;
  body_excerpt: string;
  schedules: Array<{
    kind: string;
    start_ts_ms: number;
    end_ts_ms: number;
    timezone: string;
    evidence: string;
    precision?: "SECOND" | "DAY" | "RECURRING" | "APPROXIMATE" | string;
  }>;
  risk_tags: string[];
  image_urls: string[];
  image_text: string;
  image_analysis: Array<{
    url: string;
    status: string;
    text: string;
    bytes: number;
    semantic_type?: string;
    signals?: string[];
    interpretation?: string;
    ocr_engine?: string;
    ocr_confidence?: number;
    failure_reason?: string;
    content_sha256?: string;
    text_regions?: Array<{
      text: string;
      confidence: number;
      x: number;
      y: number;
      width: number;
      height: number;
    }>;
  }>;
  image_consistency?: MarketShareNoticeImageConsistency;
  content_source: string;
  analysis_status: string;
  analysis_confidence: number;
  detail_checked_ts_ms: number;
  original_language?: string;
  original_title?: string;
  original_body_text?: string;
	 notice_quality_v2?: {
	   schema_version: 2;
	   reviewed_partitions?: number;
	   total_partitions?: number;
	   section_summaries?: string[];
	   ocr_state?: string;
	   revision_id: string | null;
	   fact_set_id: string | null;
	   summary_id: string | null;
	   body_state: string;
	   facts_state: string;
	   summary_state: string;
	   freshness: string;
	   answerable_scope: string;
	   required_conditions: string[];
	   has_more_conditions: boolean;
	 };
  translated_title_ko?: string;
  translated_body_text_ko?: string;
  translation_status?: "TRANSLATED" | "PENDING" | "FAILED" | "NOT_REQUIRED";
  importance?: "NORMAL" | "IMPORTANT";
  analysis_provider?: "openai-codex" | "";
  analysis_model?: string;
  reasoning_effort?: string;
  escalation_reason?: string;
  analysis_policy_version?: string;
  within_selected_window: boolean;
  inflection_status:
    "OBSERVED_NEAR_NOTICE_OR_SCHEDULE" | "INSUFFICIENT_OBSERVATION";
  reaction_status: string;
  reaction_studies: Array<{
    asset: string;
    exchange: string;
    status: string;
    anchor_kind: string;
    anchor_ts_ms: number;
    anchor_evidence: string;
    pre_window_sec: number;
    post_window_sec: number;
    observed_post_sec?: number;
    exchange_pre_unresolved_fx_count?: number;
    exchange_post_unresolved_fx_count?: number;
    domestic_pre_unresolved_fx_count?: number;
    domestic_post_unresolved_fx_count?: number;
    global_pre_unresolved_fx_count?: number;
    global_post_unresolved_fx_count?: number;
    exchange_volume_multiplier?: number | null;
    domestic_volume_multiplier?: number | null;
    global_volume_multiplier?: number | null;
    bithumb_pre_share_pct?: number | null;
    bithumb_post_share_pct?: number | null;
    bithumb_share_change_pp?: number | null;
    standardized_log_volume_shift?: number | null;
    statistical_status?: string;
    origin_candidate?: string;
    evidence_confidence_pct?: number | null;
    contiguous_market_coverage?: boolean;
    domestic_reaction_start_ts_ms?: number | null;
    global_reaction_start_ts_ms?: number | null;
    venue_reaction_start_ts_ms?: number | null;
    exchange_reactions?: Record<
      string,
      {
        pre_volume_krw?: number;
        post_volume_krw?: number;
        pre_observed_seconds?: number;
        post_observed_seconds?: number;
        data_status?: "OBSERVED" | "UNAVAILABLE" | "UNRESOLVED_FX";
        pre_unresolved_fx_count?: number;
        post_unresolved_fx_count?: number;
        pre_volume_per_sec_krw?: number | null;
        post_volume_per_sec_krw?: number | null;
        volume_multiplier?: number | null;
        pre_trade_count?: number;
        post_trade_count?: number;
      }
    >;
    global_pre_volume_krw?: number | null;
    global_post_volume_krw?: number | null;
    global_pre_observed_seconds?: number;
    global_post_observed_seconds?: number;
    causal_claim: false;
  }>;
  market_context: NoticeMarketContext[];
}

export interface MarketShareNotices {
  version: number;
  poll_interval_sec: number;
  history_days: number;
  ocr_backend: string;
  healthy_source_count: number;
  complete_history_source_count: number;
  active_error_count: number;
  source_count: number;
  causal_claim: boolean;
  error_history: Array<{
    id: number;
    exchange: string;
    error_kind: string;
    message: string;
    started_ts: number;
    last_seen_ts: number;
    resolved_ts: number;
    occurrence_count: number;
    blocked_until_ts: number;
    active: boolean;
  }>;
  sources: Record<
    string,
    {
      source_type: string;
      source_url: string;
      last_poll_ts: number;
      last_success_ts: number;
      last_history_scan_ts: number;
      fetched_count: number;
      new_count: number;
      updated_count: number;
      detail_analyzed_count: number;
      image_analyzed_count: number;
      oldest_fetched_ts_ms: number;
      history_days: number;
      history_status: "PENDING" | "COMPLETE" | "SOURCE_LIMITED" | "INCOMPLETE";
      history_note: string;
      pages_scanned: number;
      error_kind: string;
      error_started_ts: number;
      error_last_seen_ts: number;
      error_occurrences: number;
      blocked_until_ts: number;
      error: string;
    }
  >;
  items: ExchangeNotice[];
}

/**
 * Cached notice metadata sent before the expensive reaction analysis finishes.
 * Keeping source state with the rows prevents a six-hour source block from
 * disappearing during the warmup screen.
 */
export type MarketShareNoticeSnapshot = Pick<
  MarketShareNotices,
  | "version"
  | "poll_interval_sec"
  | "history_days"
  | "healthy_source_count"
  | "complete_history_source_count"
  | "active_error_count"
  | "source_count"
  | "error_history"
  | "sources"
> & {
  items: ExchangeNotice[];
};

export type MarketShareOriginType =
  | "DOMESTIC_NOTICE_FIRST"
  | "GLOBAL_FIRST"
  | "BINANCE_FIRST"
  | "MIXED"
  | "DOMESTIC_MARKET_FIRST"
  | "UNRESOLVED";

export type MarketShareEvidenceStatus =
  "STRONG" | "MODERATE" | "WEAK" | "UNAVAILABLE";

export interface MarketShareReactionOrigin {
  type: MarketShareOriginType;
  label: string;
  event_time_ms: number | null;
  event_time_basis:
    "NOTICE" | "EFFECTIVE" | "SCHEDULE" | "DETECTED" | "UNAVAILABLE";
  notice_time_ms: number | null;
  effective_time_ms: number | null;
  domestic_reaction_time_ms: number | null;
  /** Binance Spot reaction timestamp; the wire name is kept for compatibility. */
  global_reaction_time_ms?: number | null;
  /** @deprecated Legacy Binance Spot alias; use global_reaction_time_ms. */
  binance_reaction_time_ms: number | null;
  lead_seconds: number | null;
  evidence_level: MarketShareEvidenceStatus;
}

export interface MarketShareReactionAsset {
  asset: string;
  current_window_contribution_pp: number | null;
  contribution_scope: "CURRENT_SELECTED_WINDOW_NOT_EVENT_CAUSAL";
  /** @deprecated Always null; use current_window_contribution_pp. */
  impact_pp: null;
  domestic_volume_krw: number | null;
  domestic_volume_change_krw: number | null;
  bithumb_volume_change_krw: number | null;
  listed_on_bithumb: boolean;
  gap_type: BithumbGapType;
  domestic_exchanges: string[];
}

export interface MarketShareReactionExchange {
  exchange: string;
  before_volume_krw: number | null;
  after_volume_krw: number | null;
  volume_change_pct: number | null;
  before_trade_count: number | null;
  after_trade_count: number | null;
  before_unresolved_fx_count?: number;
  after_unresolved_fx_count?: number;
  data_status:
    | "OBSERVED"
    | "OBSERVED_ZERO"
    | "PARTIAL_OBSERVATION"
    | "UNRESOLVED_FX"
    | "QUARANTINED_GLOBAL_COVERAGE_OR_IDENTITY"
    | "UNAVAILABLE";
}

export interface MarketShareMicrostructureValues {
  bithumb_mid_krw: number | null;
  bithumb_spread_bps: number | null;
  bithumb_depth_krw: number | null;
  bithumb_buy_slippage_bps: number | null;
  bithumb_sell_slippage_bps: number | null;
  bithumb_buy_fill_pct: number | null;
  bithumb_sell_fill_pct: number | null;
  global_reference_mid_krw: number | null;
  global_depth_krw: number | null;
}

export interface MarketShareMicrostructureComparison {
  status: string;
  pre_point_count: number;
  post_point_count: number;
  pre_observed_start_ms: number | null;
  pre_observed_end_ms: number | null;
  post_observed_start_ms: number | null;
  post_observed_end_ms: number | null;
  before: MarketShareMicrostructureValues;
  after: MarketShareMicrostructureValues;
  change_pct: MarketShareMicrostructureValues;
  note: string;
}

export interface MarketShareBinanceAssetSnapshot {
  asset: string;
  status: string;
  current_price_krw: number | null;
  current_depth_krw: number | null;
  before_price_krw: number | null;
  after_price_krw: number | null;
  price_change_pct: number | null;
  before_depth_krw: number | null;
  after_depth_krw: number | null;
  depth_change_pct: number | null;
}

export interface MarketShareReactionEvent {
  event_id: string;
  event_group_id: string;
  related_notice_count: number;
  related_event_ids: string[];
  exchange: string;
  event_type: string;
  title: string;
  source_url: string;
  published_at_ms: number | null;
  timestamp_precision: "SECOND" | "DAY" | "DETECTED";
  collected_at_ms: number | null;
  effective_at_ms: number | null;
  ended_at_ms: number | null;
  affected_assets: string[];
  linked_assets: MarketShareReactionAsset[];
  linked_asset_current_window_contribution_pp: number | null;
  positive_current_window_contribution_pp: number | null;
  negative_current_window_contribution_pp: number | null;
  contribution_scope: "CURRENT_SELECTED_WINDOW_NOT_EVENT_CAUSAL";
  /** @deprecated Always null; these were not event-aligned causal impacts. */
  linked_asset_impact_pp: null;
  positive_impact_pp: null;
  negative_impact_pp: null;
  domestic_volume_before_krw: number | null;
  domestic_volume_after_krw: number | null;
  domestic_volume_change_pct: number | null;
  bithumb_share_change_pp: number | null;
  origin: MarketShareReactionOrigin;
  reaction_state: string;
  reaction_label: string;
  maturity: string;
  evidence_level: MarketShareEvidenceStatus;
  evidence_reasons: string[];
  reaction_study: ExchangeNotice["reaction_studies"][number] | null;
  timeline: Array<{
    kind: string;
    label: string;
    at_ms: number | null;
    status: "OBSERVED" | "UNAVAILABLE";
  }>;
  exchange_reactions: MarketShareReactionExchange[];
  /** Retired realtime-global compatibility object; production status is UNAVAILABLE. */
  global_context: {
    status:
      "OBSERVED" | "QUARANTINED_GLOBAL_COVERAGE_OR_IDENTITY" | "UNAVAILABLE";
    volume_multiplier: number | null;
    before_volume_krw: number | null;
    after_volume_krw: number | null;
    volume_change_pct: number | null;
    reaction_start_ts_ms: number | null;
    /** Empty in production because global event matching is policy-disabled. */
    included_exchanges: string[];
  };
  binance_context: {
    status:
      "OBSERVED" | "QUARANTINED_GLOBAL_COVERAGE_OR_IDENTITY" | "UNAVAILABLE";
    price_change_pct: number | null;
    before_price_krw?: number | null;
    after_price_krw?: number | null;
    before_depth_krw?: number | null;
    after_depth_krw?: number | null;
    depth_change_pct?: number | null;
    current_price_krw?: number | null;
    current_depth_krw?: number | null;
    microstructure_status?: string;
    asset_snapshots?: MarketShareBinanceAssetSnapshot[];
    volume_multiplier: number | null;
    before_volume_krw: number | null;
    after_volume_krw: number | null;
    volume_change_pct?: number | null;
    reaction_start_ts_ms: number | null;
    note?: string;
  };
  microstructure: Array<{
    asset: string;
    as_of_ms: number;
    status: string;
    bithumb_mid_krw: number | null;
    bithumb_spread_bps: number | null;
    bithumb_depth_krw: number | null;
    bithumb_buy_slippage_bps: number | null;
    bithumb_sell_slippage_bps: number | null;
    bithumb_buy_fill_pct: number | null;
    bithumb_sell_fill_pct: number | null;
    global_reference_mid_krw?: number | null;
    global_depth_krw?: number | null;
    comparison_status: string;
    comparison?: MarketShareMicrostructureComparison;
    note: string;
  }>;
  data_quality: {
    status: string;
    observed_post_sec: number | null;
    contiguous_market_coverage: boolean;
    note: string;
  };
}

export interface MarketShareReactionSummary {
  analysis_context_id: string | null;
  as_of: number;
  selected_window: string;
  market_scope: MarketScope;
  market_share: {
    current_pct: number | null;
    current_available: boolean;
    previous_pct: number | null;
    change_pp: number | null;
    status: string;
  };
  origin: MarketShareReactionOrigin;
  notice_summary: {
    event_count: number;
    linked_asset_count: number;
    linked_asset_current_window_contribution_pp: number | null;
    positive_current_window_contribution_pp: number | null;
    negative_current_window_contribution_pp: number | null;
    positive_current_window_contribution_asset_count: number | null;
    negative_current_window_contribution_asset_count: number | null;
    contribution_scope: "CURRENT_SELECTED_WINDOW_NOT_EVENT_CAUSAL";
    /** @deprecated Always null; use the explicit current-window fields. */
    linked_asset_impact_pp: null;
    positive_impact_pp: null;
    negative_impact_pp: null;
    /** @deprecated Always null; use the explicit current-window fields. */
    positive_impact_asset_count: null;
    negative_impact_asset_count: null;
    domestic_volume_coverage_pct: number | null;
  };
  top_notice: MarketShareReactionEvent | null;
  largest_exchange_change: {
    event_id: string;
    event_title: string;
    exchange: string;
    volume_change_pct: number;
    volume_change_krw: number;
    linked_asset_count: number;
    current_window_contribution_pp: number | null;
    contribution_scope: "CURRENT_SELECTED_WINDOW_NOT_EVENT_CAUSAL";
    /** @deprecated Always null; use current_window_contribution_pp. */
    share_impact_pp: null;
  } | null;
  events: MarketShareReactionEvent[];
  no_notice_context: {
    status: "NO_RELEVANT_NOTICE" | "NOTICE_AVAILABLE";
    message: string;
    domestic_volume_krw?: number | null;
    /** @deprecated Always null in production; use official_binance_24h separately. */
    global_context_volume_krw?: number | null;
    /** @deprecated Always null in production; use official_binance_24h separately. */
    binance_volume_krw?: number | null;
    global_context_status?: "UNAVAILABLE";
    binance_context_status?: "UNAVAILABLE";
    domestic_share_pct?: number | null;
    momentum?: {
      short_share_pct: number | null;
      long_share_pct: number | null;
      change_pp: number | null;
      short_status: string;
      long_status: string;
      short_coverage_sec: number;
      long_coverage_sec: number;
    };
    leading_asset?: {
      asset: string;
      share_change_contribution_pp: number | null;
      domestic_volume_krw: number | null;
    } | null;
    market_signal?: {
      kind: string;
      title: string;
      detail: string;
      asset: string | null;
      value_krw: number | null;
      value_pct: number | null;
      value_bps: number | null;
    } | null;
  };
}

export type MarketShareRealtimeFlowStatus =
  "WARMING" | "NORMAL" | "WATCH" | "ALERT" | "DEGRADED";

export type MarketShareRealtimeFlowDirection =
  "UNKNOWN" | "STABLE" | "UP" | "DOWN";

export interface MarketShareRealtimeOrderbook {
  status: string;
  age_sec: number | null;
  imbalance: number | null;
  spread_bps: number | null;
  bid_depth_krw: number | null;
  ask_depth_krw: number | null;
  buy_slippage_bps: number | null;
  sell_slippage_bps: number | null;
}

export interface MarketShareRealtimeFlowWindow {
  duration_sec: 10 | 30 | 60 | number;
  status: "COMPLETE" | "PARTIAL" | string;
  current_share_pct: number | null;
  previous_share_pct: number | null;
  share_delta_pp: number | null;
  current_volume_krw: number;
  previous_volume_krw: number;
  threshold_pp: number;
}

export interface MarketShareRealtimeFlowExchangeImpact {
  exchange: string;
  contribution_pp: number;
  volume_change_krw: number;
  previous_volume_krw: number;
  current_volume_krw: number;
  buy_pct: number | null;
  orderbook?: MarketShareRealtimeOrderbook;
}

export interface MarketShareRealtimeFlowDriver {
  rank: number;
  asset: string;
  contribution_pp: number;
  bithumb_volume_change_krw: number;
  competitor_volume_change_krw: number;
  previous_bithumb_volume_krw: number;
  current_bithumb_volume_krw: number;
  previous_competitor_volume_krw: number;
  current_competitor_volume_krw: number;
  bithumb_buy_pct: number | null;
  top_competitor: string;
  top_competitor_volume_krw: number;
  exchange_impacts: MarketShareRealtimeFlowExchangeImpact[];
  orderbook?: MarketShareRealtimeOrderbook;
}

export interface MarketShareRealtimeFlowExchangeDriver {
  rank: number;
  exchange: string;
  contribution_pp: number;
  volume_change_krw: number;
  previous_volume_krw: number;
  current_volume_krw: number;
  leading_asset: string;
  leading_asset_contribution_pp: number;
}

export interface MarketShareRealtimeFlowCurrentAsset {
  asset: string;
  domestic_volume_krw: number;
  bithumb_volume_krw: number;
  bithumb_share_pct: number | null;
  bithumb_buy_pct: number | null;
  top_competitor: string;
  top_competitor_volume_krw: number;
  orderbook?: MarketShareRealtimeOrderbook;
}

export interface MarketShareRealtimeFlowSharePoint1S {
  as_of_ms: number;
  status: string;
  complete: boolean;
  domestic_volume_1s_krw: number;
  bithumb_volume_1s_krw: number;
  bithumb_share_1s_pct: number | null;
  observed_bithumb_share_1s_pct?: number | null;
  exchange_volumes_1s_krw?: Record<string, number>;
  exchange_trade_counts_1s?: Record<string, number>;
  exchange_shares_1s_pct?: Record<string, number | null>;
}

export interface MarketShareRealtimeFlowAttribution {
  window_sec: number;
  stored_driver_count: number;
  total_driver_count: number;
  residual_contribution_pp: number | null;
  drivers: MarketShareRealtimeFlowDriver[];
  exchange_drivers: MarketShareRealtimeFlowExchangeDriver[];
}

/**
 * WebSocket-only diagnostic signal. It must stay separate from the official
 * rolling 24-hour ticker share and must not be reconciled to that value.
 */
export interface MarketShareRealtimeFlowState {
  version: "REALTIME_FLOW_V2" | string;
  as_of_ms: number;
  status: MarketShareRealtimeFlowStatus;
  direction: MarketShareRealtimeFlowDirection;
  reason: string;
  primary_window_sec: number;
  share_delta_pp: number | null;
  stored_driver_count: number;
  total_driver_count: number;
  residual_contribution_pp: number | null;
  domestic_volume_1s_krw: number;
  bithumb_volume_1s_krw: number;
  bithumb_share_1s_pct: number | null;
  recent_share_1s?: MarketShareRealtimeFlowSharePoint1S[];
  windows: MarketShareRealtimeFlowWindow[];
  drivers: MarketShareRealtimeFlowDriver[];
  exchange_drivers: MarketShareRealtimeFlowExchangeDriver[];
  ten_second_attribution?: MarketShareRealtimeFlowAttribution | null;
  current_assets: MarketShareRealtimeFlowCurrentAsset[];
  data_quality: Record<string, unknown>;
}

export interface MarketShareWhaleAlertEvent {
  id: string;
  kind: "SINGLE" | "BURST";
  exchange: string;
  market: string;
  asset: string;
  quote: string;
  side: "BUY" | "SELL";
  notional_krw: number;
  base_quantity: number;
  trade_count: number;
  started_at_ms: number;
  event_at_ms: number;
  detected_at_ms: number;
  window_ms: number;
}

export interface MarketShareWhaleAlertState {
  version: "WHALE_ALERT_V1" | string;
  threshold_krw: number;
  window_ms: number;
  generated_at_ms: number;
  events: MarketShareWhaleAlertEvent[];
}

export interface MarketShareRealtimeFlowHistoryResponse {
  items: MarketShareRealtimeFlowState[];
  count: number;
  contract_version: "REALTIME_FLOW_V2" | string;
}

export interface MarketShareRealtimeFlowPeriodDriver {
  rank: number;
  asset: string;
  cumulative_contribution_pp: number;
  absolute_contribution_pp: number;
  peak_contribution_pp: number;
  observation_count: number;
  positive_observation_count: number;
  negative_observation_count: number;
  direction_consistency_pct: number;
  bithumb_volume_change_krw: number;
  competitor_volume_change_krw: number;
  first_observed_at_ms: number;
  last_observed_at_ms: number;
  episode_count: number;
}

export interface MarketShareRealtimeFlowEpisode {
  id: string;
  start_ms: number;
  end_ms: number;
  direction: "UP" | "DOWN" | "STABLE" | "UNKNOWN" | string;
  status: string;
  observation_count: number;
  duration_sec: number;
  net_share_change_pp: number;
  gross_share_change_pp: number;
  peak_share_change_pp: number;
  attributed_sample_count: number;
  top_asset: string;
  top_asset_contribution_pp: number;
  top_exchange: string;
  top_exchange_contribution_pp: number;
  drivers: MarketShareRealtimeFlowEpisodeDriver[];
}

export interface MarketShareRealtimeFlowEpisodeDriver {
  rank: number;
  asset: string;
  net_contribution_pp: number;
  gross_contribution_pp: number;
  peak_contribution_pp: number;
  observation_count: number;
  bithumb_volume_change_krw: number;
  competitor_volume_change_krw: number;
}

export interface MarketShareRealtimeFlowDriverBreadth {
  basis: string;
  positive_driver_count: number;
  negative_driver_count: number;
  effective_driver_count: number;
  top1_absolute_contribution_share_pct: number;
  top3_absolute_contribution_share_pct: number;
  top5_absolute_contribution_share_pct: number;
}

export interface MarketShareRealtimeFlowCoverageSegment {
  start_ms: number;
  end_ms: number;
  status: string;
  reason?: string;
}

export interface MarketShareRealtimeFlowPeriodSummary {
  contract_version: "REALTIME_FLOW_V2" | string;
  from_ms: number;
  to_ms: number;
  window_sec: number;
  sample_sec: number;
  status: "COMPLETE" | "PARTIAL" | "UNAVAILABLE" | string;
  expected_sample_count: number;
  observed_sample_count: number;
  missing_sample_count: number;
  observed_sec: number;
  missing_sec: number;
  coverage_pct: number;
  observed_share_change_pp: number;
  signal_sample_count: number;
  watch_sample_count: number;
  alert_sample_count: number;
  attributed_sample_count: number;
  attributed_sec: number;
  attribution_missing_sec: number;
  attribution_coverage_pct: number;
  residual_contribution_pp: number;
  drivers: MarketShareRealtimeFlowPeriodDriver[];
  episodes: MarketShareRealtimeFlowEpisode[];
  driver_breadth: MarketShareRealtimeFlowDriverBreadth;
  coverage_segments: MarketShareRealtimeFlowCoverageSegment[];
}

export interface MarketRegistryRefreshObservation {
  observed_at_ms: number;
  exchange: string;
  status: "SUCCESS" | "ERROR" | string;
  authoritative: boolean;
  registry_version: number;
  previous_market_count: number;
  observed_market_count: number;
  active_market_count: number;
  active_asset_count: number;
  quote_counts: Record<string, number>;
  public_fee_market_count: number;
  discovered_count: number;
  added_count: number;
  removed_count: number;
  error: string;
}

export interface MarketRegistryLifecycleEvent {
  observed_at_ms: number;
  exchange: string;
  market: string;
  base: string;
  quote: string;
  scope: string;
  event_type: "DISCOVERED" | "ADDED" | "REMOVED" | string;
  initial_discovery: boolean;
  maker_fee_rate: number | null;
  taker_fee_rate: number | null;
  fee_source: string;
}

export interface MarketRegistryHistoryResponse {
  refreshes: MarketRegistryRefreshObservation[];
  events: MarketRegistryLifecycleEvent[];
  generated_at: number;
}

export interface MarketShareAnalysis {
  window_sec: number;
  window: string;
  definition: string;
  generated_at: number;
  context: {
    analysis_context_id: string;
    market_scope: MarketScope;
    market_scope_label: string;
    window_sec: number;
    comparison_enabled: boolean;
    comparison_window_sec: number;
    asset_scope: "ALL";
    as_of: number;
    analysis_version: number;
    aggregation_version?: number;
    selected_window_complete: boolean;
    selected_window_status:
      "CONFIRMED" | "PROVISIONAL" | "PARTIAL" | "UNAVAILABLE";
    selected_window_coverage_sec: number;
    selected_window_required_sec: number;
    current_interval_start: number;
    current_interval_end: number;
    comparison_interval_start: number | null;
    comparison_interval_end: number | null;
    data_source: MarketShareDataSource;
    orderbook_as_of: number;
    orderbook_version: number;
  };
  summary: MarketShareSummary;
  official_domestic_24h: MarketShareOfficialDomestic24h;
  official_share_monitor: MarketShareOfficialMovementState;
  /** Historical wire name. Semantics are OFFICIAL_ALIGNED_1H_CANDLE_TREND_V1. */
  official_ticker_1m: MarketShareOfficialAligned1hState;
  /** Independent reference only; never part of Bithumb share or event matching. */
  official_binance_24h: MarketShareOfficialBinance24h;
  realtime_flow: MarketShareRealtimeFlowState;
  findings: MarketShareFinding[];
  evidence_layers: {
    separation_rule: string;
    measured: MarketShareEvidenceLayer;
    diagnostic: MarketShareEvidenceLayer;
    contextual: MarketShareEvidenceLayer;
  };
  expert_brief: MarketShareExpertBrief;
  change_attribution: {
    status: string;
    provisional_reason?: string;
    included_exchanges?: string[];
    excluded_exchanges?: string[];
    comparison_window_complete: boolean;
    current_share_pct: number | null;
    previous_share_pct: number | null;
    share_change_pp: number | null;
    current_volume_krw: number;
    previous_volume_krw: number | null;
    market_volume_change_krw: number | null;
    market_volume_change_pct: number | null;
    current_bithumb_volume_krw: number;
    previous_bithumb_volume_krw: number | null;
    bithumb_volume_change_krw: number | null;
    bithumb_volume_change_pct: number | null;
    current_competitor_volume_krw: number;
    previous_competitor_volume_krw: number | null;
    competitor_volume_change_krw: number | null;
    competitor_volume_change_pct: number | null;
    bithumb_direct_effect_pp: number | null;
    competitor_effect_pp: number | null;
    domestic_market_size_effect_pp: number | null;
    reconciliation_error_pp: number | null;
    /**
     * The same change split another way: trading moving between coins (mix)
     * and Bithumb's part within the same coins (capture). Absent when the
     * comparison is unavailable.
     */
    mix_effect_pp?: number | null;
    capture_effect_pp?: number | null;
    mix_capture_method?: "MIDPOINT_MIX_CAPTURE_V1" | string;
    primary_driver: string;
    method: string;
    exchanges: MarketShareChangeExchange[];
    assets: MarketShareChangeAsset[];
    quotes: MarketShareChangeQuote[];
    asset_exchange_network: MarketShareAssetExchangeNetwork;
    contribution_method: "NORMALIZED_SHARE_EFFECT" | string;
    contribution_note: string;
  };
  momentum: MarketShareMomentum[];
  rolling_24h: MarketShareRolling24h;
  exchanges: MarketShareExchange[];
  quote_markets: MarketShareQuote[];
  assets: MarketShareAsset[];
  liquidity: MarketShareLiquidity;
  data_quality: {
    status: string;
    numeric_integrity?: import("./numeric-integrity-presentation").NumericIntegrityReport;
    components: {
      trade_share: {
        status: string;
        domestic_ws_realtime_complete: boolean;
        unhealthy_collectors: string[];
        queue_dropped: number;
        registry_errors: string[];
        unobserved_trade_chunks: number;
        unparsed_trade_events: number;
        active_trade_parse_error_chunks?: number;
        collector_trade_events: number;
        trades_enqueued: number;
        trades_processed: number;
        aggregator_accepted_trades: number;
        duplicate_trade_events: number;
        invalid_trade_events: number;
        trade_processing_errors: number;
        aggregation_loop_errors: number;
        aggregator_consumer_alive: boolean;
        aggregation_error_active?: boolean;
        last_aggregation_error?: string;
        last_aggregation_error_ts?: number;
      };
      liquidity: {
        status: string;
        registered_market_count: number;
        fresh_market_count: number;
        unavailable_market_count: number;
        stale_market_count: number;
        quiet_market_count: number;
        effective_stale_market_count: number;
        sequence_gap_market_count: number;
        stale_after_sec: number;
        stale_books_excluded: boolean;
      };
      fx: {
        status: string;
        unresolved_quote_assets: string[];
        domestic_unresolved_trade_count: number;
        global_unresolved_trade_count: number;
        unresolved_trade_count: number;
        window_sec?: number;
      };
      history_24h: {
        status: string;
        coverage_sec: number;
        coverage_age_sec: number | null;
        window_complete: boolean;
        gap_seconds: number;
        data_source: MarketShareDataSource;
        required_sec: number;
      };
    };
    trade_accounting?: {
      status: "BALANCED" | "COLLECTOR_LAG" | "MISMATCH" | string;
      collector_trade_events: number;
      trades_enqueued: number;
      trades_processed: number;
      aggregator_accepted_trades: number;
      duplicate_trade_events: number;
      invalid_trade_events: number;
      trade_processing_errors: number;
      queue_in_flight: number;
      processed_conservation_error: number;
      enqueued_conservation_error: number;
      collector_lag: number | null;
      input_source?: "COLLECTOR_QUEUE" | "DURABLE_JOURNAL" | string;
      collector_lag_status?:
        "AVAILABLE" | "NOT_COMPARABLE_PROCESS_LIFETIMES" | string;
      journal_trades_submitted?: number;
    };
    observation_gaps?: Array<{
      gap_id: number;
      persistence_pending?: boolean;
      start_second: number;
      end_second: number | null;
      status: "OPEN" | "RECOVERED" | string;
      duration_sec: number;
      affected_exchanges: string[];
      affected_markets: string[];
      observed_volume_krw: number | null;
      observed_total_volume_krw: number | null;
      observed_volume_at_risk_pct: number | null;
      weight_window_sec: number;
      reason_codes: string[];
      created_at: number;
      closed_at: number | null;
    }>;
    unhealthy_collectors: string[];
    domestic_ws_realtime_complete: boolean;
    global_ws_realtime_complete: boolean;
    enabled_ws_exchanges: string[];
    disabled_ws_exchanges: string[];
    disabled_orderbook_exchanges?: string[];
    live_market_data_transport: "WEBSOCKET_ONLY";
    rest_market_data_fallback_enabled: false;
    rest_usage: string[];
    registry_errors: string[];
    unresolved_quote_assets: string[];
    orderbook_initialized_market_pct: number | null;
    orderbook_unavailable_market_count: number;
    orderbook_stale_market_count: number;
    orderbook_sequence_gap_market_count: number;
    orderbook_sequence_gap_count: number;
    orderbook_health_by_exchange: Array<{
      exchange: string;
      registered_market_count: number;
      initialized_market_count: number;
      initialized_market_pct: number | null;
      fresh_market_count: number;
      fresh_market_pct: number | null;
      stale_market_count: number;
      unavailable_market_count: number;
      waiting_market_count: number;
      sequence_gap_market_count: number;
      diagnoses: Record<string, number>;
      issues: Array<{
        exchange: string;
        market: string;
        base: string;
        quote: string;
        scope: "domestic" | "global";
        status: string;
        age_sec: number | null;
        diagnosis: string;
        activity_state: "QUIET" | "STALE" | "GAP" | "UNINITIALIZED";
      }>;
    }>;
    queue_loss_detected: boolean;
    trade_event_loss_detected: boolean;
    trade_event_loss_active?: boolean;
    public_ws_is_lossless: boolean;
    aggregation_engine: string;
    native_acceleration: boolean;
    aggregation_engine_error: string;
    snapshot_duration_ms: number;
    snapshot_component_ms: Record<string, number>;
    analysis_duration_ms: number;
    snapshot_consistency: "EVENTUALLY_CONSISTENT";
    volume_filter_applied: boolean;
    market_filter_applied: boolean;
    coin_filter_applied: boolean;
    notice_source_errors: string[];
    note: string;
  };
  collectors: Record<string, CollectorCoverage>;
  fx: {
    transport: "WEBSOCKET_ONLY";
    rest_fallback_enabled: false;
    volume_conversion_policy: "BITHUMB_USDT_KRW_ANCHORED";
    volume_conversion_anchor_exchange: "bithumb";
    volume_conversion_anchor_pair: "USDT/KRW";
    max_age_ms: number;
    max_hops: number | null;
    valuation_edge_count: number;
    executable_edge_count: number;
    registered_market_count: number;
    quote_assets: Record<string, FxQuoteAsset | null>;
    unresolved_quote_assets: string[];
  };
  registry: {
    market_count: number;
    asset_count: number;
    ws_enabled_market_count: number;
    ws_enabled_asset_count: number;
    ws_enabled_exchanges: string[];
    exchanges: Record<
      string,
      {
        market_count: number;
        asset_count: number;
        quotes: Record<string, number>;
        public_fee_market_count: number;
        error: string;
      }
    >;
  };
  notices: MarketShareNotices;
  reaction_summary: MarketShareReactionSummary;
}

/**
 * The coin-drivers screen reads only part of the analysis, and the server's
 * dashboard_v1 projection for view=drivers sends only that part (the whole
 * analysis was 6.4 MB every ten seconds). Components on the screen take this
 * type, so reading a field the projection leaves out fails the type check.
 */
export type MarketShareDriversAnalysis = Omit<
  MarketShareAnalysis,
  | "rolling_24h"
  | "expert_brief"
  | "reaction_summary"
  | "realtime_flow"
  | "liquidity"
  | "assets"
> & {
  /** Only how widely coins moved and the change by listing group. */
  expert_brief?: Pick<MarketShareExpertBrief, "breadth" | "listing_segments">;
  assets: Array<Omit<MarketShareAsset, "microstructure" | "asset_identity">>;
  liquidity: Pick<
    MarketShareLiquidity,
    | "analysis_depth"
    | "reference_notional_krw"
    | "observed_depth_only"
    | "extrapolation_used"
    | "domestic_depth_krw"
    | "bithumb_depth_krw"
    | "exchanges"
    | "price_anomaly_policy"
    | "quarantined_assets"
  >;
};

/** The data-quality screen's projection: the drivers part without coin rows and notices. */
export type MarketShareQualityAnalysis = Omit<
  MarketShareDriversAnalysis,
  "assets" | "notices"
>;

export interface MarketShareSocketPayload {
  version: number;
  tick_version?: number;
  generated_at: number;
  running: boolean;
  queue_size: number;
  queue_capacity: number;
  queue_backpressure: number;
  queue_dropped: number;
  trades_enqueued: number;
  trades_processed: number;
  trade_processing_errors: number;
  aggregation_loop_errors: number;
  aggregation_error_active?: boolean;
  last_aggregation_error?: string;
  last_aggregation_error_ts?: number;
  last_aggregation_success_ts?: number;
  last_trade_processing_error: string;
  last_trade_processing_error_ts: number;
  aggregator_consumer_alive: boolean;
  readiness?: MarketShareReadiness;
  partial_analysis?: boolean;
  analysis: MarketShareAnalysis;
}

export interface MarketShareRealtimeTick {
  version: number;
  /** Transport deduplication version; changes for trade or order-book updates. */
  stream_version?: number;
  generated_at: number;
  window_sec: number;
  status: string;
  ws_realtime_complete: boolean;
  ws_status?: "LIVE" | "DEGRADED";
  window_complete: boolean;
  coverage_status: "CONFIRMED" | "PROVISIONAL" | "PARTIAL" | "UNAVAILABLE";
  coverage_sec: number;
  gap_seconds: number;
  data_source: MarketShareDataSource;
  market_scope: MarketScope;
  /** Supplied from the slower analysis frame after the first realtime tick. */
  official_domestic_24h?: MarketShareOfficialDomestic24h;
  /** Supplied from the slower analysis frame after the first realtime tick. */
  official_ticker_1m?: MarketShareOfficialTicker1mState;
  /** Independent reference only; never part of Bithumb share or event matching. */
  official_binance_24h?: MarketShareOfficialBinance24h;
  /** Supplied from the slower analysis frame after the first realtime tick. */
  realtime_flow?: MarketShareRealtimeFlowState;
  /** Compact top-of-book values sourced from the Upbit/Bithumb WebSocket collectors. */
  best_order_book_source?: "UPBIT_BITHUMB_WEBSOCKET_ORDERBOOK" | string;
  best_order_book_version?: number;
  best_order_book_as_of?: number;
  best_order_books?: MarketShareBestOrderBook[];
  readiness?: MarketShareReadiness;
  exchanges: MarketShareTickExchange[];
  /** The same window ending at the last closed second (newer calculators). */
  closed_second?: MarketShareClosedSecond;
  asset_exchange_shares?: Array<{
    asset: string;
    domestic_volume_krw: number;
    exchanges: Array<{
      exchange: string;
      share_pct: number | null;
      volume_krw: number;
    }>;
  }>;
  summary: Pick<
    MarketShareSummary,
    | "domestic_volume_krw"
    | "bithumb_volume_krw"
    | "bithumb_share_pct"
    | "domestic_trade_count"
    | "bithumb_trade_count"
    | "bithumb_trade_count_share_pct"
  >;
}

export interface MarketShareTickExchange {
  exchange: string;
  share_pct: number | null;
  volume_krw: number;
  trade_count: number;
  trade_count_share_pct: number | null;
}

/**
 * A live window ending at a closed second, by exchange. Unlike the tick's own
 * window it leaves the still-open second out, so it does not dip as each
 * second starts.
 */
export interface MarketShareClosedSecond {
  as_of_second: number;
  window_complete: boolean;
  coverage_sec: number;
  exchanges: MarketShareTickExchange[];
}

/** The live window ending at each of the last 120 closed seconds. */
export interface MarketShareClosedSeconds {
  window_sec: number;
  market_scope: MarketScope;
  seconds: MarketShareClosedSecond[];
}

export interface MarketShareBestOrderBook {
  exchange: "upbit" | "bithumb" | string;
  asset: "XRP" | "USDT" | "BTC" | "ETH" | "SOL" | string;
  market: string;
  status: string;
  age_sec: number | null;
  bid_price_krw: number | null;
  bid_quantity: number | null;
  bid_notional_krw: number | null;
  ask_price_krw: number | null;
  ask_quantity: number | null;
  ask_notional_krw: number | null;
}

/** One window compared with the window before it (window-changes endpoint). */
export interface MarketShareWindowChange {
  window_sec: number;
  window: string;
  status: "CONFIRMED" | "PROVISIONAL" | "UNAVAILABLE" | string;
  unavailable_reason?: string;
  provisional_reason?:
    | "INCOMPLETE_EXCHANGE_SET"
    | "MINOR_OBSERVATION_GAP"
    | "OBSERVED_RANGE"
    | string;
  excluded_exchanges?: string[];
  current_share_pct: number | null;
  previous_share_pct: number | null;
  share_change_pp: number | null;
  current_volume_krw: number;
  previous_volume_krw: number | null;
  /** Absent from servers before the trading changes were added. */
  bithumb_volume_change_krw?: number | null;
  competitor_volume_change_krw?: number | null;
  bithumb_direct_effect_pp: number | null;
  competitor_effect_pp: number | null;
  mix_effect_pp: number | null;
  capture_effect_pp: number | null;
  primary_driver: string;
  exchanges: MarketShareChangeExchange[];
  rising_assets: MarketShareWindowAssetChange[];
  falling_assets: MarketShareWindowAssetChange[];
  breadth: MarketShareExpertBrief["breadth"];
}

export interface MarketShareWindowAssetChange {
  asset: string;
  contribution_pp: number;
  bithumb_volume_change_krw: number;
  competitor_volume_change_krw: number;
}

/** Every rolling window but 24 hours, from one observation. */
export interface MarketShareWindowChanges {
  market_scope: MarketScope;
  /** The last closed second every window includes (Unix seconds). */
  as_of_second: number;
  windows: MarketShareWindowChange[];
}

/** Why a coin's share is missing. */
export type MarketShareCoinShareStatus =
  | "AVAILABLE"
  | "NO_TRADES"
  | "NOT_OBSERVED"
  | "UNRESOLVED_FX"
  | "UNAVAILABLE"
  | (string & {});

/** A coin's domestic and Bithumb trading and Bithumb's share of it. */
export interface MarketShareCoinShareValue {
  status: MarketShareCoinShareStatus;
  domestic_volume_krw: number | null;
  bithumb_volume_krw: number | null;
  bithumb_share_pct: number | null;
  /** Trades without a KRW value (UNRESOLVED_FX). */
  unresolved_fx_count?: number;
}

/** The bucket sizes of the realtime share series: a minute to a half hour. */
export type MarketShareSeriesBucketSec = 60 | 600 | 1800;

/**
 * One bucket of the realtime share series. A bucket that was not fully
 * observed (complete false) keeps the share of what was observed.
 */
export interface MarketShareSeriesPoint extends MarketShareCoinShareValue {
  /** The bucket's first second, in milliseconds. */
  bucket_ms: number;
  complete: boolean;
  coverage_sec: number;
}

/** Bithumb's share in each of the last closed buckets, oldest first. */
export interface MarketShareSeries {
  market_scope: MarketScope;
  bucket_sec: MarketShareSeriesBucketSec;
  /** The last second of the newest bucket (Unix seconds). */
  as_of_second: number;
  points: MarketShareSeriesPoint[];
}

/** One coin's share of the live window. */
export interface MarketShareLiveCoinShare extends MarketShareCoinShareValue {
  asset: string;
}

/**
 * Bithumb's share of the most traded coins in the last minute, which the
 * WebSocket sends every second to a screen that asks for it (coin_shares=live).
 * A frame the server could not read is {status: "UNAVAILABLE", detail}.
 */
export interface MarketShareLiveCoinShares {
  window_sec: number;
  /** The last closed second the window includes (Unix seconds). */
  as_of_second: number;
  window_complete: boolean;
  coverage_sec: number;
  /** The official 24 hours order the coins, or the realtime 24 hours while they are not live. */
  ranking: "OFFICIAL_24H_TICKER" | "REALTIME_24H" | (string & {});
  coins: MarketShareLiveCoinShare[];
}
