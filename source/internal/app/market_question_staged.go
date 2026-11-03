package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/marketquestion"
)

const marketQuestionACMode = "QUANTITATIVE_FIRST_A_PLUS_C_V1"

func marketQuestionCloneResponse(response marketanalysis.GroundedMarketQuestionResponse) (marketanalysis.GroundedMarketQuestionResponse, error) {
	// The browser contract requires an array even before prose is available.
	// Normalize at the shared stage boundary, including failure and cache paths.
	if response.Prose.KeyPoints == nil {
		response.Prose.KeyPoints = []string{}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	var cloned marketanalysis.GroundedMarketQuestionResponse
	if err = json.Unmarshal(encoded, &cloned); err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	return cloned, nil
}

func (s *Service) marketQuestionPublishQuantitative(
	ctx context.Context,
	question string,
	response marketanalysis.GroundedMarketQuestionResponse,
	bundle marketquestion.EvidenceBundle,
	options marketQuestionRunOptions,
) error {
	state := options.state
	if state == nil {
		return errors.New("market question staged state is required")
	}
	snapshot := bundle.Snapshot()
	state.bundle = &snapshot
	response.Prose = marketanalysis.MarketQuestionProse{}
	response.Provider.Source = "SERVER_CALCULATION"
	response.Provider.Model = ""
	response.Provider.FallbackUsed = false
	response.EvidenceBundle = &snapshot
	retention := s.retainMarketQuestionEvidence(ctx, bundle)
	response.EvidenceRetention = &retention
	scope, err := s.signMarketQuestionConversationScope(marketanalysis.MarketQuestionConversationScope{
		Query: response.Query, EvidenceBundleID: snapshot.BundleID, Revision: snapshot.Revision,
		CreatedAtMS: snapshot.CreatedAtMS, FulfillmentStatus: snapshot.Fulfillment.Status,
		AsOfMS: response.Provider.AsOfMS,
	})
	if err != nil {
		return err
	}
	response.ConversationScope = &scope
	now := time.Now()
	ready := now.UnixMilli()
	state.timing.QuantitativeReady = &ready
	if state.timing.RetrievalCalculation == nil && !state.retrievalStarted.IsZero() {
		value := now.Sub(state.retrievalStarted).Milliseconds()
		state.timing.RetrievalCalculation = &value
	}
	response.Orchestration = &marketanalysis.MarketQuestionOrchestration{
		Mode: marketQuestionACMode, OverallStatus: marketanalysis.MarketQuestionOverallPending,
		QuantitativeStatus: string(snapshot.Fulfillment.Status),
		ExplanationStatus:  marketanalysis.MarketQuestionExplanationPending,
		ValidationStatus:   marketanalysis.MarketQuestionValidationUndetermined,
		Blocks:             []marketanalysis.MarketQuestionExplanationBlock{},
		Calls:              state.calls, TimingMS: state.timing,
	}
	state.quantitative = nil
	quantitative, err := marketQuestionCloneResponse(response)
	if err != nil {
		return err
	}
	state.quantitative = &quantitative
	return marketQuestionEmitStage(options, marketanalysis.MarketQuestionStageQuantitativeReady, marketanalysis.MarketQuestionStageReady, quantitative)
}

func marketQuestionEmitStage(
	options marketQuestionRunOptions,
	stage marketanalysis.MarketQuestionStage,
	status marketanalysis.MarketQuestionStageStatus,
	response marketanalysis.GroundedMarketQuestionResponse,
) error {
	if options.emit == nil || options.state == nil {
		return errors.New("market question stage emitter is unavailable")
	}
	if response.Orchestration != nil {
		response.Orchestration.Calls = options.state.calls
		response.Orchestration.TimingMS = options.state.timing
	}
	cloned, err := marketQuestionCloneResponse(response)
	if err != nil {
		return err
	}
	options.state.sequence++
	err = options.emit(marketanalysis.MarketQuestionStageEvent{
		Sequence: options.state.sequence, Stage: stage, Status: status,
		EmittedAtMS: time.Now().UnixMilli(), Response: cloned,
	})
	if err != nil {
		options.state.emitFailed = true
	}
	return err
}

func marketQuestionSetTerminalFailure(
	response *marketanalysis.GroundedMarketQuestionResponse,
	runErr error,
	state *marketQuestionRunState,
) {
	if response == nil || state == nil {
		return
	}
	explanation := marketanalysis.MarketQuestionExplanationFailed
	overall := marketanalysis.MarketQuestionOverallPartial
	switch {
	case errors.Is(runErr, context.DeadlineExceeded):
		explanation = marketanalysis.MarketQuestionExplanationTimeout
		overall = marketanalysis.MarketQuestionOverallTimeout
	case errors.Is(runErr, context.Canceled):
		explanation = marketanalysis.MarketQuestionExplanationCanceled
		overall = marketanalysis.MarketQuestionOverallCanceled
	}
	now := time.Now()
	total := now.Sub(state.started).Milliseconds()
	state.timing.Total = &total
	response.Prose = marketanalysis.MarketQuestionProse{}
	response.Orchestration = &marketanalysis.MarketQuestionOrchestration{
		Mode: marketQuestionACMode, OverallStatus: overall,
		QuantitativeStatus: marketQuestionQuantitativeStatus(*response),
		ExplanationStatus:  explanation, ValidationStatus: marketanalysis.MarketQuestionValidationUndetermined,
		Blocks: []marketanalysis.MarketQuestionExplanationBlock{{
			Index: 0, Status: marketanalysis.MarketQuestionValidationUndetermined,
			Reason: "EXPLANATION_CALL_DID_NOT_COMPLETE", ClaimRefs: []string{},
		}},
		Calls: state.calls, TimingMS: state.timing,
	}
}

func marketQuestionStageStatusForResponse(response marketanalysis.GroundedMarketQuestionResponse) marketanalysis.MarketQuestionStageStatus {
	if response.Orchestration == nil {
		return marketanalysis.MarketQuestionStageFailed
	}
	switch response.Orchestration.ExplanationStatus {
	case marketanalysis.MarketQuestionExplanationAccepted:
		return marketanalysis.MarketQuestionStageAccepted
	case marketanalysis.MarketQuestionExplanationPartial:
		return marketanalysis.MarketQuestionStagePartial
	case marketanalysis.MarketQuestionExplanationTimeout:
		return marketanalysis.MarketQuestionStageTimeout
	case marketanalysis.MarketQuestionExplanationCanceled:
		return marketanalysis.MarketQuestionStageCanceled
	case marketanalysis.MarketQuestionExplanationUnverified:
		return marketanalysis.MarketQuestionStageUnverified
	case marketanalysis.MarketQuestionExplanationFailed:
		return marketanalysis.MarketQuestionStageFailed
	default:
		return marketanalysis.MarketQuestionStageReady
	}
}

func marketQuestionQuantitativeStatus(response marketanalysis.GroundedMarketQuestionResponse) string {
	if response.EvidenceBundle != nil {
		return string(response.EvidenceBundle.Fulfillment.Status)
	}
	return "UNAVAILABLE"
}

func marketQuestionApplyCachedExplanation(
	quantitative marketanalysis.GroundedMarketQuestionResponse,
	cached marketanalysis.GroundedMarketQuestionResponse,
	state *marketQuestionRunState,
) (marketanalysis.GroundedMarketQuestionResponse, error) {
	response, err := marketQuestionCloneResponse(quantitative)
	if err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	cachedClone, err := marketQuestionCloneResponse(cached)
	if err != nil {
		return marketanalysis.GroundedMarketQuestionResponse{}, err
	}
	response.Prose = cachedClone.Prose
	response.Provider = cachedClone.Provider
	response.Orchestration = cachedClone.Orchestration
	if response.Orchestration == nil {
		return response, nil
	}
	response.Orchestration.Calls = state.calls
	response.Orchestration.TimingMS = state.timing
	return response, nil
}
