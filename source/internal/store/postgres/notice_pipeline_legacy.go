package postgres

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/notice"
)

func pipelineLegacyAdmission(ctx context.Context, tx pgx.Tx, req notice.HermesIngestRequest) error {
	var installed bool
	if err := tx.QueryRow(ctx, "SELECT to_regclass('notice_v2_sources') IS NOT NULL").Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return nil
	}
	keys := map[string]bool{}
	for _, exchange := range req.ScannedExchanges {
		keys[exchange] = true
	}
	for _, item := range req.Items {
		keys[item.Exchange] = true
	}
	exchanges := make([]string, 0, len(keys))
	for exchange := range keys {
		exchanges = append(exchanges, exchange)
	}
	sort.Strings(exchanges)
	for _, exchange := range exchanges {
		rows, err := tx.Query(ctx, "SELECT collector_mode FROM notice_v2_sources WHERE exchange=$1 FOR UPDATE", exchange)
		if err != nil {
			return err
		}
		for rows.Next() {
			var mode string
			if err = rows.Scan(&mode); err != nil {
				rows.Close()
				return err
			}
			if mode == "V2" {
				rows.Close()
				return notice.ErrPipelineConflict
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
