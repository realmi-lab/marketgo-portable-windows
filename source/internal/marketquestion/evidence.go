package marketquestion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sort"
	"strings"
)

const EvidenceBundleContractVersion = "MARKET_QUESTION_EVIDENCE_BUNDLE_V1"
const MaxEncodedEvidenceBundleBytes = 2 << 20

var ErrInvalidEvidenceBundle = errors.New("invalid market question evidence bundle")

type IdentityStatus string

const (
	IdentityConfirmed     IdentityStatus = "CONFIRMED"
	IdentityUnavailable   IdentityStatus = "UNAVAILABLE"
	IdentityNotApplicable IdentityStatus = "NOT_APPLICABLE"
)

type QuantityState string

const (
	QuantityConfirmedValue QuantityState = "CONFIRMED_VALUE"
	QuantityConfirmedZero  QuantityState = "CONFIRMED_ZERO"
	QuantityUnknown        QuantityState = "UNKNOWN"
	QuantityNotApplicable  QuantityState = "NOT_APPLICABLE"
)

type Quantity struct {
	State   QuantityState `json:"state"`
	Decimal string        `json:"decimal,omitempty"`
	Unit    string        `json:"unit,omitempty"`
}

type FulfillmentStatus string

const (
	FulfillmentFulfilled          FulfillmentStatus = "FULFILLED"
	FulfillmentPartial            FulfillmentStatus = "PARTIAL"
	FulfillmentUnsupported        FulfillmentStatus = "UNSUPPORTED"
	FulfillmentNeedsClarification FulfillmentStatus = "NEEDS_CLARIFICATION"
)

type RequirementStatus string

const (
	RequirementSatisfied          RequirementStatus = "SATISFIED"
	RequirementUnavailable        RequirementStatus = "UNAVAILABLE"
	RequirementUnsupported        RequirementStatus = "UNSUPPORTED"
	RequirementNeedsClarification RequirementStatus = "NEEDS_CLARIFICATION"
)

type QuestionRequirement struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Required bool              `json:"required"`
	Status   RequirementStatus `json:"status"`
	FactIDs  []string          `json:"fact_ids"`
	Reason   string            `json:"reason,omitempty"`
}

type QuestionContract struct {
	QuestionDigest       string                `json:"question_digest"`
	Metric               MetricID              `json:"metric"`
	Comparison           ComparisonID          `json:"comparison"`
	TargetExchange       string                `json:"target_exchange,omitempty"`
	TargetAsset          string                `json:"target_asset,omitempty"`
	RequestedExplanation []string              `json:"requested_explanation"`
	Requirements         []QuestionRequirement `json:"requirements"`
}

type Fulfillment struct {
	Status       FulfillmentStatus     `json:"status"`
	Requirements []QuestionRequirement `json:"requirements"`
}

type TimeBasis string

const (
	TimeBasisRolling24hSnapshot           TimeBasis = "ROLLING_24H_SNAPSHOT"
	TimeBasisRolling24hEndpointComparison TimeBasis = "ROLLING_24H_ENDPOINT_COMPARISON"
	TimeBasisFixedInterval                TimeBasis = "FIXED_INTERVAL"
	TimeBasisAdjacentFixedIntervals       TimeBasis = "ADJACENT_FIXED_INTERVALS"
	TimeBasisCalendarDay                  TimeBasis = "CALENDAR_DAY"
	TimeBasisDailyEndpointSeries          TimeBasis = "DAILY_ENDPOINT_SERIES"
)

type TimeAggregationSemantics string

const (
	TimeAggregationSnapshotLevel       TimeAggregationSemantics = "SNAPSHOT_LEVEL"
	TimeAggregationEndpointDifference  TimeAggregationSemantics = "ENDPOINT_DIFFERENCE_NOT_INTERVAL_FLOW"
	TimeAggregationClosedIntervalSum   TimeAggregationSemantics = "CLOSED_INTERVAL_SUM"
	TimeAggregationAdjacentIntervalSum TimeAggregationSemantics = "ADJACENT_CLOSED_INTERVAL_COMPARISON"
)

type TimeBoundary struct {
	StartMS      *int64 `json:"start_ms,omitempty"`
	EndMS        *int64 `json:"end_ms,omitempty"`
	AsOfMS       *int64 `json:"as_of_ms,omitempty"`
	ObservedAtMS *int64 `json:"observed_at_ms,omitempty"`
}

type TimeSemantics struct {
	Basis                TimeBasis                `json:"basis"`
	AggregationSemantics TimeAggregationSemantics `json:"aggregation_semantics"`
	RequestedWindowSec   int64                    `json:"requested_window_sec,omitempty"`
	ActualWindowSec      int64                    `json:"actual_window_sec,omitempty"`
	AlignmentDeltaMS     int64                    `json:"alignment_delta_ms,omitempty"`
	Requested            TimeBoundary             `json:"requested"`
	Actual               TimeBoundary             `json:"actual"`
	Timezone             string                   `json:"timezone"`
	Status               IdentityStatus           `json:"status"`
	Reason               string                   `json:"reason,omitempty"`
}

type ScopeStatus string

const (
	ScopeExact              ScopeStatus = "EXACT"
	ScopeSubstitutedPartial ScopeStatus = "SUBSTITUTED_PARTIAL"
	ScopeUnavailable        ScopeStatus = "UNAVAILABLE"
)

type ScopeExclusion struct {
	Exchange string `json:"exchange"`
	Reason   string `json:"reason"`
}

type ScopeIdentity struct {
	RequestedMarketScope string           `json:"requested_market_scope"`
	ActualMarketScope    string           `json:"actual_market_scope,omitempty"`
	RequestedAssetSet    string           `json:"requested_asset_set,omitempty"`
	ActualAssetSet       string           `json:"actual_asset_set,omitempty"`
	RequestedExchanges   []string         `json:"requested_exchanges"`
	IncludedExchanges    []string         `json:"included_exchanges"`
	ExcludedExchanges    []ScopeExclusion `json:"excluded_exchanges"`
	SameAssetSet         bool             `json:"same_asset_set"`
	Status               ScopeStatus      `json:"status"`
	Reason               string           `json:"reason,omitempty"`
}

type CoverageIdentity struct {
	Basis          string         `json:"basis"`
	Status         IdentityStatus `json:"status"`
	ConfirmedUnits int64          `json:"confirmed_units,omitempty"`
	RequiredUnits  int64          `json:"required_units,omitempty"`
	GapUnits       int64          `json:"gap_units,omitempty"`
	Unit           string         `json:"unit,omitempty"`
	Reason         string         `json:"reason,omitempty"`
}

