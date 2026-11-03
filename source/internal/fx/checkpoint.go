package fx

import (
	"encoding/json"
	"errors"
	"market-share-go/internal/domain"
	"sort"
)

type checkpointEdge struct {
	From, To, Source   string
	Rate               float64
	At                 domain.UnixMilli
	Priority           uint8
	Primary, Secondary uint64
}
type oracleCheckpoint struct {
	Version                   int
	MaxAge                    int64
	MaxHops                   *int
	Edges, Volume, Executable []checkpointEdge
	Quotes                    []string
	MarketCount               int
	UpdatedAt                 domain.UnixMilli
}

// Checkpoint preserves observations and tie-breaking versions, not merely a
// currently resolved rate. Replay uses the original receive times and sources.
func (o *Oracle) Checkpoint() ([]byte, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	c := oracleCheckpoint{Version: 1, MaxAge: o.maxAge, MaxHops: copyMaxHops(o.maxHops), MarketCount: o.marketCount, UpdatedAt: o.updatedAt}
	for q := range o.quoteAssets {
		c.Quotes = append(c.Quotes, q)
	}
	sort.Strings(c.Quotes)
	c.Edges = checkpointEdges(o.edges)
	c.Volume = checkpointEdges(o.volumeEdges)
	c.Executable = checkpointEdges(o.executableEdges)
	return json.Marshal(c)
}

// checkpointEdges keeps one row per venue, its newest observation, so the
// checkpoint format is unchanged and a previous release can still restore it.
// The short history behind each venue only serves conversions within seconds
// of receipt and is rebuilt by the live stream after a restart.
func checkpointEdges(edges edgeSet) []checkpointEdge {
	var rows []checkpointEdge
	for key, sources := range edges {
		for source, history := range sources {
			value, ok := history.newest()
			if !ok {
				continue
			}
			rows = append(rows, checkpointEdge{key.source, key.destination, source, value.rate, value.time, value.version.priority, value.version.primary, value.version.secondary})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Source < b.Source
	})
	return rows
}
func restoreCheckpointEdges(rows []checkpointEdge) (edgeSet, error) {
	result := make(edgeSet)
	for _, row := range rows {
		if row.From == "" || row.To == "" || row.From == row.To || row.Source == "" || !positive(row.Rate) || row.At <= 0 {
			return nil, errors.New("invalid FX checkpoint observation")
		}
		key := edgeKey{row.From, row.To}
		if result[key] == nil {
			result[key] = make(map[string]venueObservations)
		}
		if _, exists := result[key][row.Source]; exists {
			return nil, errors.New("duplicate FX checkpoint observation")
		}
		result[key][row.Source] = venueObservations{{row.Rate, row.At, observationVersion{row.Priority, row.Primary, row.Secondary}}}
	}
	return result, nil
}
func (o *Oracle) RestoreCheckpoint(data []byte) error {
	var c oracleCheckpoint
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	if c.Version != 1 || c.MaxAge <= 0 || c.MarketCount < 0 {
		return errors.New("invalid FX checkpoint")
	}
	edges, err := restoreCheckpointEdges(c.Edges)
	if err != nil {
		return err
	}
	volume, err := restoreCheckpointEdges(c.Volume)
	if err != nil {
		return err
	}
	executable, err := restoreCheckpointEdges(c.Executable)
	if err != nil {
		return err
	}
	quotes := make(map[string]struct{})
	for _, q := range c.Quotes {
		if q == "" {
			return errors.New("invalid FX quote asset")
		}
		quotes[q] = struct{}{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if c.MaxAge != o.maxAge || (c.MaxHops == nil) != (o.maxHops == nil) || (c.MaxHops != nil && *c.MaxHops != *o.maxHops) {
		return errors.New("FX checkpoint policy differs from configured oracle")
	}
	o.edges = edges
	o.volumeEdges = volume
	o.executableEdges = executable
	o.quoteAssets = quotes
	o.marketCount = c.MarketCount
	o.updatedAt = c.UpdatedAt
	return nil
}
