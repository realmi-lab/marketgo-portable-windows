package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/marketquestion"
)

var ErrMarketQuestionEvidenceUnavailable = errors.New("retained market question evidence is absent or expired")
var ErrMarketQuestionEvidenceCorrupt = errors.New("retained market question evidence failed integrity validation")

var marketQuestionBundleIDPattern = regexp.MustCompile(`^bundle_[0-9a-f]{32}$`)

// SaveMarketQuestionEvidenceBundle retains the first validated version for an
// evidence identity. A repeated answer cannot overwrite or extend that row.
// The caller explicitly chooses expiry; there is no implicit permanent archive.
func (r *Repository) SaveMarketQuestionEvidenceBundle(ctx context.Context, bundle marketquestion.EvidenceBundle, expiresAt time.Time) error {
	if r == nil || r.pool == nil {
		return ErrMarketQuestionEvidenceUnavailable
	}
	if !marketQuestionBundleIDPattern.MatchString(bundle.ID()) || !expiresAt.After(time.Now()) || expiresAt.After(time.Now().Add(90*24*time.Hour)) {
		return fmt.Errorf("invalid bundle identity or retention (maximum 90 days)")
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return err
	}
	if _, err = marketquestion.DecodeEvidenceBundle(raw); err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// This bounded expiry cleanup is confined to the optional answer archive.
	// Collector history, input snapshots and notices are never modified here.
	if _, err = tx.Exec(ctx, `DELETE FROM market_question_evidence_bundles
		WHERE bundle_id IN (SELECT bundle_id FROM market_question_evidence_bundles
		WHERE expires_at <= now() ORDER BY expires_at LIMIT 100)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO market_question_evidence_bundles
		(bundle_id, expires_at, content_sha256, bundle_payload) VALUES ($1,$2,$3,$4)
		ON CONFLICT (bundle_id) DO NOTHING`, bundle.ID(), expiresAt, hex.EncodeToString(sum[:]), raw); err != nil {
		return err
	}
	// ON CONFLICT is deliberately not an upsert. Validate the retained row even
	// when the caller presents an equivalent bundle with a newer creation time.
	var stored []byte
	var digest string
	if err = tx.QueryRow(ctx, `SELECT bundle_payload,content_sha256 FROM market_question_evidence_bundles
		WHERE bundle_id=$1 AND expires_at>now()`, bundle.ID()).Scan(&stored, &digest); err != nil {
		return err
	}
	if _, err = decodeRetainedMarketQuestionEvidence(bundle.ID(), stored, digest); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) MarketQuestionEvidenceBundle(ctx context.Context, id string) (marketquestion.EvidenceBundle, error) {
	bundle, _, err := r.MarketQuestionEvidenceBundleWithExpiry(ctx, id)
	return bundle, err
}

// MarketQuestionEvidenceBundleWithExpiry reports the original retained expiry,
// including when the same evidence identity was submitted again later.
func (r *Repository) MarketQuestionEvidenceBundleWithExpiry(ctx context.Context, id string) (marketquestion.EvidenceBundle, time.Time, error) {
	if r == nil || r.pool == nil || !marketQuestionBundleIDPattern.MatchString(id) {
		return marketquestion.EvidenceBundle{}, time.Time{}, ErrMarketQuestionEvidenceUnavailable
	}
	var raw []byte
	var digest string
	var expiresAt time.Time
	err := r.pool.QueryRow(ctx, `SELECT bundle_payload,content_sha256,expires_at FROM market_question_evidence_bundles
		WHERE bundle_id=$1 AND expires_at>now()`, id).Scan(&raw, &digest, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return marketquestion.EvidenceBundle{}, time.Time{}, ErrMarketQuestionEvidenceUnavailable
	}
	if err != nil {
		return marketquestion.EvidenceBundle{}, time.Time{}, err
	}
	bundle, err := decodeRetainedMarketQuestionEvidence(id, raw, digest)
	return bundle, expiresAt, err
}

func decodeRetainedMarketQuestionEvidence(id string, raw []byte, digest string) (marketquestion.EvidenceBundle, error) {
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != digest {
		return marketquestion.EvidenceBundle{}, ErrMarketQuestionEvidenceCorrupt
	}
	bundle, err := marketquestion.DecodeEvidenceBundle(raw)
	if err != nil || bundle.ID() != id {
		return marketquestion.EvidenceBundle{}, ErrMarketQuestionEvidenceCorrupt
	}
	return bundle, nil
}
