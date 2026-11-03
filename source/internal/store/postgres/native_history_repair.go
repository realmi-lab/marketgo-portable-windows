package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"market-share-go/services/collector/candlewire"
)

type NativeHistoryRepair struct {
	ID      int64
	Attempt int
	Range   NativeHistoryRange
}

func (r *Repository) EnqueueNativeHistoryRepair(ctx context.Context, q NativeHistoryRange) (int64, error) {
	if err := q.validate(); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO native_history_repairs(exchange_name,market,base,quote,source_kind,interval_sec,start_sec,end_sec)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(exchange_name,market,source_kind,interval_sec,start_sec,end_sec)
 DO UPDATE SET updated_at=native_history_repairs.updated_at
 WHERE native_history_repairs.base=excluded.base AND native_history_repairs.quote=excluded.quote RETURNING id`, q.Exchange, q.Market, q.Base, q.Quote, q.Source, q.IntervalSec, q.StartSec, q.EndSec).Scan(&id)
	return id, err
}

// Claim uses a DB-clock lease and monotonically increasing attempt token.
// An expired worker cannot finish a replacement worker's attempt.
func (r *Repository) ClaimNativeHistoryRepair(ctx context.Context) (*NativeHistoryRepair, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var job NativeHistoryRepair
	q := &job.Range
	err = tx.QueryRow(ctx, `SELECT id,attempts,exchange_name,market,base,quote,source_kind,interval_sec,start_sec,end_sec
 FROM native_history_repairs WHERE (status IN ('PENDING','RETRY') AND next_attempt_at<=clock_timestamp())
 OR (status='RUNNING' AND lease_until<clock_timestamp())
 ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&job.ID, &job.Attempt, &q.Exchange, &q.Market, &q.Base, &q.Quote, &q.Source, &q.IntervalSec, &q.StartSec, &q.EndSec)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE native_history_repair_attempts SET status='LEASE_EXPIRED',error_text='worker lease expired',finished_at=clock_timestamp() WHERE repair_id=$1 AND attempt=$2 AND finished_at IS NULL`, job.ID, job.Attempt); err != nil {
		return nil, err
	}
	job.Attempt++
	if _, err = tx.Exec(ctx, `UPDATE native_history_repairs SET status='RUNNING',attempts=$2,lease_until=clock_timestamp()+interval '30 seconds',updated_at=clock_timestamp() WHERE id=$1`, job.ID, job.Attempt); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO native_history_repair_attempts(repair_id,attempt,status) VALUES($1,$2,'RUNNING')`, job.ID, job.Attempt); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &job, nil
}

// FinishNativeHistoryRepair atomically writes source-validated absolute candles
// and the attempt outcome. COMPLETE means interval amounts exist, not that WS
// trade details or uninterrupted coverage were recovered. No synthetic zeroes.
func (r *Repository) FinishNativeHistoryRepair(ctx context.Context, job NativeHistoryRepair, candles []wscandle.Candle, status, message string) error {
	return r.finishNativeHistoryRepair(ctx, job, candles, status, message, nil)
}

