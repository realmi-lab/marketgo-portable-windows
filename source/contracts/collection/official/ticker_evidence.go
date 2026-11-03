package official

import (
	"bytes"
	"encoding/gob"
	"errors"
	"io"

	"market-share-go/internal/domain"
)

// DomesticTickerEvidenceVersion changes whenever the private transport cannot
// be interpreted by an older decoder. Version 1's bare gob lost *0 versus nil.
const DomesticTickerEvidenceVersion = 2

type optionalTickerValue[T float64 | int64 | string] struct {
	Present bool
	Value   T
}

func tickerValue[T float64 | int64 | string](value *T) optionalTickerValue[T] {
	if value == nil {
		return optionalTickerValue[T]{}
	}
	return optionalTickerValue[T]{Present: true, Value: *value}
}

func (v optionalTickerValue[T]) pointer() (*T, error) {
	if !v.Present {
		var zero T
		if v.Value != zero {
			return nil, errors.New("absent ticker value contains data")
		}
		return nil, nil
	}
	value := v.Value
	return &value, nil
}

type tickerSnapshotValues struct {
	VolumeKRW           optionalTickerValue[float64]
	SharePercent        optionalTickerValue[float64]
	ExchangeTimestampMS optionalTickerValue[int64]
	LastError           optionalTickerValue[string]
}

type domesticTickerEvidence struct {
	Version             int
	Summary             DomesticTickerSummary
	TotalVolumeKRW      optionalTickerValue[float64]
	BithumbVolumeKRW    optionalTickerValue[float64]
	BithumbSharePercent optionalTickerValue[float64]
	Exchanges           map[domain.Exchange]tickerSnapshotValues
}

// EncodeDomesticTickerEvidence preserves all private fields through gob while
// explicitly carrying optional values. Gob alone cannot distinguish *0/nil or
// an empty pointed-to string. This envelope is self-contained; no public JSON
// copy is consulted to repair the decoded summary.
func EncodeDomesticTickerEvidence(summary DomesticTickerSummary) ([]byte, error) {
	wire := domesticTickerEvidence{
		Version: DomesticTickerEvidenceVersion, Summary: summary,
		TotalVolumeKRW:      tickerValue(summary.TotalVolumeKRW),
		BithumbVolumeKRW:    tickerValue(summary.BithumbVolumeKRW),
		BithumbSharePercent: tickerValue(summary.BithumbSharePercent),
		Exchanges:           make(map[domain.Exchange]tickerSnapshotValues, len(summary.Exchanges)),
	}
	for exchange, row := range summary.Exchanges {
		wire.Exchanges[exchange] = tickerSnapshotValues{
			VolumeKRW: tickerValue(row.VolumeKRW), SharePercent: tickerValue(row.SharePercent),
			ExchangeTimestampMS: tickerValue(row.ExchangeTimestampMS), LastError: tickerValue(row.LastError),
		}
	}
	var buffer bytes.Buffer
	err := gob.NewEncoder(&buffer).Encode(wire)
	return buffer.Bytes(), err
}

func DecodeDomesticTickerEvidence(raw []byte) (DomesticTickerSummary, error) {
	var wire domesticTickerEvidence
	decoder := gob.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&wire); err != nil {
		return DomesticTickerSummary{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return DomesticTickerSummary{}, errors.New("one complete domestic ticker evidence value required")
	}
	if wire.Version != DomesticTickerEvidenceVersion {
		return DomesticTickerSummary{}, errors.New("domestic ticker evidence version does not preserve optional values")
	}
	if len(wire.Exchanges) != len(wire.Summary.Exchanges) {
		return DomesticTickerSummary{}, errors.New("domestic ticker optional evidence is incomplete")
	}
	result := wire.Summary
	var err error
	if result.TotalVolumeKRW, err = wire.TotalVolumeKRW.pointer(); err != nil {
		return DomesticTickerSummary{}, err
	}
	if result.BithumbVolumeKRW, err = wire.BithumbVolumeKRW.pointer(); err != nil {
		return DomesticTickerSummary{}, err
	}
	if result.BithumbSharePercent, err = wire.BithumbSharePercent.pointer(); err != nil {
		return DomesticTickerSummary{}, err
	}
	for exchange, row := range result.Exchanges {
		values, exists := wire.Exchanges[exchange]
		if !exists {
			return DomesticTickerSummary{}, errors.New("domestic ticker optional exchange evidence is missing")
		}
		if row.VolumeKRW, err = values.VolumeKRW.pointer(); err != nil {
			return DomesticTickerSummary{}, err
		}
		if row.SharePercent, err = values.SharePercent.pointer(); err != nil {
			return DomesticTickerSummary{}, err
		}
		if row.ExchangeTimestampMS, err = values.ExchangeTimestampMS.pointer(); err != nil {
			return DomesticTickerSummary{}, err
		}
		if row.LastError, err = values.LastError.pointer(); err != nil {
			return DomesticTickerSummary{}, err
		}
		result.Exchanges[exchange] = row
	}
	return result, nil
}
