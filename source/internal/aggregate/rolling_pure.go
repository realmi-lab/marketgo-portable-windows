package aggregate

import "errors"

type pureRollingEngine struct {
	windows  []int64
	rows     map[int64]map[VolumeKey]Totals
	prepared []Totals
	eligible []bool
}

func newPureRollingEngine(windows []int64) (rollingEngine, error) {
	rows := make(map[int64]map[VolumeKey]Totals, len(windows))
	normalized := make([]int64, 0, len(windows))
	for _, window := range windows {
		if _, exists := rows[window]; exists {
			continue
		}
		rows[window] = make(map[VolumeKey]Totals)
		normalized = append(normalized, window)
	}
	return &pureRollingEngine{
		windows: normalized, rows: rows,
		prepared: make([]Totals, len(normalized)), eligible: make([]bool, len(normalized)),
	}, nil
}

func (e *pureRollingEngine) Ingest(eventSecond, currentSecond int64, key VolumeKey, value Totals) error {
	if eventSecond <= 0 || currentSecond <= 0 {
		return errors.New("pure rolling engine seconds must be positive")
	}
	// The engine has one owner goroutine, so reusable scratch space preserves
	// all-or-nothing ingestion without allocating for every trade.
	for index, window := range e.windows {
		if eventSecond <= currentSecond-window {
			e.eligible[index] = false
			continue
		}
		total := e.rows[window][key]
		if !total.CheckedAdd(value) {
			return errors.New("pure rolling engine total overflow")
		}
		e.prepared[index] = total
		e.eligible[index] = true
	}
	for index, window := range e.windows {
		if !e.eligible[index] {
			continue
		}
		e.rows[window][key] = e.prepared[index]
	}
	return nil
}

func (e *pureRollingEngine) SubtractBatch(window int64, values map[VolumeKey]Totals) error {
	rows, ok := e.rows[window]
	if !ok {
		return errors.New("pure rolling engine unsupported window")
	}
	for key, value := range values {
		total, exists := rows[key]
		if !exists {
			continue
		}
		total.Subtract(value)
		if total.Empty() {
			delete(rows, key)
		} else {
			rows[key] = total
		}
	}
	return nil
}

func (e *pureRollingEngine) Snapshot(window int64) (map[VolumeKey]Totals, bool, error) {
	rows, ok := e.rows[window]
	if !ok {
		return nil, false, nil
	}
	result := make(map[VolumeKey]Totals, len(rows))
	for key, value := range rows {
		result[key] = value
	}
	return result, true, nil
}

func (*pureRollingEngine) Name() string       { return "go-oracle" }
func (*pureRollingEngine) ABIVersion() uint32 { return 0 }
func (*pureRollingEngine) Close()             {}
