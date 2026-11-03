package marketquestion

import (
	"fmt"
	"sort"
	"strings"
)

type AggregationPolicyID string

const (
	AggregationPolicyAdditiveAmount      AggregationPolicyID = "ADDITIVE_NON_OVERLAPPING_INTERVALS"
	AggregationPolicyRatioFromComponents AggregationPolicyID = "RATIO_RECOMPUTE_FROM_NUMERATOR_DENOMINATOR"
	AggregationPolicyEndpointDelta       AggregationPolicyID = "ENDPOINT_DIFFERENCE_ONLY"
	AggregationPolicyContribution        AggregationPolicyID = "CONTRIBUTION_COMPONENTS_WITH_RESIDUAL"
	AggregationPolicyComposite           AggregationPolicyID = "COMPOSITE_NO_IMPLICIT_AGGREGATION"
)

type AggregationOperationID string

const (
	AggregationDirectValue            AggregationOperationID = "DIRECT_VALUE"
	AggregationSumNonOverlapping      AggregationOperationID = "SUM_NON_OVERLAPPING_INTERVALS"
	AggregationRecomputeRatio         AggregationOperationID = "RECOMPUTE_RATIO_FROM_COMPONENT_SUMS"
	AggregationEndpointDifference     AggregationOperationID = "ENDPOINT_DIFFERENCE"
	AggregationReconcileContributions AggregationOperationID = "RECONCILE_CONTRIBUTIONS_WITH_RESIDUAL"
	AggregationArithmeticAverage      AggregationOperationID = "ARITHMETIC_AVERAGE"
	AggregationSumRollingSnapshots    AggregationOperationID = "SUM_ROLLING_SNAPSHOTS"
)

type AggregationPolicyDefinition struct {
	ID                        AggregationPolicyID      `json:"id"`
	Description               string                   `json:"description"`
	AllowedOperations         []AggregationOperationID `json:"allowed_operations"`
	CrossSourceOverlapAllowed bool                     `json:"cross_source_overlap_allowed"`
	RollingSnapshotSumAllowed bool                     `json:"rolling_snapshot_sum_allowed"`
}

var aggregationPolicyDefinitions = []AggregationPolicyDefinition{
	{
		ID:                AggregationPolicyAdditiveAmount,
		Description:       "동일 의미·출처의 겹치지 않는 구간 금액 또는 건수만 합산",
		AllowedOperations: []AggregationOperationID{AggregationDirectValue, AggregationSumNonOverlapping, AggregationEndpointDifference},
	},
	{
		ID:                AggregationPolicyRatioFromComponents,
		Description:       "구간 비율은 시간별 비율 평균이 아니라 합산한 분자와 분모에서 재계산",
		AllowedOperations: []AggregationOperationID{AggregationDirectValue, AggregationRecomputeRatio, AggregationEndpointDifference},
	},
	{
		ID:                AggregationPolicyEndpointDelta,
		Description:       "수준 값의 동일 의미 endpoint 차이만 계산하며 rolling snapshot을 합산하지 않음",
		AllowedOperations: []AggregationOperationID{AggregationDirectValue, AggregationEndpointDifference},
	},
	{
		ID:                AggregationPolicyContribution,
		Description:       "산술 기여 구성요소는 전체 변화와 기타·잔차를 함께 보존해 대조",
		AllowedOperations: []AggregationOperationID{AggregationDirectValue, AggregationReconcileContributions},
	},
	{
		ID:                AggregationPolicyComposite,
		Description:       "복합 지표는 하위 typed 지표를 보존하며 암시적으로 합치지 않음",
		AllowedOperations: []AggregationOperationID{AggregationDirectValue},
	},
}

func AggregationPolicies() []AggregationPolicyDefinition {
	result := append([]AggregationPolicyDefinition(nil), aggregationPolicyDefinitions...)
	for index := range result {
		result[index].AllowedOperations = append([]AggregationOperationID(nil), result[index].AllowedOperations...)
	}
	return result
}

func AggregationPolicy(id AggregationPolicyID) (AggregationPolicyDefinition, bool) {
	for _, definition := range aggregationPolicyDefinitions {
		if definition.ID == id {
			definition.AllowedOperations = append([]AggregationOperationID(nil), definition.AllowedOperations...)
			return definition, true
		}
	}
	return AggregationPolicyDefinition{}, false
}