type DenominatorIdentity struct {
	ID         string   `json:"id"`
	Meaning    string   `json:"meaning"`
	Scope      string   `json:"scope"`
	Value      Quantity `json:"value"`
	RegistryID string   `json:"registry_id,omitempty"`
	SourceID   string   `json:"source_id"`
}

type SourceIdentity struct {
	ID            string         `json:"id"`
	EvidenceLayer string         `json:"evidence_layer"`
	Tool          EvidenceToolID `json:"tool,omitempty"`
	Status        IdentityStatus `json:"status"`
	ObservedAtMS  *int64         `json:"observed_at_ms,omitempty"`
	Reason        string         `json:"reason,omitempty"`
}

type RegistryIdentity struct {
	Status   IdentityStatus `json:"status"`
	Identity string         `json:"identity,omitempty"`
	AsOfMS   *int64         `json:"as_of_ms,omitempty"`
	Source   string         `json:"source,omitempty"`
	Reason   string         `json:"reason,omitempty"`
}

// RevisionIdentity keeps independently known versions even when a joint DB and
// memory input revision is unavailable. ID must only be set by a caller that
// owns a real immutable input snapshot identity; the bundle constructor never
// manufactures it from timestamps or version counters.
type RevisionIdentity struct {
	Status             IdentityStatus `json:"status"`
	ID                 string         `json:"id,omitempty"`
	AnalysisContextID  string         `json:"analysis_context_id,omitempty"`
	InputRevision      string         `json:"input_revision,omitempty"`
	AnalysisVersion    uint64         `json:"analysis_version,omitempty"`
	AggregationVersion uint64         `json:"aggregation_version,omitempty"`
	DBSnapshotIdentity string         `json:"db_snapshot_identity,omitempty"`
	MemoryGeneration   string         `json:"memory_generation,omitempty"`
	Reason             string         `json:"reason,omitempty"`
}

type FactSubject struct {
	MarketScope string `json:"market_scope,omitempty"`
	Exchange    string `json:"exchange,omitempty"`
	Asset       string `json:"asset,omitempty"`
}

type FactTimeReference struct {
	Role    string `json:"role"`
	StartMS *int64 `json:"start_ms,omitempty"`
	EndMS   *int64 `json:"end_ms,omitempty"`
	AsOfMS  *int64 `json:"as_of_ms,omitempty"`
}

type FactRelation struct {
	Operation      AggregationOperationID `json:"operation"`
	OperandFactIDs []string               `json:"operand_fact_ids"`
}

type EvidenceFact struct {
	ID                string              `json:"id"`
	Metric            string              `json:"metric"`
	Role              string              `json:"role"`
	Subject           FactSubject         `json:"subject"`
	Value             Quantity            `json:"value"`
	Time              FactTimeReference   `json:"time"`
	DenominatorIDs    []string            `json:"denominator_ids"`
	SourceID          string              `json:"source_id"`
	AggregationPolicy AggregationPolicyID `json:"aggregation_policy"`
	Quality           string              `json:"quality"`
	Relation          *FactRelation       `json:"relation,omitempty"`
	Text              string              `json:"text,omitempty"`
}

type ContributionIdentity struct {
	Status           IdentityStatus `json:"status"`
	Method           string         `json:"method,omitempty"`
	MethodVersion    string         `json:"method_version,omitempty"`
	ComponentFactIDs []string       `json:"component_fact_ids"`
	OtherFactID      string         `json:"other_fact_id,omitempty"`
	ResidualFactID   string         `json:"residual_fact_id,omitempty"`
	ResidualKind     string         `json:"residual_kind,omitempty"`
	Reason           string         `json:"reason,omitempty"`
}

type NoticeSearchStatus string

const (
	NoticeNotQueried NoticeSearchStatus = "NOT_QUERIED"
	NoticeFailed     NoticeSearchStatus = "FAILED"
	NoticeNotFound   NoticeSearchStatus = "NOT_FOUND"
	NoticeFound      NoticeSearchStatus = "FOUND"
)

type NoticeEvidence struct {
	Status             NoticeSearchStatus `json:"status"`
	SourceCount        int                `json:"source_count"`
	HealthySourceCount int                `json:"healthy_source_count"`
	FactIDs            []string           `json:"fact_ids"`
	CausalClaim        bool               `json:"causal_claim"`
	Reason             string             `json:"reason,omitempty"`
}

type ReplayCapability struct {
	RecalculationMode     string         `json:"recalculation_mode"`
	RetainedBundleStatus  IdentityStatus `json:"retained_bundle_status"`
	OriginalAsKnownStatus IdentityStatus `json:"original_as_known_status"`
	Reason                string         `json:"reason,omitempty"`
}

type ExplanationCacheIdentity struct {
	Language            string `json:"language"`
	DetailLevel         string `json:"detail_level"`
	Model               string `json:"model"`
	PromptPolicyVersion string `json:"prompt_policy_version"`
	ValidatorVersion    string `json:"validator_version"`
}

type CacheIdentity struct {
	CalculationKey string `json:"calculation_key"`
	ExplanationKey string `json:"explanation_key,omitempty"`
}

type EvidenceBundleInput struct {
	CreatedAtMS      int64
	QuestionContract QuestionContract
	Time             TimeSemantics
	Scope            ScopeIdentity
	Coverage         CoverageIdentity
	Denominators     []DenominatorIdentity
	Sources          []SourceIdentity
	Registry         RegistryIdentity
	Revision         RevisionIdentity
	Facts            []EvidenceFact
	Contribution     ContributionIdentity
	Notice           NoticeEvidence
	Replay           ReplayCapability
	ExplanationCache ExplanationCacheIdentity
	Warnings         []string
}

type EvidenceBundleSnapshot struct {
	ContractVersion  string                `json:"contract_version"`
	BundleID         string                `json:"bundle_id"`
	CatalogVersion   string                `json:"catalog_version"`
	CreatedAtMS      int64                 `json:"created_at_ms"`
	QuestionContract QuestionContract      `json:"question_contract"`
	Fulfillment      Fulfillment           `json:"fulfillment"`
	Time             TimeSemantics         `json:"time"`
	Scope            ScopeIdentity         `json:"scope"`
	Coverage         CoverageIdentity      `json:"coverage"`
	Denominators     []DenominatorIdentity `json:"denominators"`
	Sources          []SourceIdentity      `json:"sources"`
	Registry         RegistryIdentity      `json:"registry"`
	Revision         RevisionIdentity      `json:"revision"`
	Facts            []EvidenceFact        `json:"facts"`
	Contribution     ContributionIdentity  `json:"contribution"`
	Notice           NoticeEvidence        `json:"notice"`
	Replay           ReplayCapability      `json:"replay"`
	Cache            CacheIdentity         `json:"cache"`
	Warnings         []string              `json:"warnings"`
}

