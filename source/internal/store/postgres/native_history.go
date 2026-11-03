package postgres

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// NativeHistoryRange is one source and currency. Window is [StartSec,EndSec).
// Source names deliberately cannot be combined into a single amount.
type NativeHistoryRange struct {
	Exchange, Market, Base, Quote, Source string
	IntervalSec, StartSec, EndSec         int64
}

func (q NativeHistoryRange) validate() error {
	if q.Exchange == "" || q.Market == "" || q.Base == "" || q.Quote == "" || q.Source == "" {
		return errors.New("explicit market, asset, quote and source required")
	}
	switch q.IntervalSec {
	case 60, 600, 3600, 86400:
	default:
		return errors.New("unsupported native interval")
	}
	if q.StartSec < 0 || q.EndSec <= q.StartSec || q.StartSec%q.IntervalSec != 0 || q.EndSec%q.IntervalSec != 0 || (q.EndSec-q.StartSec)/q.IntervalSec > 10000 {
		return errors.New("invalid or excessive history window")
	}
	if q.Exchange == "bithumb" {
		return errors.New("Bithumb legacy ticker has no verified interval; query receipt history or hourly rollup")
	}
	return nil
}

// Decimal pointers distinguish missing values from source-reported zero.
type NativeHistoryObservation struct {
	Exchange, Market, Base, Quote, Source, Slot string
	IntervalSec                                 int64
	StartSec, EndSec                            *int64
	Open, High, Low                             *string
	Close                                       string
	BaseVolume, QuoteVolume                     *string
	VolumeSemantics                             string
	BoundaryVerified, Closed                    bool
	SourceOrder, ReceivedAtNS                   int64
	SourceDate, SourceTime                      *string
	NormalizationError                          string
}

const nativeHistoryColumns = `exchange_name,market,base,quote,source_kind,observation_slot,interval_sec,start_sec,end_sec,
 open_price::text,high_price::text,low_price::text,close_price::text,base_volume::text,quote_volume::text,
 volume_semantics,boundary_verified,closed,source_order,received_at_ns,source_date,source_time,normalization_error`

