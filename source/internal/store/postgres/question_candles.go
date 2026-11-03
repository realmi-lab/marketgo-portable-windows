package postgres

import (
	"context"
	"errors"
	"time"
)

// QuestionCandleMarket contains the same market in both complete windows.
// A missing candle is never treated as no trading. Registry counts describe
// current membership, not proof of historical exchange completeness.
type QuestionCandleMarket struct {
	Exchange          string
	Market            string
	Base              string
	IntervalSec       int64
	Previous, Current float64
}

func (r *Repository) QuestionCandleMarkets(ctx context.Context, start, middle, end int64) ([]QuestionCandleMarket, error) {
	if start <= 0 || middle-start != end-middle || end-middle > 86400 || start%1800 != 0 || middle%1800 != 0 || end%1800 != 0 || end > time.Now().Unix()-90 {
		return nil, errors.New("invalid closed candle comparison bounds")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := r.pool.Query(ctx, `SELECT c.exchange_name,c.market,c.base,c.interval_sec,
 sum(c.quote_volume) FILTER(WHERE c.start_sec<$2),sum(c.quote_volume) FILTER(WHERE c.start_sec>=$2)
 FROM official_interval_candles c
 JOIN market_registry_current m ON m.exchange_name=c.exchange_name AND m.market=c.market AND m.base=c.base AND m.quote=c.quote AND m.active
 WHERE c.quote='KRW' AND c.start_sec>=$1 AND c.start_sec+c.interval_sec<=$3
 AND ((c.exchange_name IN ('upbit','bithumb') AND c.interval_sec=600) OR (c.exchange_name='coinone' AND c.interval_sec=900))
 GROUP BY c.exchange_name,c.market,c.base,c.interval_sec
 HAVING count(*) FILTER(WHERE c.start_sec<$2)*c.interval_sec=$2::bigint-$1::bigint
 AND count(*) FILTER(WHERE c.start_sec>=$2)*c.interval_sec=$3::bigint-$2::bigint
 AND bool_and(c.quote_volume IS NOT NULL)
 ORDER BY c.exchange_name,c.market`, start, middle, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuestionCandleMarket{}
	for rows.Next() {
		var v QuestionCandleMarket
		if err := rows.Scan(&v.Exchange, &v.Market, &v.Base, &v.IntervalSec, &v.Previous, &v.Current); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
