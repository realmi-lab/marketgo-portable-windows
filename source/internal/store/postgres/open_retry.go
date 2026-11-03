package postgres

import (
	"context"
	"math/rand/v2"
	"time"

	"market-share-go/internal/ingestlog"
)

func waitDatabaseReady(ctx context.Context, ping func(context.Context) error, wait func(context.Context, time.Duration) error) error {
	delay := 250 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		attempt, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := ping(attempt)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !ingestlog.IsRetryable(classifyIngestionError(err)) {
			return err
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
		delay = min(delay*2, 5*time.Second)
	}
}

func waitDatabaseRetry(ctx context.Context, delay time.Duration) error {
	// Spread independently restarted services across the capped retry interval.
	timer := time.NewTimer(delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