// A worker may commit a complete bounded chunk while the original job remains
// RETRY. Persisted usable observations are its checkpoint; the fixed job range
// and public all-or-nothing Finish contract remain unchanged.
func (r *Repository) finishNativeHistoryRepair(ctx context.Context, job NativeHistoryRepair, candles []wscandle.Candle, status, message string, chunk *NativeHistoryRange) error {
	if status != "COMPLETE" && status != "RETRY" && status != "UNAVAILABLE" {
		return errors.New("invalid repair outcome")
	}
	if status != "COMPLETE" && (message == "" || len(candles) != 0) {
		return errors.New("failed repair requires reason and no output")
	}
	if len(candles) > 512 {
		return errors.New("repair batch exceeds 512")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var q NativeHistoryRange
	err = tx.QueryRow(ctx, `SELECT exchange_name,market,base,quote,source_kind,interval_sec,start_sec,end_sec
 FROM native_history_repairs WHERE id=$1 AND attempts=$2 AND status='RUNNING' AND lease_until>clock_timestamp() FOR UPDATE`, job.ID, job.Attempt).Scan(&q.Exchange, &q.Market, &q.Base, &q.Quote, &q.Source, &q.IntervalSec, &q.StartSec, &q.EndSec)
	if err != nil {
		return fmt.Errorf("repair lease unavailable: %w", err)
	}
	if err = q.validate(); err != nil {
		return err
	}
	scope := q
	if chunk != nil {
		scope.StartSec, scope.EndSec = chunk.StartSec, chunk.EndSec
		if status != "COMPLETE" || scope != *chunk || scope.validate() != nil || scope.StartSec < q.StartSec || scope.EndSec > q.EndSec || (scope.EndSec-scope.StartSec)/scope.IntervalSec > 512 {
			return errors.New("invalid repair chunk")
		}
	}
	for _, c := range candles {
		if err = c.Validate(); err != nil {
			return err
		}
		if c.Exchange != q.Exchange || c.Market != q.Market || c.Asset != q.Base || c.Quote != q.Quote || c.Source != q.Source || int64(c.Interval/time.Second) != q.IntervalSec || !c.BoundaryVerified || c.Start.Unix() < scope.StartSec || c.End.Unix() > scope.EndSec || c.Start.Unix()%q.IntervalSec != 0 {
			return errors.New("repair evidence outside fixed source/range")
		}
		if err = r.saveNativeWSObservationUsing(ctx, tx, c.Exchange, c.Market, c.Asset, c.Quote, q.IntervalSec, c.Start.Unix(), c.End.Unix(), c.Close, c.Source, c.BoundaryVerified, c.Closed, time.Now(), c); err != nil {
			return err
		}
	}
	if chunk != nil {
		total, err := sumNativeIntervals(ctx, tx, scope)
		if err != nil {
			return err
		}
		if total.CompleteQuoteSum == nil {
			return errors.New("repair chunk still missing or has unverified amounts")
		}
	}
	progress := false
	if status == "COMPLETE" {
		var complete bool
		err = tx.QueryRow(ctx, `SELECT count(*)=$9 AND count(DISTINCT start_sec)=$9 AND COALESCE(bool_and(normalization_error='' AND volume_semantics='INTERVAL' AND quote_volume IS NOT NULL AND boundary_verified),false)
 FROM native_ws_candle_observations WHERE exchange_name=$1 AND market=$2 AND base=$3 AND quote=$4 AND source_kind=$5 AND interval_sec=$6 AND start_sec>=$7 AND end_sec<=$8 AND start_sec%interval_sec=0`, q.Exchange, q.Market, q.Base, q.Quote, q.Source, q.IntervalSec, q.StartSec, q.EndSec, (q.EndSec-q.StartSec)/q.IntervalSec).Scan(&complete)
		if err != nil {
			return err
		}
		if !complete {
			if chunk == nil {
				return errors.New("repair range still missing or has unverified amounts")
			}
			status, message, progress = "RETRY", "verified chunk committed; remaining intervals pending", true
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE native_history_repairs SET status=$3,last_error=$4,lease_until=NULL,
 next_attempt_at=CASE WHEN $5 THEN clock_timestamp() ELSE clock_timestamp()+make_interval(secs=>least(3600,attempts*30)) END,updated_at=clock_timestamp() WHERE id=$1 AND attempts=$2`, job.ID, job.Attempt, status, message, progress); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE native_history_repair_attempts SET status=$3,error_text=$4,finished_at=clock_timestamp() WHERE repair_id=$1 AND attempt=$2`, job.ID, job.Attempt, status, message); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Fetch is an explicitly supplied historical adapter. No REST endpoint or
// recent-N snapshot is assumed to support missing historical native intervals.
type NativeHistoryFetcher interface {
	FetchNativeHistory(context.Context, NativeHistoryRange) ([]wscandle.Candle, error)
}

var ErrNativeHistoryUnavailable = errors.New("native historical source unavailable")

func (r *Repository) RunNativeHistoryRepair(ctx context.Context, fetch NativeHistoryFetcher) (bool, error) {
	if fetch == nil {
		return false, errors.New("historical adapter required")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	job, err := r.ClaimNativeHistoryRepair(ctx)
	if err != nil || job == nil {
		return false, err
	}
	gaps, err := r.FindNativeHistoryGaps(ctx, job.Range)
	if err != nil {
		return true, errors.Join(err, r.FinishNativeHistoryRepair(ctx, *job, nil, "RETRY", err.Error()))
	}
	if len(gaps) == 0 {
		return true, r.FinishNativeHistoryRepair(ctx, *job, nil, "COMPLETE", "")
	}
	scope := job.Range
	scope.StartSec = gaps[0].StartSec
	scope.EndSec = scope.StartSec + min(int64(512), (job.Range.EndSec-scope.StartSec)/scope.IntervalSec)*scope.IntervalSec
	fetchCtx, done := context.WithTimeout(ctx, 15*time.Second)
	candles, fetchErr := fetch.FetchNativeHistory(fetchCtx, scope)
	done()
	if fetchErr != nil {
		status := "RETRY"
		if errors.Is(fetchErr, ErrNativeHistoryUnavailable) {
			status = "UNAVAILABLE"
		}
		return true, r.FinishNativeHistoryRepair(ctx, *job, nil, status, fetchErr.Error())
	}
	if err = r.finishNativeHistoryRepair(ctx, *job, candles, "COMPLETE", "", &scope); err != nil {
		recordErr := r.FinishNativeHistoryRepair(ctx, *job, nil, "RETRY", err.Error())
		return true, errors.Join(err, recordErr)
	}
	return true, nil
}