// ReadNativeReceiptHistory also supports Bithumb source-clock slots without
// manufacturing UTC candle boundaries. Results are bounded; history is not pruned.
func (r *Repository) ReadNativeReceiptHistory(ctx context.Context, exchange, base, quote string, fromNS, toNS int64, limit int) ([]NativeHistoryObservation, error) {
	if exchange == "" || base == "" || quote == "" || fromNS <= 0 || toNS <= fromNS || limit < 1 || limit > 10000 {
		return nil, errors.New("invalid receipt query bounds")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := r.pool.Query(ctx, `SELECT `+nativeHistoryColumns+` FROM native_ws_candle_observations WHERE exchange_name=$1 AND base=$2 AND quote=$3 AND received_at_ns>=$4 AND received_at_ns<$5 ORDER BY received_at_ns,market,interval_sec,source_kind,observation_slot LIMIT $6`, exchange, base, quote, fromNS, toNS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NativeHistoryObservation, 0)
	for rows.Next() {
		var v NativeHistoryObservation
		if err = rows.Scan(&v.Exchange, &v.Market, &v.Base, &v.Quote, &v.Source, &v.Slot, &v.IntervalSec, &v.StartSec, &v.EndSec, &v.Open, &v.High, &v.Low, &v.Close, &v.BaseVolume, &v.QuoteVolume, &v.VolumeSemantics, &v.BoundaryVerified, &v.Closed, &v.SourceOrder, &v.ReceivedAtNS, &v.SourceDate, &v.SourceTime, &v.NormalizationError); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type NativeIntervalTotal struct {
	ExpectedIntervals, ObservedIntervals, AmountIntervals, ClosedIntervals int64
	// ObservedQuoteSum is only known, nonoverlapping interval amounts. It is not
	// a complete-window claim. CompleteQuoteSum remains NULL if any slot is absent.
	ObservedQuoteSum, CompleteQuoteSum *string
}

func (r *Repository) SumNativeIntervals(ctx context.Context, q NativeHistoryRange) (NativeIntervalTotal, error) {
	return sumNativeIntervals(ctx, r.pool, q)
}

type nativeHistoryQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func sumNativeIntervals(ctx context.Context, db nativeHistoryQuerier, q NativeHistoryRange) (NativeIntervalTotal, error) {
	var v NativeIntervalTotal
	if err := q.validate(); err != nil {
		return v, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := db.QueryRow(ctx, `WITH amounts AS (
 SELECT start_sec,count(*) AS n,max(quote_volume) AS amount,bool_and(closed) AS closed,
 bool_and(normalization_error='' AND volume_semantics='INTERVAL' AND quote_volume IS NOT NULL) AS usable
 FROM native_ws_candle_observations
 WHERE exchange_name=$1 AND market=$2 AND base=$3 AND quote=$4 AND source_kind=$5
 AND interval_sec=$6 AND start_sec>=$7 AND end_sec<=$8 AND boundary_verified
 AND start_sec%interval_sec=0 GROUP BY start_sec
 ), totals AS (SELECT count(*) AS observed,count(*) FILTER(WHERE n=1 AND usable) AS usable,
 count(*) FILTER(WHERE n=1 AND closed AND usable) AS closed,
 sum(amount) FILTER(WHERE n=1 AND usable) AS amount FROM amounts)
 SELECT ($8::bigint-$7::bigint)/$6::bigint,observed,usable,closed,amount::text,
 CASE WHEN usable=($8::bigint-$7::bigint)/$6::bigint THEN amount::text END FROM totals`, q.Exchange, q.Market, q.Base, q.Quote, q.Source, q.IntervalSec, q.StartSec, q.EndSec).Scan(&v.ExpectedIntervals, &v.ObservedIntervals, &v.AmountIntervals, &v.ClosedIntervals, &v.ObservedQuoteSum, &v.CompleteQuoteSum)
	return v, err
}

type NativeIntervalComparison struct {
	Left, Right NativeIntervalTotal
	// RightMinusLeft is absent unless both fixed windows have every amount.
	RightMinusLeft *string
}

// CompareNativeIntervals uses one repeatable snapshot and exact decimal
// subtraction. Different quotes, assets or window lengths cannot be compared.
func (r *Repository) CompareNativeIntervals(ctx context.Context, left, right NativeHistoryRange) (NativeIntervalComparison, error) {
	var out NativeIntervalComparison
	if err := left.validate(); err != nil {
		return out, err
	}
	if err := right.validate(); err != nil {
		return out, err
	}
	if left.Base != right.Base || left.Quote != right.Quote || left.IntervalSec != right.IntervalSec || left.EndSec-left.StartSec != right.EndSec-right.StartSec {
		return out, errors.New("incomparable asset, quote or interval windows")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if out.Left, err = sumNativeIntervals(ctx, tx, left); err != nil {
		return out, err
	}
	if out.Right, err = sumNativeIntervals(ctx, tx, right); err != nil {
		return out, err
	}
	if out.Left.CompleteQuoteSum != nil && out.Right.CompleteQuoteSum != nil {
		l, lok := new(big.Rat).SetString(*out.Left.CompleteQuoteSum)
		rr, rok := new(big.Rat).SetString(*out.Right.CompleteQuoteSum)
		if !lok || !rok {
			return out, errors.New("invalid persisted decimal total")
		}
		scale := 0
		for _, s := range []string{*out.Left.CompleteQuoteSum, *out.Right.CompleteQuoteSum} {
			if p := strings.IndexByte(s, '.'); p >= 0 {
				scale = max(scale, len(s)-p-1)
			}
		}
		delta := new(big.Rat).Sub(rr, l).FloatString(scale)
		out.RightMinusLeft = &delta
	}
	return out, tx.Commit(ctx)
}

type NativeHistoryGap struct {
	StartSec, EndSec int64
	Reason           string
}

// Missing rows are UNKNOWN, including no-trade omissions. This cannot prove an
// exchange outage or restore exact trade coverage.
func (r *Repository) FindNativeHistoryGaps(ctx context.Context, q NativeHistoryRange) ([]NativeHistoryGap, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := r.pool.Query(ctx, `SELECT s,s+$6::bigint,
 CASE WHEN count(o.observation_slot)=0 THEN 'NOT_OBSERVED' ELSE 'AMOUNT_UNVERIFIED' END
 FROM generate_series($7::bigint,$8::bigint-$6::bigint,$6::bigint) AS slots(s)
 LEFT JOIN native_ws_candle_observations o ON exchange_name=$1 AND market=$2 AND base=$3 AND quote=$4 AND source_kind=$5 AND interval_sec=$6 AND start_sec=s AND end_sec=s+$6::bigint
 GROUP BY s HAVING count(o.observation_slot)<>1 OR NOT bool_and(normalization_error='' AND boundary_verified AND volume_semantics='INTERVAL' AND quote_volume IS NOT NULL)
 ORDER BY s`, q.Exchange, q.Market, q.Base, q.Quote, q.Source, q.IntervalSec, q.StartSec, q.EndSec)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]NativeHistoryGap, 0)
	for rows.Next() {
		var v NativeHistoryGap
		if err = rows.Scan(&v.StartSec, &v.EndSec, &v.Reason); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

// RepairNativeNormalization reprojects bounded existing payloads in-place.
// It records failed attempts without deleting evidence or starving later rows.
func (r *Repository) RepairNativeNormalization(ctx context.Context, limit int) (int64, error) {
	return repairNativeNormalization(ctx, r.pool, limit)
}

func repairNativeNormalization(ctx context.Context, db nativeObservationExecer, limit int) (int64, error) {
	if limit < 1 || limit > 512 {
		return 0, errors.New("invalid normalization repair limit")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tag, err := db.Exec(ctx, `WITH candidates AS MATERIALIZED (
 SELECT o.* FROM native_ws_candle_observations o
 LEFT JOIN LATERAL (SELECT max(attempted_at) AS last_try FROM native_normalization_attempts a
 WHERE (a.exchange_name,a.market,a.interval_sec,a.source_kind,a.observation_slot)=
 (o.exchange_name,o.market,o.interval_sec,o.source_kind,o.observation_slot)) a ON true
 WHERE normalization_error<>'' AND (last_try IS NULL OR last_try<clock_timestamp()-interval '1 minute')
 ORDER BY last_try NULLS FIRST,received_at_ns LIMIT $1 FOR UPDATE OF o SKIP LOCKED
 ), repaired AS (
 UPDATE native_ws_candle_observations o SET payload_json=o.payload_json FROM candidates c
 WHERE (o.exchange_name,o.market,o.interval_sec,o.source_kind,o.observation_slot)=
 (c.exchange_name,c.market,c.interval_sec,c.source_kind,c.observation_slot)
 RETURNING o.*)
 INSERT INTO native_normalization_attempts(exchange_name,market,interval_sec,source_kind,observation_slot,evidence_hash,error_text)
 SELECT exchange_name,market,interval_sec,source_kind,observation_slot,evidence_hash,normalization_error FROM repaired WHERE normalization_error=''`, limit)
	return tag.RowsAffected(), err
}
