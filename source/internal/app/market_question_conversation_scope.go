package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	marketanalysis "market-share-go/internal/analysis"
)

const marketQuestionConversationScopeContract = "GROUNDED_MARKET_QUESTION_SCOPE_V1"

func (s *Service) signMarketQuestionConversationScope(scope marketanalysis.MarketQuestionConversationScope) (marketanalysis.MarketQuestionConversationScope, error) {
	if s == nil {
		return marketanalysis.MarketQuestionConversationScope{}, errors.New("market question service is unavailable")
	}
	s.hermesQuestionMu.Lock()
	defer s.hermesQuestionMu.Unlock()
	if !s.marketQuestionScopeKeyReady {
		if _, err := rand.Read(s.marketQuestionScopeKey[:]); err != nil {
			return marketanalysis.MarketQuestionConversationScope{}, fmt.Errorf("create market question scope key: %w", err)
		}
		s.marketQuestionScopeKeyReady = true
	}
	scope.Contract = marketQuestionConversationScopeContract
	scope.Token = ""
	payload, err := json.Marshal(scope)
	if err != nil {
		return marketanalysis.MarketQuestionConversationScope{}, err
	}
	mac := hmac.New(sha256.New, s.marketQuestionScopeKey[:])
	_, _ = mac.Write(payload)
	scope.Token = base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return scope, nil
}

func (s *Service) validateMarketQuestionConversationScope(scope *marketanalysis.MarketQuestionConversationScope) (marketanalysis.MarketQuestionConversationScope, error) {
	if scope == nil {
		return marketanalysis.MarketQuestionConversationScope{}, nil
	}
	provided := strings.TrimSpace(scope.Token)
	if scope.Contract != marketQuestionConversationScopeContract || provided == "" || strings.TrimSpace(scope.EvidenceBundleID) == "" || scope.CreatedAtMS <= 0 || scope.AsOfMS <= 0 {
		return marketanalysis.MarketQuestionConversationScope{}, marketanalysis.ErrInvalidMarketQuestion
	}
	copyScope := *scope
	copyScope.Token = ""
	payload, err := json.Marshal(copyScope)
	if err != nil {
		return marketanalysis.MarketQuestionConversationScope{}, marketanalysis.ErrInvalidMarketQuestion
	}
	decoded, err := base64.RawURLEncoding.DecodeString(provided)
	if err != nil {
		return marketanalysis.MarketQuestionConversationScope{}, marketanalysis.ErrInvalidMarketQuestion
	}
	s.hermesQuestionMu.Lock()
	defer s.hermesQuestionMu.Unlock()
	if !s.marketQuestionScopeKeyReady {
		return marketanalysis.MarketQuestionConversationScope{}, marketanalysis.ErrInvalidMarketQuestion
	}
	mac := hmac.New(sha256.New, s.marketQuestionScopeKey[:])
	_, _ = mac.Write(payload)
	if !hmac.Equal(decoded, mac.Sum(nil)) {
		return marketanalysis.MarketQuestionConversationScope{}, marketanalysis.ErrInvalidMarketQuestion
	}
	if err := validateMarketQuestionQuery(copyScope.Query, time.UnixMilli(copyScope.AsOfMS)); err != nil {
		return marketanalysis.MarketQuestionConversationScope{}, marketanalysis.ErrInvalidMarketQuestion
	}
	copyScope.Token = provided
	return copyScope, nil
}

func marketQuestionHistoryWithConfirmedScope(
	history []marketanalysis.MarketQuestionConversationTurn,
	scope marketanalysis.MarketQuestionConversationScope,
) []marketanalysis.MarketQuestionConversationTurn {
	if strings.TrimSpace(scope.Contract) == "" {
		return history
	}
	// The token is verified before this function. Preserve the exact typed query
	// for the planner and explanation prompt; never round-trip its time basis,
	// comparison or metric through a lossy natural-language summary.
	encoded, err := json.Marshal(scope.Query)
	if err != nil {
		return history
	}
	result := append([]marketanalysis.MarketQuestionConversationTurn(nil), history...)
	result = append(result, marketanalysis.MarketQuestionConversationTurn{
		Question: "서버 확정 분석 조건(JSON): " + string(encoded),
		Answer:   "이 조건은 서명된 conversation_scope에서 복원되었습니다.",
	})
	if len(result) > marketanalysis.MaxMarketQuestionConversationTurns {
		result = result[len(result)-marketanalysis.MaxMarketQuestionConversationTurns:]
	}
	return result
}

