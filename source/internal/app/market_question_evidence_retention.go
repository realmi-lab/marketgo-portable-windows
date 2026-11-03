package app

import (
	"context"
	"errors"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/marketquestion"
	"market-share-go/internal/store/postgres"
)

const marketQuestionEvidencePersistBudget = 2 * time.Second

func (s *Service) retainMarketQuestionEvidence(
	ctx context.Context,
	bundle marketquestion.EvidenceBundle,
) marketanalysis.MarketQuestionEvidenceRetention {
	if s.marketQuestionEvidenceRetention <= 0 {
		return marketanalysis.MarketQuestionEvidenceRetention{Status: "DISABLED", Reason: "RETENTION_NOT_CONFIGURED"}
	}
	if s.postgresRepository == nil {
		return marketanalysis.MarketQuestionEvidenceRetention{Status: "FAILED", Reason: "POSTGRES_EVIDENCE_STORE_UNAVAILABLE"}
	}
	budget := marketQuestionEvidencePersistBudget
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return marketanalysis.MarketQuestionEvidenceRetention{Status: "FAILED", Reason: "REQUEST_BUDGET_EXHAUSTED"}
		}
		budget = min(budget, remaining)
	}
	persistCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	expiresAt := time.Now().Add(s.marketQuestionEvidenceRetention).UTC()
	if err := s.postgresRepository.SaveMarketQuestionEvidenceBundle(persistCtx, bundle, expiresAt); err != nil {
		return marketanalysis.MarketQuestionEvidenceRetention{Status: "FAILED", Reason: "EVIDENCE_STORE_WRITE_FAILED"}
	}
	_, persistedExpiry, err := s.postgresRepository.MarketQuestionEvidenceBundleWithExpiry(persistCtx, bundle.ID())
	if err != nil {
		return marketanalysis.MarketQuestionEvidenceRetention{Status: "FAILED", Reason: "EVIDENCE_STORE_CONFIRMATION_FAILED"}
	}
	expiresAtMS := persistedExpiry.UnixMilli()
	return marketanalysis.MarketQuestionEvidenceRetention{Status: "STORED", ExpiresAtMS: &expiresAtMS}
}

// MarketQuestionEvidenceBundle returns the frozen retained bundle. It never
// recalculates current market data for an older bundle identifier.
func (s *Service) MarketQuestionEvidenceBundle(ctx context.Context, bundleID string) (marketquestion.EvidenceBundleSnapshot, error) {
	bundleID = strings.TrimSpace(bundleID)
	if s == nil || s.marketQuestionEvidenceRetention <= 0 || s.postgresRepository == nil {
		return marketquestion.EvidenceBundleSnapshot{}, marketanalysis.ErrMarketQuestionEvidenceUnavailable
	}
	readCtx, cancel := context.WithTimeout(ctx, marketQuestionEvidencePersistBudget)
	defer cancel()
	bundle, err := s.postgresRepository.MarketQuestionEvidenceBundle(readCtx, bundleID)
	if err != nil {
		if errors.Is(err, postgres.ErrMarketQuestionEvidenceUnavailable) {
			return marketquestion.EvidenceBundleSnapshot{}, marketanalysis.ErrMarketQuestionEvidenceUnavailable
		}
		return marketquestion.EvidenceBundleSnapshot{}, err
	}
	return bundle.Snapshot(), nil
}