// AggregationPolicyForFactMetric binds request-path typed facts to the same
// catalogued rules used during bundle admission. Unknown metrics are composite
// and therefore cannot gain additive behavior by omission.
func AggregationPolicyForFactMetric(metric string) AggregationPolicyID {
	metric = strings.ToUpper(strings.TrimSpace(metric))
	switch {
	case strings.Contains(metric, "ATTRIBUTION"), strings.Contains(metric, "CONTRIBUTION_SUM"), strings.Contains(metric, "RESIDUAL"):
		return AggregationPolicyContribution
	case strings.HasSuffix(metric, "_DELTA_PP"), strings.HasSuffix(metric, "_GAP_PP"), strings.HasSuffix(metric, "_CHANGE_PP"):
		return AggregationPolicyEndpointDelta
	case strings.HasSuffix(metric, "_PCT"), strings.HasSuffix(metric, "_PERCENT"):
		return AggregationPolicyRatioFromComponents
	case strings.HasSuffix(metric, "_KRW"), strings.HasSuffix(metric, "_COUNT"):
		return AggregationPolicyAdditiveAmount
	default:
		return AggregationPolicyComposite
	}
}

func ValidateAggregationUse(metric string, policyID AggregationPolicyID, operation AggregationOperationID) error {
	expected := AggregationPolicyForFactMetric(metric)
	if policyID != expected {
		return fmt.Errorf("metric %q policy=%q want=%q", metric, policyID, expected)
	}
	policy, ok := AggregationPolicy(policyID)
	if !ok {
		return fmt.Errorf("unknown aggregation policy %q", policyID)
	}
	for _, allowed := range policy.AllowedOperations {
		if allowed == operation {
			return nil
		}
	}
	return fmt.Errorf("operation %q is forbidden by policy %q", operation, policyID)
}

