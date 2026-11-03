package reconcile

import (
	"errors"
	"strings"
	"time"

	"market-share-go/internal/domain"
)

const (
	SealComplete = "COMPLETE"
	SealPartial  = "PARTIAL"
	SealMissing  = "MISSING"
)

// MinuteSeal is one compact exchange-level proof for a closed minute. Markets
// with accepted trades keep their normal ws_trade_minutes rows. When this seal
// is COMPLETE, the remaining registry markets are observed zeroes rather than
// fabricated per-market rows. A non-complete seal keeps those absent markets
// UNKNOWN so a transport or parser gap can never be converted to zero.
type MinuteSeal struct {
	Exchange             domain.Exchange   `json:"exchange"`
	MinuteStart          domain.UnixSecond `json:"minute_start"`
	Status               string            `json:"status"`
	CoverageSeconds      int64             `json:"coverage_seconds"`
	GapSeconds           int64             `json:"gap_seconds"`
	RegistryMarketCount  int64             `json:"registry_market_count"`
	ObservedMarketCount  int64             `json:"observed_market_count"`
	ObservedZeroCount    int64             `json:"observed_zero_count"`
	UnknownMarketCount   int64             `json:"unknown_market_count"`
	GapMarketCount       int64             `json:"gap_market_count"`
	ScheduledMarketCount int64             `json:"scheduled_market_count"`
	RegistryFingerprint  string            `json:"registry_fingerprint"`
	SealedAt             time.Time         `json:"sealed_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
}

func (s *MinuteSeal) Normalize() error {
	if s == nil {
		return errors.New("minute seal is nil")
	}
	if !s.Exchange.Valid() {
		return errors.New("minute seal exchange is invalid")
	}
	if err := validateMinute(s.MinuteStart); err != nil {
		return err
	}
	s.Status = strings.ToUpper(strings.TrimSpace(s.Status))
	if s.Status != SealComplete && s.Status != SealPartial && s.Status != SealMissing {
		return errors.New("minute seal status is invalid")
	}
	if s.CoverageSeconds < 0 || s.CoverageSeconds > MinuteSeconds ||
		s.GapSeconds < 0 || s.GapSeconds > MinuteSeconds ||
		s.CoverageSeconds+s.GapSeconds != MinuteSeconds {
		return errors.New("minute seal coverage and gap must partition 60 seconds")
	}
	counts := []int64{s.RegistryMarketCount, s.ObservedMarketCount, s.ObservedZeroCount,
		s.UnknownMarketCount, s.GapMarketCount, s.ScheduledMarketCount}
	for _, count := range counts {
		if count < 0 {
			return errors.New("minute seal counts must be non-negative")
		}
	}
	if s.ObservedMarketCount > s.RegistryMarketCount ||
		s.GapMarketCount > s.RegistryMarketCount ||
		s.ScheduledMarketCount > s.RegistryMarketCount {
		return errors.New("minute seal member count exceeds registry count")
	}
	absent := s.RegistryMarketCount - s.ObservedMarketCount
	switch s.Status {
	case SealComplete:
		if s.CoverageSeconds != MinuteSeconds || s.GapSeconds != 0 ||
			s.ObservedZeroCount != absent || s.UnknownMarketCount != 0 {
			return errors.New("complete minute seal must classify every absent market as observed zero")
		}
	case SealPartial, SealMissing:
		if s.ObservedZeroCount+s.UnknownMarketCount != absent || s.UnknownMarketCount > s.GapMarketCount {
			return errors.New("incomplete minute seal must classify every absent market without exceeding gap scope")
		}
		if s.Status == SealMissing && (s.CoverageSeconds != 0 || s.GapMarketCount != s.RegistryMarketCount) {
			return errors.New("missing minute seal must have zero coverage for the full registry")
		}
		if s.Status == SealPartial && (s.GapMarketCount == 0 || s.CoverageSeconds == MinuteSeconds) {
			return errors.New("partial minute seal must identify a non-empty gap scope")
		}
	}
	s.RegistryFingerprint = strings.ToLower(strings.TrimSpace(s.RegistryFingerprint))
	if len(s.RegistryFingerprint) != 64 {
		return errors.New("minute seal registry fingerprint must be a SHA-256 hex digest")
	}
	if s.SealedAt.IsZero() {
		s.SealedAt = time.Now().UTC()
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.SealedAt
	}
	return nil
}
