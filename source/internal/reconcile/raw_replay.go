package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"market-share-go/internal/domain"
)

const (
	RawReplayClockJitterMS = int64(5_000)
	RawReplayQueueCapacity = 8
)

type RawReplayCandidate struct {
	Exchange        domain.Exchange     `json:"exchange"`
	MinuteStart     domain.UnixSecond   `json:"minute_start"`
	Markets         []domain.MarketPair `json:"markets"`
	ReceivedStartNS int64               `json:"received_start_ns"`
	ReceivedEndNS   int64               `json:"received_end_ns"`
	// EvidenceFingerprint distinguishes a changed gap snapshot even when its
	// exchange, minute, market set, and journal bounds are unchanged. It is an
	// in-process scheduling identity, not part of the replay API contract.
	EvidenceFingerprint string `json:"-"`
}

// SlotKey is the serial execution boundary. One raw journal scan per exchange
// minute runs at a time even when a later gap snapshot changes the market set.
func (c RawReplayCandidate) SlotKey() string {
	return string(c.Exchange) + "\x00" + integer(int64(c.MinuteStart))
}

func (c RawReplayCandidate) Key() string {
	key := c.SlotKey()
	for _, market := range c.Markets {
		key += "\x00" + market.Key()
	}
	return key
}

// GenerationKey identifies the exact evidence that requested a replay. Callers
// normalize the candidate first so equivalent market sets have one identity.
func (c RawReplayCandidate) GenerationKey() string {
	hash := sha256.New()
	for _, value := range []string{
		c.Key(),
		integer(c.ReceivedStartNS),
		integer(c.ReceivedEndNS),
		c.EvidenceFingerprint,
	} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (c *RawReplayCandidate) Normalize() error {
	if c == nil || !c.Exchange.Valid() {
		return errors.New("raw replay candidate exchange is invalid")
	}
	if err := validateMinute(c.MinuteStart); err != nil {
		return err
	}
	if len(c.Markets) == 0 || c.ReceivedStartNS <= 0 || c.ReceivedEndNS < c.ReceivedStartNS {
		return errors.New("raw replay candidate range or market scope is invalid")
	}
	seen := make(map[string]struct{}, len(c.Markets))
	markets := make([]domain.MarketPair, 0, len(c.Markets))
	for _, market := range c.Markets {
		if market.Exchange != c.Exchange || market.Scope != domain.ScopeDomestic || !strings.EqualFold(market.Quote, "KRW") {
			return errors.New("raw replay candidate contains a market outside its direct KRW exchange scope")
		}
		if _, exists := seen[market.Key()]; exists {
			continue
		}
		seen[market.Key()] = struct{}{}
		markets = append(markets, market)
	}
	sort.Slice(markets, func(i, j int) bool { return markets[i].Key() < markets[j].Key() })
	c.Markets = markets
	return nil
}

func mergeRawReplayCandidates(left, right RawReplayCandidate) RawReplayCandidate {
	result := left
	if right.ReceivedStartNS < result.ReceivedStartNS {
		result.ReceivedStartNS = right.ReceivedStartNS
	}
	if right.ReceivedEndNS > result.ReceivedEndNS {
		result.ReceivedEndNS = right.ReceivedEndNS
	}
	markets := make(map[string]domain.MarketPair, len(left.Markets)+len(right.Markets))
	for _, candidate := range []RawReplayCandidate{left, right} {
		for _, market := range candidate.Markets {
			markets[market.Key()] = market
		}
	}
	result.Markets = result.Markets[:0]
	for _, market := range markets {
		result.Markets = append(result.Markets, market)
	}
	_ = result.Normalize()

	generations := []string{left.GenerationKey(), right.GenerationKey()}
	sort.Strings(generations)
	hash := sha256.New()
	for _, generation := range generations {
		_, _ = hash.Write([]byte(generation))
		_, _ = hash.Write([]byte{0})
	}
	result.EvidenceFingerprint = hex.EncodeToString(hash.Sum(nil))
	return result
}

type RawReplayResult struct {
	FramesRead      int `json:"frames_read"`
	TradesSubmitted int `json:"trades_submitted"`
}

type RawReplayFunc func(context.Context, RawReplayCandidate) (RawReplayResult, error)