// EvidenceBundle is immutable by construction. Snapshot and JSON operations
// return deep copies, so callers cannot mutate facts that a model or cache key
// already references.
type EvidenceBundle struct {
	snapshot EvidenceBundleSnapshot
}

func NewEvidenceBundle(input EvidenceBundleInput) (EvidenceBundle, error) {
	snapshot := EvidenceBundleSnapshot{
		ContractVersion:  EvidenceBundleContractVersion,
		CatalogVersion:   CatalogVersion,
		CreatedAtMS:      input.CreatedAtMS,
		QuestionContract: cloneQuestionContract(input.QuestionContract),
		Time:             cloneTimeSemantics(input.Time), Scope: cloneScopeIdentity(input.Scope), Coverage: input.Coverage,
		Denominators: cloneDenominators(input.Denominators), Sources: cloneSources(input.Sources),
		Registry: cloneRegistry(input.Registry), Revision: input.Revision,
		Facts: cloneFacts(input.Facts), Contribution: cloneContribution(input.Contribution),
		Notice: cloneNotice(input.Notice), Replay: input.Replay,
		Warnings: append([]string(nil), input.Warnings...),
	}
	normalizeEvidenceSnapshot(&snapshot)
	if err := validateEvidenceSnapshot(&snapshot); err != nil {
		return EvidenceBundle{}, err
	}
	snapshot.Fulfillment = Fulfillment{
		Status:       deriveFulfillmentStatus(snapshot.QuestionContract.Requirements),
		Requirements: cloneRequirements(snapshot.QuestionContract.Requirements),
	}
	snapshot.BundleID = evidenceDigest("bundle", evidenceBundleDigestMaterial(snapshot))
	snapshot.Cache.CalculationKey = evidenceDigest("calculation", evidenceBundleCalculationMaterial(snapshot))
	if !emptyExplanationCacheIdentity(input.ExplanationCache) {
		key, err := explanationCacheKey(snapshot.BundleID, input.ExplanationCache)
		if err != nil {
			return EvidenceBundle{}, err
		}
		snapshot.Cache.ExplanationKey = key
	}
	return EvidenceBundle{snapshot: cloneEvidenceSnapshot(snapshot)}, nil
}

func (bundle EvidenceBundle) ID() string { return bundle.snapshot.BundleID }

func (bundle EvidenceBundle) Snapshot() EvidenceBundleSnapshot {
	return cloneEvidenceSnapshot(bundle.snapshot)
}

func (bundle EvidenceBundle) MarshalJSON() ([]byte, error) {
	return json.Marshal(bundle.Snapshot())
}

// ParseEvidenceBundle is the persistence/read boundary for retained bundles.
// It rejects unknown fields and any mutation of the content, fulfillment, or
// calculation identity recorded by the original constructor.

