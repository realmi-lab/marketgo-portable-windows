package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"market-share-go/internal/notice"
	"sort"
)

// Lock/check the complete conditional batch before revision, audit or heartbeat
// writes. READ COMMITTED observes a competing updater after the row lock waits.
func checkHermesNoticePreconditions(ctx context.Context, tx pgx.Tx, request notice.HermesIngestRequest) error {
	if request.ExpectedRevisions == nil {
		return nil
	}
	if len(request.Items) == 0 || len(request.ExpectedRevisions) != len(request.Items) {
		return fmt.Errorf("%w: expected_revisions must match every item", notice.ErrHermesIngestInvalid)
	}
	type identity struct{ key, exchange, id string }
	keys := make([]identity, 0, len(request.Items))
	seen := map[string]bool{}
	for _, item := range request.Items {
		key := item.Exchange + ":" + item.NoticeID
		if seen[key] || request.ExpectedRevisions[key] == "" {
			return fmt.Errorf("%w: expected_revisions item mismatch", notice.ErrHermesIngestInvalid)
		}
		seen[key] = true
		keys = append(keys, identity{key, item.Exchange, item.NoticeID})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].key < keys[j].key })
	for _, key := range keys {
		var version string
		err := tx.QueryRow(ctx, `SELECT xmin::text FROM exchange_notices WHERE exchange_name=$1 AND notice_id=$2 FOR UPDATE`, key.exchange, key.id).Scan(&version)
		if err == pgx.ErrNoRows || (err == nil && version != request.ExpectedRevisions[key.key]) {
			return fmt.Errorf("%w: notice changed or disappeared; reload before analysis", notice.ErrHermesIngestConflict)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