// ValidateAggregationRelation applies source and interval rules to a concrete
// request-path fact relation. A result LIMIT or valid numeric value cannot make
// overlapping or cross-source operands safe.
func ValidateAggregationRelation(result EvidenceFact, facts map[string]EvidenceFact) error {
	if result.Relation == nil {
		return nil
	}
	if len(result.Relation.OperandFactIDs) == 0 {
		return fmt.Errorf("aggregation relation has no operands")
	}
	operands := make([]EvidenceFact, 0, len(result.Relation.OperandFactIDs))
	seen := map[string]bool{}
	for _, id := range result.Relation.OperandFactIDs {
		if id == "" || seen[id] {
			return fmt.Errorf("aggregation relation has empty or duplicate operand")
		}
		seen[id] = true
		fact, ok := facts[id]
		if !ok {
			return fmt.Errorf("unknown operand %q", id)
		}
		operands = append(operands, fact)
	}
	if result.Value.State == QuantityConfirmedValue || result.Value.State == QuantityConfirmedZero {
		for _, operand := range operands {
			if operand.Value.State != QuantityConfirmedValue && operand.Value.State != QuantityConfirmedZero {
				return fmt.Errorf("confirmed result uses unconfirmed operand %q", operand.ID)
			}
		}
	}
	policy, _ := AggregationPolicy(result.AggregationPolicy)
	if result.SourceID != operands[0].SourceID {
		return fmt.Errorf("result and operands use different sources")
	}
	if !policy.CrossSourceOverlapAllowed {
		for _, operand := range operands[1:] {
			if operand.SourceID != operands[0].SourceID {
				return fmt.Errorf("cross-source aggregation is forbidden")
			}
		}
	}
	if result.Relation.Operation == AggregationSumNonOverlapping {
		intervals := make([]FactTimeReference, 0, len(operands))
		if result.Metric != operands[0].Metric || result.Value.Unit != operands[0].Value.Unit || !sameFactSubject(result.Subject, operands[0].Subject) {
			return fmt.Errorf("sum result has incompatible metric, unit, or subject")
		}
		for _, operand := range operands {
			if operand.Metric != operands[0].Metric || operand.Value.Unit != operands[0].Value.Unit || !sameFactSubject(operand.Subject, operands[0].Subject) {
				return fmt.Errorf("sum operands have incompatible metric, unit, or subject")
			}
			if operand.Time.StartMS == nil || operand.Time.EndMS == nil || *operand.Time.StartMS >= *operand.Time.EndMS {
				return fmt.Errorf("sum operand %q has no closed interval", operand.ID)
			}
			intervals = append(intervals, operand.Time)
		}
		sort.Slice(intervals, func(i, j int) bool { return *intervals[i].StartMS < *intervals[j].StartMS })
		for index := 1; index < len(intervals); index++ {
			if *intervals[index].StartMS < *intervals[index-1].EndMS {
				return fmt.Errorf("overlapping interval aggregation is forbidden")
			}
		}
		if result.Time.StartMS == nil || result.Time.EndMS == nil || *result.Time.StartMS != *intervals[0].StartMS || *result.Time.EndMS != *intervals[len(intervals)-1].EndMS {
			return fmt.Errorf("sum result time does not equal operand interval union")
		}
	}
	if result.Relation.Operation == AggregationRecomputeRatio && len(operands) != 2 {
		return fmt.Errorf("ratio recomputation requires numerator and denominator")
	}
	if result.Relation.Operation == AggregationRecomputeRatio {
		if result.Value.Unit != "PCT" || operands[0].Value.Unit == "" || operands[0].Value.Unit != operands[1].Value.Unit || !sameFactTime(operands[0].Time, operands[1].Time) {
			return fmt.Errorf("ratio operands have incompatible unit or time")
		}
		if operands[0].Subject.Asset != operands[1].Subject.Asset || operands[0].Subject.MarketScope != operands[1].Subject.MarketScope {
			return fmt.Errorf("ratio operands have incompatible market or asset scope")
		}
		if !sameFactSubject(result.Subject, operands[0].Subject) || !sameFactTime(result.Time, operands[0].Time) {
			return fmt.Errorf("ratio result does not match numerator subject or time")
		}
	}
	if result.Relation.Operation == AggregationEndpointDifference {
		if len(operands) != 2 || operands[0].Metric != operands[1].Metric || operands[0].Value.Unit != operands[1].Value.Unit || !sameFactSubject(operands[0].Subject, operands[1].Subject) {
			return fmt.Errorf("endpoint difference operands have incompatible metric, unit, or subject")
		}
		wantUnit := operands[0].Value.Unit
		if wantUnit == "PCT" {
			wantUnit = "PP"
		}
		if result.Value.Unit != wantUnit {
			return fmt.Errorf("endpoint difference result unit=%q want=%q", result.Value.Unit, wantUnit)
		}
		if !sameFactSubject(result.Subject, operands[0].Subject) {
			return fmt.Errorf("endpoint difference result subject does not match operands")
		}
		if !orderedFactTimes(operands[0].Time, operands[1].Time) {
			return fmt.Errorf("endpoint difference operands are not previous then current")
		}
		if !endpointResultTime(result.Time, operands[0].Time, operands[1].Time) {
			return fmt.Errorf("endpoint difference result time does not span operands")
		}
	}
	if result.Relation.Operation == AggregationReconcileContributions {
		if result.Value.Unit != "PP" {
			return fmt.Errorf("contribution reconciliation result must use PP")
		}
		for _, operand := range operands {
			if operand.Value.Unit != "PP" || !sameFactTime(result.Time, operand.Time) {
				return fmt.Errorf("contribution operands have incompatible unit or time")
			}
			if result.Subject.MarketScope != operand.Subject.MarketScope || result.Subject.Exchange != operand.Subject.Exchange {
				return fmt.Errorf("contribution result and operands have incompatible subject scope")
			}
		}
	}
	return nil
}

func endpointResultTime(result, previous, current FactTimeReference) bool {
	previousBoundary := previous.StartMS
	if previous.AsOfMS != nil {
		previousBoundary = previous.AsOfMS
	}
	currentBoundary := current.EndMS
	if current.AsOfMS != nil {
		currentBoundary = current.AsOfMS
	}
	return result.Role == "COMPARISON" && equalInt64(result.StartMS, previousBoundary) && equalInt64(result.EndMS, currentBoundary)
}

func sameFactSubject(left, right FactSubject) bool {
	return left.MarketScope == right.MarketScope && left.Exchange == right.Exchange && left.Asset == right.Asset
}

func sameFactTime(left, right FactTimeReference) bool {
	return left.Role == right.Role && equalInt64(left.StartMS, right.StartMS) && equalInt64(left.EndMS, right.EndMS) && equalInt64(left.AsOfMS, right.AsOfMS)
}

func orderedFactTimes(previous, current FactTimeReference) bool {
	if previous.Role != "PREVIOUS" || current.Role != "CURRENT" {
		return false
	}
	if previous.AsOfMS != nil && current.AsOfMS != nil {
		return *previous.AsOfMS < *current.AsOfMS
	}
	if previous.EndMS != nil && current.StartMS != nil {
		return *previous.EndMS <= *current.StartMS
	}
	return false
}

func equalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