// marketQuestionResolveSignedFollowUp inherits the server-signed query as a
// typed value. Explicit fields in the new question may replace their matching
// dimension, but omitted fields never pass through a prose reconstruction.
func marketQuestionResolveSignedFollowUp(
	question string,
	previous marketanalysis.MarketQuestionQuery,
	assetAliases map[string]string,
	reference time.Time,
) (marketanalysis.MarketQuestionQuery, error) {
	if !marketQuestionIsConversationalFollowUp(question) {
		return parseMarketQuestionAt(question, assetAliases, reference)
	}
	query := previous
	query.TargetDates = append([]string(nil), previous.TargetDates...)
	query.QuestionParts = append([]string(nil), previous.QuestionParts...)
	query.RequestedExplanation = append([]string(nil), previous.RequestedExplanation...)

	exchange, err := parseMarketQuestionExchange(question)
	if err != nil {
		return marketanalysis.MarketQuestionQuery{}, err
	}
	asset, err := parseMarketQuestionAsset(question, assetAliases)
	if err != nil {
		return marketanalysis.MarketQuestionQuery{}, err
	}
	if exchange != "" {
		query.Exchange = exchange
		query.PeerExchange = ""
		if exchange == "bithumb" {
			if query.Metric == marketanalysis.MarketQuestionMetricTargetExchange {
				query.Metric = marketanalysis.MarketQuestionMetricOverallShare
			}
		} else if asset == "" && query.Asset == "" {
			query.Metric = marketanalysis.MarketQuestionMetricTargetExchange
		}
	}
	if asset != "" {
		query.Asset = asset
		query.Metric = marketQuestionAssetMetric(question)
	}

	duration, durationFound, err := parseMarketQuestionDuration(question)
	if err != nil {
		return marketanalysis.MarketQuestionQuery{}, err
	}
	dates, err := parseMarketQuestionCalendarDates(question, reference)
	if err != nil {
		return marketanalysis.MarketQuestionQuery{}, err
	}
	clockCount := len(marketQuestionClockPattern.FindAllStringSubmatch(question, -1))
	if clockCount != 0 && clockCount != 2 || marketQuestionUnsupportedKoreanClockPattern.MatchString(question) {
		return marketanalysis.MarketQuestionQuery{}, errInvalidMarketQuestionWindow
	}
	compact := strings.ReplaceAll(strings.ToLower(question), " ", "")
	sameClockDate := len(dates) == 1 && strings.Contains(compact, "같은시간") &&
		previous.TargetStartMS > 0 && previous.TargetEndMS > previous.TargetStartMS
	switch {
	case sameClockDate:
		priorStart := time.UnixMilli(previous.TargetStartMS).In(marketQuestionKSTLocation)
		start := time.Date(dates[0].Year(), dates[0].Month(), dates[0].Day(), priorStart.Hour(), priorStart.Minute(), priorStart.Second(), priorStart.Nanosecond(), marketQuestionKSTLocation)
		query.Intent = marketanalysis.MarketQuestionHistoricalRange
		query.Comparison = marketanalysis.MarketQuestionFixedHistoricalInterval
		query.EndPolicy = "EXACT_USER_TIME_RANGE_KST"
		query.TargetDate = start.Format("2006-01-02")
		query.TargetDates = nil
		query.TargetStartMS = start.UnixMilli()
		query.TargetEndMS = start.Add(time.Duration(previous.TargetEndMS-previous.TargetStartMS) * time.Millisecond).UnixMilli()
	case len(dates) > 0 || clockCount == 2:
		parsed, parseErr := parseMarketQuestionAt(question, assetAliases, reference)
		if parseErr != nil {
			return marketanalysis.MarketQuestionQuery{}, parseErr
		}
		if exchange == "" {
			parsed.Exchange = query.Exchange
		}
		if asset == "" {
			parsed.Asset = query.Asset
		}
		query = parsed
		if query.Exchange != "" && query.Exchange != "bithumb" && query.Asset == "" {
			query.Metric = marketanalysis.MarketQuestionMetricTargetExchange
		}
	case durationFound:
		query.TargetDate, query.TargetDates = "", nil
		query.TargetStartMS, query.TargetEndMS = 0, 0
		applyMarketQuestionDuration(&query, duration, question)
		applyMarketQuestionExchangeInterval(&query)
	case marketQuestionExplicitCurrentFollowUp(question):
		query.Intent = marketanalysis.MarketQuestionCurrentOfficialShare
		query.Comparison = marketanalysis.MarketQuestionCurrentSnapshot
		query.TimeValue, query.TimeUnit = 0, marketanalysis.MarketQuestionTimeNone
		query.EndPolicy = "LATEST_CLOSED_BOUNDARY"
		query.TargetDate, query.TargetDates = "", nil
		query.TargetStartMS, query.TargetEndMS = 0, 0
		if query.Asset != "" {
			query.Intent = marketanalysis.MarketQuestionAssetCapture
		}
	}

	if strings.Contains(compact, "거래대금말고") && strings.Contains(compact, "점유율") {
		if query.Exchange == "" || query.Exchange == "bithumb" {
			query.Metric = marketanalysis.MarketQuestionMetricOverallShare
		} else {
			query.Metric = marketanalysis.MarketQuestionMetricTargetExchange
		}
	}
	query.AnalysisDepth = marketQuestionAnalysisDepth(question)
	query.RequestedExplanation = mergeMarketQuestionExplanations(query.RequestedExplanation, marketQuestionExplanations(question))
	if err := validateMarketQuestionQuery(query, reference); err != nil {
		return marketanalysis.MarketQuestionQuery{}, err
	}
	return query, nil
}
