// Package collection defines durable input envelopes shared by producer and replay consumer.
package collection

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"market-share-go/contracts/collection/official"
	"market-share-go/contracts/collection/registry"
	"market-share-go/contracts/collection/stream"
	"market-share-go/internal/domain"
	"time"
)

type FramePayload = collector.FrameEnvelope
type RegistryPayload struct {
	Snapshot  registry.Snapshot
	Markets   []domain.MarketPair
	Exchanges []domain.Exchange
}
type OfficialPayload struct {
	Source       string
	Domestic     *official.DomesticTickerSummary
	Binance      *official.BinanceTicker24hSummary
	Version      int
	FullDomestic []byte
}

// MarshalJSON retains private numeric evidence and update-watermark fields.
func (p OfficialPayload) MarshalJSON() ([]byte, error) {
	type wire OfficialPayload
	p.Version = official.DomesticTickerEvidenceVersion
	if p.Domestic != nil {
		var err error
		p.FullDomestic, err = official.EncodeDomesticTickerEvidence(*p.Domestic)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(wire(p))
}
func (p OfficialPayload) DecodeDomestic() (*official.DomesticTickerSummary, error) {
	if p.Version == 1 {
		// Previously committed input remains readable without a migration. The
		// bare gob cannot recover lost pointer presence: retain nil as unknown,
		// never infer zero from the duplicate public JSON or a default value.
		var summary official.DomesticTickerSummary
		decoder := gob.NewDecoder(bytes.NewReader(p.FullDomestic))
		if err := decoder.Decode(&summary); err != nil {
			return nil, err
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("one complete legacy domestic evidence value required")
		}
		return &summary, nil
	}
	if p.Version != official.DomesticTickerEvidenceVersion || len(p.FullDomestic) == 0 {
		return nil, fmt.Errorf("missing versioned domestic evidence")
	}
	summary, err := official.DecodeDomesticTickerEvidence(p.FullDomestic)
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

const healthPayloadVersion = 2

// Version 2 retains private exchange fields and optional zero values in gob,
// and protects market activity keys in an encoded JSON envelope. Older
// consumers reject this version rather than silently dropping activity.
type HealthPayload struct {
	Version   int
	At        time.Time
	Exchanges []byte
	Activity  map[string]collector.MarketActivity
}

// Market activity keys contain the internal exchange\x00market separator.
// PostgreSQL JSONB cannot represent NUL, even escaped as \u0000. Keep the
// complete activity JSON in a versioned byte envelope (base64 on the wire),
// preserving both the original keys and nil versus zero age evidence.
type healthWire struct {
	Version         int
	At              time.Time
	Exchanges       []byte
	ActivityVersion int                                 `json:",omitempty"`
	ActivityJSON    []byte                              `json:",omitempty"`
	Activity        map[string]collector.MarketActivity `json:",omitempty"`
}

func (p HealthPayload) MarshalJSON() ([]byte, error) {
	if p.Version != 1 && p.Version != healthPayloadVersion {
		return nil, fmt.Errorf("unsupported health payload version %d", p.Version)
	}
	if p.Version == 1 {
		health, err := p.DecodeHealth()
		if err != nil {
			return nil, err
		}
		p, err = EncodeHealth(p.At, health, p.Activity)
		if err != nil {
			return nil, err
		}
	}
	activity, err := json.Marshal(p.Activity)
	if err != nil {
		return nil, err
	}
	return json.Marshal(healthWire{Version: healthPayloadVersion, At: p.At, Exchanges: p.Exchanges, ActivityVersion: 1, ActivityJSON: activity})
}

func (p *HealthPayload) UnmarshalJSON(raw []byte) error {
	var wire healthWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	activity := wire.Activity
	switch wire.Version {
	case 1:
		if wire.ActivityVersion != 0 || len(wire.ActivityJSON) != 0 {
			return fmt.Errorf("unexpected activity envelope in legacy health payload")
		}
	case healthPayloadVersion:
		if wire.ActivityVersion != 1 || len(wire.ActivityJSON) == 0 || wire.Activity != nil {
			return fmt.Errorf("invalid versioned market activity evidence")
		}
		if err := json.Unmarshal(wire.ActivityJSON, &activity); err != nil {
			return fmt.Errorf("decode market activity evidence: %w", err)
		}
	default:
		return fmt.Errorf("unsupported health payload version %d", wire.Version)
	}
	*p = HealthPayload{Version: wire.Version, At: wire.At, Exchanges: wire.Exchanges, Activity: activity}
	return nil
}

func EncodeHealth(at time.Time, exchanges map[domain.Exchange]collector.ExchangeHealth, activity map[string]collector.MarketActivity) (HealthPayload, error) {
	var b bytes.Buffer
	wire := healthExchangesWire{Values: exchanges, Pointers: make(map[domain.Exchange][7]healthOptionalFloat, len(exchanges))}
	for exchange, health := range exchanges {
		var pointers [7]healthOptionalFloat
		for i, pointer := range healthFloatPointers(&health) {
			if *pointer != nil {
				pointers[i] = healthOptionalFloat{Present: true, Value: **pointer}
			}
		}
		wire.Pointers[exchange] = pointers
	}
	err := gob.NewEncoder(&b).Encode(wire)
	return HealthPayload{Version: healthPayloadVersion, At: at, Exchanges: b.Bytes(), Activity: activity}, err
}
func (p HealthPayload) DecodeHealth() (map[domain.Exchange]collector.ExchangeHealth, error) {
	if p.Version != 1 && p.Version != healthPayloadVersion {
		return nil, fmt.Errorf("unsupported health payload version %d", p.Version)
	}
	var result map[domain.Exchange]collector.ExchangeHealth
	decoder := gob.NewDecoder(bytes.NewReader(p.Exchanges))
	if p.Version == 1 {
		err := decoder.Decode(&result)
		return result, err
	}
	var wire healthExchangesWire
	if err := decoder.Decode(&wire); err != nil {
		return nil, err
	}
	if len(wire.Values) != len(wire.Pointers) {
		return nil, fmt.Errorf("incomplete optional exchange health evidence")
	}
	for exchange, health := range wire.Values {
		pointers, ok := wire.Pointers[exchange]
		if !ok {
			return nil, fmt.Errorf("missing optional exchange health evidence")
		}
		for i, pointer := range healthFloatPointers(&health) {
			*pointer = nil
			if pointers[i].Present {
				value := pointers[i].Value
				*pointer = &value
			}
		}
		wire.Values[exchange] = health
	}
	return wire.Values, nil
}

// Gob flattens *float64(0) to nil in a struct. Explicit presence retains the
// distinction without inferring values that legacy version 1 already lost.
type healthOptionalFloat struct {
	Present bool
	Value   float64
}

type healthExchangesWire struct {
	Values   map[domain.Exchange]collector.ExchangeHealth
	Pointers map[domain.Exchange][7]healthOptionalFloat
}

// The order is part of health payload version 2.
func healthFloatPointers(health *collector.ExchangeHealth) [7]**float64 {
	return [7]**float64{
		&health.ConnectedMarketPercent, &health.FreshMarketPercent, &health.FreshSubscriptionPercent,
		&health.MaxMessageAgeSec, &health.MaxDataAgeSec, &health.MaxTradeAgeSec, &health.MaxOrderBookAgeSec,
	}
}