func DecodeEvidenceBundle(encoded []byte) (EvidenceBundle, error) {
	if len(encoded) == 0 || len(encoded) > MaxEncodedEvidenceBundleBytes {
		return EvidenceBundle{}, fmt.Errorf("%w: encoded size %d exceeds 1..%d", ErrInvalidEvidenceBundle, len(encoded), MaxEncodedEvidenceBundleBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var snapshot EvidenceBundleSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return EvidenceBundle{}, fmt.Errorf("%w: decode: %v", ErrInvalidEvidenceBundle, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return EvidenceBundle{}, fmt.Errorf("%w: trailing JSON", ErrInvalidEvidenceBundle)
	}
	return EvidenceBundleFromSnapshot(snapshot)
}

// ParseEvidenceBundle is retained as a descriptive alias for callers that are
// not tied to the PostgreSQL adapter naming.
func ParseEvidenceBundle(encoded []byte) (EvidenceBundle, error) {
	return DecodeEvidenceBundle(encoded)
}

// EvidenceBundleFromSnapshot validates an already serialized projection. It
// does not upgrade versions or fill absent source identities.
func EvidenceBundleFromSnapshot(snapshot EvidenceBundleSnapshot) (EvidenceBundle, error) {
	if snapshot.ContractVersion != EvidenceBundleContractVersion || snapshot.CatalogVersion != CatalogVersion {
		return EvidenceBundle{}, fmt.Errorf("%w: unsupported contract or catalog version", ErrInvalidEvidenceBundle)
	}
	original := cloneEvidenceSnapshot(snapshot)
	normalizeEvidenceSnapshot(&snapshot)
	if !reflect.DeepEqual(original, snapshot) {
		return EvidenceBundle{}, fmt.Errorf("%w: non-canonical snapshot", ErrInvalidEvidenceBundle)
	}
	if err := validateEvidenceSnapshot(&snapshot); err != nil {
		return EvidenceBundle{}, err
	}
	wantFulfillment := Fulfillment{
		Status:       deriveFulfillmentStatus(snapshot.QuestionContract.Requirements),
		Requirements: cloneRequirements(snapshot.QuestionContract.Requirements),
	}
	if !reflect.DeepEqual(snapshot.Fulfillment, wantFulfillment) {
		return EvidenceBundle{}, fmt.Errorf("%w: fulfillment does not match requirements", ErrInvalidEvidenceBundle)
	}
	wantBundleID := evidenceDigest("bundle", evidenceBundleDigestMaterial(snapshot))
	if snapshot.BundleID != wantBundleID {
		return EvidenceBundle{}, fmt.Errorf("%w: bundle identity mismatch", ErrInvalidEvidenceBundle)
	}
	wantCalculationKey := evidenceDigest("calculation", evidenceBundleCalculationMaterial(snapshot))
	if snapshot.Cache.CalculationKey != wantCalculationKey {
		return EvidenceBundle{}, fmt.Errorf("%w: calculation cache identity mismatch", ErrInvalidEvidenceBundle)
	}
	if snapshot.Cache.ExplanationKey != "" && !strings.HasPrefix(snapshot.Cache.ExplanationKey, "explanation_") {
		return EvidenceBundle{}, fmt.Errorf("%w: malformed explanation cache identity", ErrInvalidEvidenceBundle)
	}
	return EvidenceBundle{snapshot: cloneEvidenceSnapshot(snapshot)}, nil
}

func (bundle EvidenceBundle) ExplanationCacheKey(identity ExplanationCacheIdentity) (string, error) {
	if bundle.snapshot.BundleID == "" {
		return "", fmt.Errorf("%w: empty bundle", ErrInvalidEvidenceBundle)
	}
	return explanationCacheKey(bundle.snapshot.BundleID, identity)
}

func ConfirmedQuantity(decimal, unit string) (Quantity, error) {
	decimal = strings.TrimSpace(decimal)
	value, _, err := big.ParseFloat(decimal, 10, 1024, big.ToNearestEven)
	if err != nil || value == nil || len(decimal) > 256 {
		return Quantity{}, fmt.Errorf("%w: invalid decimal quantity", ErrInvalidEvidenceBundle)
	}
	state := QuantityConfirmedValue
	if value.Sign() == 0 {
		state = QuantityConfirmedZero
		decimal = "0"
	}
	result := Quantity{State: state, Decimal: strings.TrimSpace(decimal), Unit: strings.TrimSpace(unit)}
	if err := validateQuantity(result); err != nil {
		return Quantity{}, err
	}
	return result, nil
}

func UnknownQuantity(unit string) Quantity {
	return Quantity{State: QuantityUnknown, Unit: strings.TrimSpace(unit)}
}

func NotApplicableQuantity() Quantity { return Quantity{State: QuantityNotApplicable} }

func explanationCacheKey(bundleID string, identity ExplanationCacheIdentity) (string, error) {
	identity.Language = strings.TrimSpace(identity.Language)
	identity.DetailLevel = strings.TrimSpace(identity.DetailLevel)
	identity.Model = strings.TrimSpace(identity.Model)
	identity.PromptPolicyVersion = strings.TrimSpace(identity.PromptPolicyVersion)
	identity.ValidatorVersion = strings.TrimSpace(identity.ValidatorVersion)
	if bundleID == "" || identity.Language == "" || identity.DetailLevel == "" || identity.Model == "" || identity.PromptPolicyVersion == "" || identity.ValidatorVersion == "" {
		return "", fmt.Errorf("%w: incomplete explanation cache identity", ErrInvalidEvidenceBundle)
	}
	return evidenceDigest("explanation", struct {
		BundleID string
		Identity ExplanationCacheIdentity
	}{bundleID, identity}), nil
}

func emptyExplanationCacheIdentity(value ExplanationCacheIdentity) bool {
	return value == (ExplanationCacheIdentity{})
}

func deriveFulfillmentStatus(requirements []QuestionRequirement) FulfillmentStatus {
	status := FulfillmentFulfilled
	for _, requirement := range requirements {
		if !requirement.Required {
			continue
		}
		switch requirement.Status {
		case RequirementNeedsClarification:
			return FulfillmentNeedsClarification
		case RequirementUnsupported:
			if status != FulfillmentNeedsClarification {
				status = FulfillmentUnsupported
			}
		case RequirementUnavailable:
			if status == FulfillmentFulfilled {
				status = FulfillmentPartial
			}
		}
	}
	return status
}

func normalizeEvidenceSnapshot(snapshot *EvidenceBundleSnapshot) {
	snapshot.QuestionContract.QuestionDigest = strings.TrimSpace(snapshot.QuestionContract.QuestionDigest)
	snapshot.QuestionContract.TargetExchange = strings.ToLower(strings.TrimSpace(snapshot.QuestionContract.TargetExchange))
	snapshot.QuestionContract.TargetAsset = strings.ToUpper(strings.TrimSpace(snapshot.QuestionContract.TargetAsset))
	normalizeStrings(&snapshot.QuestionContract.RequestedExplanation, strings.ToUpper)
	for index := range snapshot.QuestionContract.Requirements {
		r := &snapshot.QuestionContract.Requirements[index]
		r.ID, r.Kind, r.Reason = strings.TrimSpace(r.ID), strings.TrimSpace(r.Kind), strings.TrimSpace(r.Reason)
		normalizeStrings(&r.FactIDs, nil)
	}
	sort.Slice(snapshot.QuestionContract.Requirements, func(i, j int) bool {
		return snapshot.QuestionContract.Requirements[i].ID < snapshot.QuestionContract.Requirements[j].ID
	})
	normalizeStrings(&snapshot.Scope.RequestedExchanges, strings.ToLower)
	normalizeStrings(&snapshot.Scope.IncludedExchanges, strings.ToLower)
	for index := range snapshot.Scope.ExcludedExchanges {
		snapshot.Scope.ExcludedExchanges[index].Exchange = strings.ToLower(strings.TrimSpace(snapshot.Scope.ExcludedExchanges[index].Exchange))
		snapshot.Scope.ExcludedExchanges[index].Reason = strings.TrimSpace(snapshot.Scope.ExcludedExchanges[index].Reason)
	}
	sort.Slice(snapshot.Scope.ExcludedExchanges, func(i, j int) bool {
		return snapshot.Scope.ExcludedExchanges[i].Exchange < snapshot.Scope.ExcludedExchanges[j].Exchange
	})
	for index := range snapshot.Denominators {
		d := &snapshot.Denominators[index]
		d.ID, d.Meaning, d.Scope, d.RegistryID, d.SourceID = strings.TrimSpace(d.ID), strings.TrimSpace(d.Meaning), strings.TrimSpace(d.Scope), strings.TrimSpace(d.RegistryID), strings.TrimSpace(d.SourceID)
	}
	sort.Slice(snapshot.Denominators, func(i, j int) bool { return snapshot.Denominators[i].ID < snapshot.Denominators[j].ID })
	for index := range snapshot.Sources {
		s := &snapshot.Sources[index]
		s.ID, s.EvidenceLayer, s.Reason = strings.TrimSpace(s.ID), strings.TrimSpace(s.EvidenceLayer), strings.TrimSpace(s.Reason)
	}
	sort.Slice(snapshot.Sources, func(i, j int) bool { return snapshot.Sources[i].ID < snapshot.Sources[j].ID })
	factIDMap := map[string]string{}
	for index := range snapshot.Facts {
		f := &snapshot.Facts[index]
		originalID := strings.TrimSpace(f.ID)
		f.ID, f.Metric, f.Role = originalID, strings.TrimSpace(f.Metric), strings.TrimSpace(f.Role)
		f.Subject.Exchange = strings.ToLower(strings.TrimSpace(f.Subject.Exchange))
		f.Subject.Asset = strings.ToUpper(strings.TrimSpace(f.Subject.Asset))
		f.Subject.MarketScope = strings.TrimSpace(f.Subject.MarketScope)
		f.SourceID, f.Quality, f.Text = strings.TrimSpace(f.SourceID), strings.TrimSpace(f.Quality), strings.TrimSpace(f.Text)
		normalizeStrings(&f.DenominatorIDs, nil)
		if f.Relation != nil {
			normalizeOrderedStrings(&f.Relation.OperandFactIDs)
		}
		if f.AggregationPolicy == "" {
			f.AggregationPolicy = AggregationPolicyForFactMetric(f.Metric)
		}
		if f.ID == "" {
			copy := *f
			copy.ID = ""
			f.ID = evidenceFactID(copy)
		} else {
			f.ID = CanonicalFactID(f.ID)
		}
		factIDMap[originalID] = f.ID
	}
	for index := range snapshot.Facts {
		if relation := snapshot.Facts[index].Relation; relation != nil {
			for operandIndex, operandID := range relation.OperandFactIDs {
				if canonical := factIDMap[operandID]; canonical != "" {
					relation.OperandFactIDs[operandIndex] = canonical
				}
			}
			normalizeOrderedStrings(&relation.OperandFactIDs)
		}
	}
	for index := range snapshot.QuestionContract.Requirements {
		for factIndex, factID := range snapshot.QuestionContract.Requirements[index].FactIDs {
			if canonical := factIDMap[factID]; canonical != "" {
				snapshot.QuestionContract.Requirements[index].FactIDs[factIndex] = canonical
			}
		}
		normalizeStrings(&snapshot.QuestionContract.Requirements[index].FactIDs, nil)
	}
	for index, factID := range snapshot.Contribution.ComponentFactIDs {
		if canonical := factIDMap[factID]; canonical != "" {
			snapshot.Contribution.ComponentFactIDs[index] = canonical
		}
	}
	if canonical := factIDMap[snapshot.Contribution.OtherFactID]; canonical != "" {
		snapshot.Contribution.OtherFactID = canonical
	}
	if canonical := factIDMap[snapshot.Contribution.ResidualFactID]; canonical != "" {
		snapshot.Contribution.ResidualFactID = canonical
	}
	for index, factID := range snapshot.Notice.FactIDs {
		if canonical := factIDMap[factID]; canonical != "" {
			snapshot.Notice.FactIDs[index] = canonical
		}
	}
	sort.Slice(snapshot.Facts, func(i, j int) bool { return snapshot.Facts[i].ID < snapshot.Facts[j].ID })
	normalizeStrings(&snapshot.Contribution.ComponentFactIDs, nil)
	normalizeStrings(&snapshot.Notice.FactIDs, nil)
	normalizeStrings(&snapshot.Warnings, nil)
}

func validateEvidenceSnapshot(snapshot *EvidenceBundleSnapshot) error {
	if snapshot.CatalogVersion == "" || snapshot.QuestionContract.QuestionDigest == "" || snapshot.QuestionContract.Metric == "" || snapshot.QuestionContract.Comparison == "" {
		return fmt.Errorf("%w: incomplete question contract", ErrInvalidEvidenceBundle)
	}
	if _, ok := Metric(snapshot.QuestionContract.Metric); !ok {
		return fmt.Errorf("%w: unknown metric %q", ErrInvalidEvidenceBundle, snapshot.QuestionContract.Metric)
	}
	if _, ok := Comparison(snapshot.QuestionContract.Comparison); !ok {
		return fmt.Errorf("%w: unknown comparison %q", ErrInvalidEvidenceBundle, snapshot.QuestionContract.Comparison)
	}
	if snapshot.Time.Basis == "" || snapshot.Time.AggregationSemantics == "" || snapshot.Time.Timezone == "" || !validIdentityStatus(snapshot.Time.Status) {
		return fmt.Errorf("%w: incomplete time semantics", ErrInvalidEvidenceBundle)
	}
	if snapshot.Time.Status == IdentityUnavailable && strings.TrimSpace(snapshot.Time.Reason) == "" {
		return fmt.Errorf("%w: unavailable time semantics need reason", ErrInvalidEvidenceBundle)
	}
	if !validTimeSemantics(snapshot.QuestionContract.Comparison, snapshot.Time) {
		return fmt.Errorf("%w: time basis is incompatible with comparison %q", ErrInvalidEvidenceBundle, snapshot.QuestionContract.Comparison)
	}
	if !validScopeStatus(snapshot.Scope.Status) || snapshot.Scope.RequestedMarketScope == "" {
		return fmt.Errorf("%w: incomplete scope identity", ErrInvalidEvidenceBundle)
	}
	if snapshot.Scope.Status != ScopeExact && strings.TrimSpace(snapshot.Scope.Reason) == "" {
		return fmt.Errorf("%w: non-exact scope needs reason", ErrInvalidEvidenceBundle)
	}
	if !validIdentityStatus(snapshot.Coverage.Status) || strings.TrimSpace(snapshot.Coverage.Basis) == "" {
		return fmt.Errorf("%w: incomplete coverage identity", ErrInvalidEvidenceBundle)
	}
	if snapshot.Coverage.ConfirmedUnits < 0 || snapshot.Coverage.RequiredUnits < 0 || snapshot.Coverage.GapUnits < 0 {
		return fmt.Errorf("%w: negative coverage units", ErrInvalidEvidenceBundle)
	}
	if snapshot.Coverage.Status == IdentityUnavailable && strings.TrimSpace(snapshot.Coverage.Reason) == "" {
		return fmt.Errorf("%w: unavailable coverage needs reason", ErrInvalidEvidenceBundle)
	}
	if err := validateRegistry(snapshot.Registry); err != nil {
		return err
	}
	if err := validateRevision(snapshot.Revision); err != nil {
		return err
	}
	sources := map[string]SourceIdentity{}
	for _, source := range snapshot.Sources {
		if source.ID == "" || !validIdentityStatus(source.Status) || sources[source.ID].ID != "" {
			return fmt.Errorf("%w: invalid or duplicate source %q", ErrInvalidEvidenceBundle, source.ID)
		}
		if source.Status == IdentityConfirmed && source.EvidenceLayer == "" {
			return fmt.Errorf("%w: confirmed source %q has no evidence layer", ErrInvalidEvidenceBundle, source.ID)
		}
		sources[source.ID] = source
	}
	denominators := map[string]DenominatorIdentity{}
	for _, denominator := range snapshot.Denominators {
		if denominator.ID == "" || denominator.Meaning == "" || denominator.SourceID == "" || denominators[denominator.ID].ID != "" {
			return fmt.Errorf("%w: invalid or duplicate denominator %q", ErrInvalidEvidenceBundle, denominator.ID)
		}
		if _, ok := sources[denominator.SourceID]; !ok {
			return fmt.Errorf("%w: denominator %q refers to unknown source", ErrInvalidEvidenceBundle, denominator.ID)
		}
		if err := validateQuantity(denominator.Value); err != nil {
			return err
		}
		denominators[denominator.ID] = denominator
	}
	facts := map[string]EvidenceFact{}
	for _, fact := range snapshot.Facts {
		if !ValidFactID(fact.ID) || fact.Metric == "" || fact.Role == "" || fact.SourceID == "" || facts[fact.ID].ID != "" {
			return fmt.Errorf("%w: invalid or duplicate fact %q", ErrInvalidEvidenceBundle, fact.ID)
		}
		if _, ok := sources[fact.SourceID]; !ok {
			return fmt.Errorf("%w: fact %q refers to unknown source", ErrInvalidEvidenceBundle, fact.ID)
		}
		for _, denominatorID := range fact.DenominatorIDs {
			if _, ok := denominators[denominatorID]; !ok {
				return fmt.Errorf("%w: fact %q refers to unknown denominator %q", ErrInvalidEvidenceBundle, fact.ID, denominatorID)
			}
		}
		if err := validateQuantity(fact.Value); err != nil {
			return err
		}
		operation := AggregationDirectValue
		if fact.Relation != nil {
			operation = fact.Relation.Operation
		}
		if err := ValidateAggregationUse(fact.Metric, fact.AggregationPolicy, operation); err != nil {
			return fmt.Errorf("%w: fact %q: %v", ErrInvalidEvidenceBundle, fact.ID, err)
		}
		facts[fact.ID] = fact
	}
	for _, fact := range snapshot.Facts {
		if fact.Relation == nil {
			continue
		}
		if err := ValidateAggregationRelation(fact, facts); err != nil {
			return fmt.Errorf("%w: fact %q: %v", ErrInvalidEvidenceBundle, fact.ID, err)
		}
	}
	seenRequirements := map[string]bool{}
	if len(snapshot.QuestionContract.Requirements) == 0 {
		return fmt.Errorf("%w: question contract has no requirements", ErrInvalidEvidenceBundle)
	}
	for _, requirement := range snapshot.QuestionContract.Requirements {
		if requirement.ID == "" || requirement.Kind == "" || seenRequirements[requirement.ID] {
			return fmt.Errorf("%w: invalid or duplicate requirement %q", ErrInvalidEvidenceBundle, requirement.ID)
		}
		seenRequirements[requirement.ID] = true
		switch requirement.Status {
		case RequirementSatisfied, RequirementUnavailable, RequirementUnsupported, RequirementNeedsClarification:
		default:
			return fmt.Errorf("%w: requirement %q has invalid status %q", ErrInvalidEvidenceBundle, requirement.ID, requirement.Status)
		}
		confirmedFacts := 0
		for _, factID := range requirement.FactIDs {
			fact, ok := facts[factID]
			if !ok {
				return fmt.Errorf("%w: requirement %q refers to unknown fact %q", ErrInvalidEvidenceBundle, requirement.ID, factID)
			}
			if fact.Value.State == QuantityConfirmedValue || fact.Value.State == QuantityConfirmedZero {
				confirmedFacts++
			}
		}
		if requirement.Status == RequirementSatisfied {
			if numericRequirementKind(requirement.Kind) && (len(requirement.FactIDs) == 0 || confirmedFacts != len(requirement.FactIDs)) {
				return fmt.Errorf("%w: numeric requirement %q has an unconfirmed or missing fact", ErrInvalidEvidenceBundle, requirement.ID)
			}
		} else if requirement.Required && strings.TrimSpace(requirement.Reason) == "" {
			return fmt.Errorf("%w: unmet requirement %q has no reason", ErrInvalidEvidenceBundle, requirement.ID)
		}
	}
	if err := validateContribution(snapshot.Contribution, facts); err != nil {
		return err
	}
	if err := validateNotice(snapshot.Notice, facts); err != nil {
		return err
	}
	if !validIdentityStatus(snapshot.Replay.RetainedBundleStatus) || !validIdentityStatus(snapshot.Replay.OriginalAsKnownStatus) {
		return fmt.Errorf("%w: invalid replay status", ErrInvalidEvidenceBundle)
	}
	if (snapshot.Replay.RetainedBundleStatus == IdentityUnavailable || snapshot.Replay.OriginalAsKnownStatus == IdentityUnavailable) && strings.TrimSpace(snapshot.Replay.Reason) == "" {
		return fmt.Errorf("%w: unavailable replay capability needs reason", ErrInvalidEvidenceBundle)
	}
	return nil
}

func validTimeSemantics(comparison ComparisonID, value TimeSemantics) bool {
	if value.RequestedWindowSec < 0 || value.ActualWindowSec < 0 {
		return false
	}
	if !validTimeBoundary(value.Requested) || !validTimeBoundary(value.Actual) {
		return false
	}
	confirmedInterval := value.Actual.StartMS != nil && value.Actual.EndMS != nil && *value.Actual.StartMS < *value.Actual.EndMS
	switch comparison {
	case ComparisonCurrentSnapshot:
		return value.Basis == TimeBasisRolling24hSnapshot && value.AggregationSemantics == TimeAggregationSnapshotLevel &&
			(value.Status != IdentityConfirmed || value.Actual.AsOfMS != nil)
	case ComparisonCalendarDayClose:
		return value.Basis == TimeBasisCalendarDay && value.AggregationSemantics == TimeAggregationClosedIntervalSum &&
			value.Requested.StartMS != nil && value.Requested.EndMS != nil &&
			(value.Status != IdentityConfirmed || confirmedInterval)
	case ComparisonDailyRolling24hEndpoints:
		return value.Basis == TimeBasisDailyEndpointSeries && value.AggregationSemantics == TimeAggregationEndpointDifference &&
			value.Requested.StartMS != nil && value.Requested.EndMS != nil &&
			(value.Status != IdentityConfirmed || confirmedInterval)
	case ComparisonFixedHistoricalInterval:
		return value.Basis == TimeBasisFixedInterval && value.AggregationSemantics == TimeAggregationClosedIntervalSum &&
			value.Requested.StartMS != nil && value.Requested.EndMS != nil &&
			(value.Status != IdentityConfirmed || confirmedInterval)
	case ComparisonRolling24hEndpointDelta:
		return value.Basis == TimeBasisRolling24hEndpointComparison && value.AggregationSemantics == TimeAggregationEndpointDifference &&
			value.RequestedWindowSec > 0 && (value.Status != IdentityConfirmed || confirmedInterval)
	case ComparisonCurrentPreviousInterval:
		return value.Basis == TimeBasisAdjacentFixedIntervals && value.AggregationSemantics == TimeAggregationAdjacentIntervalSum &&
			value.RequestedWindowSec > 0 && (value.Status != IdentityConfirmed || confirmedInterval)
	default:
		return false
	}
}

func validTimeBoundary(value TimeBoundary) bool {
	if value.StartMS != nil && value.EndMS != nil && *value.StartMS >= *value.EndMS {
		return false
	}
	for _, timestamp := range []*int64{value.StartMS, value.EndMS, value.AsOfMS, value.ObservedAtMS} {
		if timestamp != nil && *timestamp <= 0 {
			return false
		}
	}
	return true
}

func validScopeStatus(value ScopeStatus) bool {
	return value == ScopeExact || value == ScopeSubstitutedPartial || value == ScopeUnavailable
}

func numericRequirementKind(kind string) bool {
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "METRIC", "CHANGE", "DENOMINATOR", "ATTRIBUTION", "QUANTITATIVE_RESULT", "AMOUNT", "RATIO", "COUNT":
		return true
	default:
		return false
	}
}

func validateQuantity(value Quantity) error {
	switch value.State {
	case QuantityConfirmedValue, QuantityConfirmedZero:
		if strings.TrimSpace(value.Decimal) == "" || strings.TrimSpace(value.Unit) == "" {
			return fmt.Errorf("%w: confirmed quantity needs decimal and unit", ErrInvalidEvidenceBundle)
		}
		parsed, _, err := big.ParseFloat(value.Decimal, 10, 1024, big.ToNearestEven)
		if err != nil || parsed == nil || len(value.Decimal) > 256 {
			return fmt.Errorf("%w: non-finite quantity", ErrInvalidEvidenceBundle)
		}
		if value.State == QuantityConfirmedZero && parsed.Sign() != 0 || value.State == QuantityConfirmedValue && parsed.Sign() == 0 {
			return fmt.Errorf("%w: zero quantity state mismatch", ErrInvalidEvidenceBundle)
		}
	case QuantityUnknown:
		if value.Decimal != "" || strings.TrimSpace(value.Unit) == "" {
			return fmt.Errorf("%w: unknown quantity must omit decimal and retain unit", ErrInvalidEvidenceBundle)
		}
	case QuantityNotApplicable:
		if value.Decimal != "" || value.Unit != "" {
			return fmt.Errorf("%w: not-applicable quantity must omit decimal and unit", ErrInvalidEvidenceBundle)
		}
	default:
		return fmt.Errorf("%w: unknown quantity state %q", ErrInvalidEvidenceBundle, value.State)
	}
	return nil
}

func validateRegistry(value RegistryIdentity) error {
	if !validIdentityStatus(value.Status) {
		return fmt.Errorf("%w: invalid registry status", ErrInvalidEvidenceBundle)
	}
	if value.Status == IdentityConfirmed && (strings.TrimSpace(value.Identity) == "" || value.AsOfMS == nil) {
		return fmt.Errorf("%w: confirmed registry needs identity and as-of", ErrInvalidEvidenceBundle)
	}
	if value.Status == IdentityUnavailable && strings.TrimSpace(value.Reason) == "" {
		return fmt.Errorf("%w: unavailable registry needs reason", ErrInvalidEvidenceBundle)
	}
	return nil
}

func validateRevision(value RevisionIdentity) error {
	if !validIdentityStatus(value.Status) {
		return fmt.Errorf("%w: invalid revision status", ErrInvalidEvidenceBundle)
	}
	if value.Status == IdentityConfirmed && strings.TrimSpace(value.ID) == "" && strings.TrimSpace(value.InputRevision) == "" {
		return fmt.Errorf("%w: confirmed revision needs source-owned identity", ErrInvalidEvidenceBundle)
	}
	if value.Status == IdentityUnavailable && strings.TrimSpace(value.Reason) == "" {
		return fmt.Errorf("%w: unavailable revision needs reason", ErrInvalidEvidenceBundle)
	}
	return nil
}

func validateContribution(value ContributionIdentity, facts map[string]EvidenceFact) error {
	if !validIdentityStatus(value.Status) {
		return fmt.Errorf("%w: invalid contribution status", ErrInvalidEvidenceBundle)
	}
	if value.Status == IdentityConfirmed && (value.Method == "" || value.MethodVersion == "" || len(value.ComponentFactIDs) == 0 || value.ResidualFactID == "") {
		return fmt.Errorf("%w: confirmed contribution identity is incomplete", ErrInvalidEvidenceBundle)
	}
	if value.Status == IdentityUnavailable && value.Reason == "" {
		return fmt.Errorf("%w: unavailable contribution identity needs reason", ErrInvalidEvidenceBundle)
	}
	ids := append([]string{}, value.ComponentFactIDs...)
	ids = append(ids, value.OtherFactID, value.ResidualFactID)
	for _, id := range ids {
		if id != "" {
			if _, ok := facts[id]; !ok {
				return fmt.Errorf("%w: contribution refers to unknown fact %q", ErrInvalidEvidenceBundle, id)
			}
		}
	}
	return nil
}

func validateNotice(value NoticeEvidence, facts map[string]EvidenceFact) error {
	switch value.Status {
	case NoticeNotQueried, NoticeFailed, NoticeNotFound, NoticeFound:
	default:
		return fmt.Errorf("%w: invalid notice status %q", ErrInvalidEvidenceBundle, value.Status)
	}
	if value.SourceCount < 0 || value.HealthySourceCount < 0 || value.HealthySourceCount > value.SourceCount {
		return fmt.Errorf("%w: invalid notice source counts", ErrInvalidEvidenceBundle)
	}
	if value.Status == NoticeFound && len(value.FactIDs) == 0 {
		return fmt.Errorf("%w: found notice has no facts", ErrInvalidEvidenceBundle)
	}
	if value.Status != NoticeFound && len(value.FactIDs) != 0 {
		return fmt.Errorf("%w: non-found notice has facts", ErrInvalidEvidenceBundle)
	}
	for _, id := range value.FactIDs {
		if _, ok := facts[id]; !ok {
			return fmt.Errorf("%w: notice refers to unknown fact %q", ErrInvalidEvidenceBundle, id)
		}
	}
	return nil
}

func validIdentityStatus(value IdentityStatus) bool {
	return value == IdentityConfirmed || value == IdentityUnavailable || value == IdentityNotApplicable
}

func evidenceBundleDigestMaterial(snapshot EvidenceBundleSnapshot) any {
	copy := cloneEvidenceSnapshot(snapshot)
	copy.BundleID, copy.CreatedAtMS, copy.Cache.CalculationKey, copy.Cache.ExplanationKey = "", 0, "", ""
	return copy
}

func evidenceBundleCalculationMaterial(snapshot EvidenceBundleSnapshot) any {
	return struct {
		ContractVersion string
		CatalogVersion  string
		Question        QuestionContract
		Time            TimeSemantics
		Scope           ScopeIdentity
		Coverage        CoverageIdentity
		Denominators    []DenominatorIdentity
		Sources         []SourceIdentity
		Registry        RegistryIdentity
		Revision        RevisionIdentity
		Facts           []EvidenceFact
		Contribution    ContributionIdentity
		Notice          NoticeEvidence
		Replay          ReplayCapability
		Warnings        []string
	}{snapshot.ContractVersion, snapshot.CatalogVersion, snapshot.QuestionContract, snapshot.Time, snapshot.Scope, snapshot.Coverage, snapshot.Denominators, snapshot.Sources, snapshot.Registry, snapshot.Revision, snapshot.Facts, snapshot.Contribution, snapshot.Notice, snapshot.Replay, snapshot.Warnings}
}

func evidenceDigest(prefix string, value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return prefix + "_" + hex.EncodeToString(sum[:16])
}

func CanonicalFactID(value string) string {
	value = strings.TrimSpace(value)
	if ValidFactID(value) {
		return value
	}
	return evidenceFactID(value)
}

func ValidFactID(value string) bool {
	if len(value) != len("fact_")+32 || !strings.HasPrefix(value, "fact_") {
		return false
	}
	for _, char := range value[len("fact_"):] {
		if char < 'a' || char > 'p' {
			return false
		}
	}
	return true
}

func evidenceFactID(value any) string {
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	result := []byte("fact_")
	for _, item := range sum[:16] {
		result = append(result, 'a'+item>>4, 'a'+item&15)
	}
	return string(result)
}

func normalizeStrings(values *[]string, transform func(string) string) {
	seen := map[string]bool{}
	result := make([]string, 0, len(*values))
	for _, value := range *values {
		value = strings.TrimSpace(value)
		if transform != nil {
			value = transform(value)
		}
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	*values = result
}

func normalizeOrderedStrings(values *[]string) {
	for index := range *values {
		(*values)[index] = strings.TrimSpace((*values)[index])
	}
}

func cloneEvidenceSnapshot(value EvidenceBundleSnapshot) EvidenceBundleSnapshot {
	value.QuestionContract = cloneQuestionContract(value.QuestionContract)
	value.Fulfillment.Requirements = cloneRequirements(value.Fulfillment.Requirements)
	value.Time = cloneTimeSemantics(value.Time)
	value.Scope = cloneScopeIdentity(value.Scope)
	value.Denominators = cloneDenominators(value.Denominators)
	value.Sources = cloneSources(value.Sources)
	value.Registry = cloneRegistry(value.Registry)
	value.Facts = cloneFacts(value.Facts)
	value.Contribution = cloneContribution(value.Contribution)
	value.Notice = cloneNotice(value.Notice)
	value.Warnings = cloneValues(value.Warnings)
	return value
}

func cloneQuestionContract(value QuestionContract) QuestionContract {
	value.RequestedExplanation = cloneValues(value.RequestedExplanation)
	value.Requirements = cloneRequirements(value.Requirements)
	return value
}

func cloneRequirements(values []QuestionRequirement) []QuestionRequirement {
	result := append([]QuestionRequirement(nil), values...)
	for index := range result {
		result[index].FactIDs = cloneValues(result[index].FactIDs)
	}
	return result
}

func cloneTimeSemantics(value TimeSemantics) TimeSemantics {
	value.Requested = cloneTimeBoundary(value.Requested)
	value.Actual = cloneTimeBoundary(value.Actual)
	return value
}

func cloneTimeBoundary(value TimeBoundary) TimeBoundary {
	value.StartMS, value.EndMS, value.AsOfMS, value.ObservedAtMS = cloneInt64(value.StartMS), cloneInt64(value.EndMS), cloneInt64(value.AsOfMS), cloneInt64(value.ObservedAtMS)
	return value
}

func cloneScopeIdentity(value ScopeIdentity) ScopeIdentity {
	value.RequestedExchanges = cloneValues(value.RequestedExchanges)
	value.IncludedExchanges = cloneValues(value.IncludedExchanges)
	value.ExcludedExchanges = cloneValues(value.ExcludedExchanges)
	return value
}

func cloneDenominators(values []DenominatorIdentity) []DenominatorIdentity {
	return cloneValues(values)
}

func cloneSources(values []SourceIdentity) []SourceIdentity {
	result := cloneValues(values)
	for index := range result {
		result[index].ObservedAtMS = cloneInt64(result[index].ObservedAtMS)
	}
	return result
}

func cloneRegistry(value RegistryIdentity) RegistryIdentity {
	value.AsOfMS = cloneInt64(value.AsOfMS)
	return value
}

func cloneFacts(values []EvidenceFact) []EvidenceFact {
	result := cloneValues(values)
	for index := range result {
		result[index].DenominatorIDs = cloneValues(result[index].DenominatorIDs)
		result[index].Time.StartMS, result[index].Time.EndMS, result[index].Time.AsOfMS = cloneInt64(result[index].Time.StartMS), cloneInt64(result[index].Time.EndMS), cloneInt64(result[index].Time.AsOfMS)
		if result[index].Relation != nil {
			relation := *result[index].Relation
			relation.OperandFactIDs = cloneValues(relation.OperandFactIDs)
			result[index].Relation = &relation
		}
	}
	return result
}

func cloneContribution(value ContributionIdentity) ContributionIdentity {
	value.ComponentFactIDs = cloneValues(value.ComponentFactIDs)
	return value
}

func cloneNotice(value NoticeEvidence) NoticeEvidence {
	value.FactIDs = cloneValues(value.FactIDs)
	return value
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneValues[T any](values []T) []T {
	if values == nil {
		return nil
	}
	result := make([]T, len(values))
	copy(result, values)
	return result
}
