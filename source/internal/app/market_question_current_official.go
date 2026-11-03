package app

import (
	"fmt"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
)

func buildCurrentOfficialShareFallback(
	payload marketanalysis.Analysis,
	asOfMS int64,
) marketanalysis.MarketQuestionAnswer {
	return buildCurrentOfficialShareFallbackForQuery(payload, marketanalysis.MarketQuestionQuery{}, asOfMS)
}

func buildCurrentOfficialShareFallbackForQuery(
	payload marketanalysis.Analysis,
	query marketanalysis.MarketQuestionQuery,
	asOfMS int64,
) marketanalysis.MarketQuestionAnswer {
	exchange := strings.ToLower(strings.TrimSpace(query.Exchange))
	if exchange == "" {
		exchange = string(domain.ExchangeBithumb)
	}
	answer := buildCurrentOfficialExchangeFallback(payload, exchange, asOfMS)
	if query.PeerExchange != "" && query.Intent == marketanalysis.MarketQuestionCurrentOfficialShare {
		peer := buildCurrentOfficialExchangeFallback(payload, query.PeerExchange, asOfMS)
		answer.Answer += "\n\n" + peer.Answer
		answer.KeyPoints = append(answer.KeyPoints, peer.KeyPoints...)
	}
	return answer
}

func buildCurrentOfficialExchangeFallback(
	payload marketanalysis.Analysis,
	exchangeName string,
	asOfMS int64,
) marketanalysis.MarketQuestionAnswer {
	exchangeName = strings.ToLower(strings.TrimSpace(exchangeName))
	label := rollingExchangeLabel(exchangeName)
	summary := payload.OfficialDomestic24h
	snapshot, exists := summary.Exchanges[domain.Exchange(exchangeName)]
	if !exists || snapshot.Status != "LIVE" || snapshot.VolumeKRW == nil {
		status := snapshot.Status
		if strings.TrimSpace(status) == "" {
			status = summary.Status
		}
		if strings.TrimSpace(status) == "" {
			status = "UNAVAILABLE"
		}
		return marketanalysis.MarketQuestionAnswer{
			Answer:    fmt.Sprintf("현재 %s 공식 rolling 24시간 거래대금은 확정할 수 없습니다. 해당 티커 상태는 %s이며, 값을 빗썸 수치로 대체하지 않았습니다.", label, status),
			KeyPoints: []string{label + " 공식 rolling 24시간 티커 " + status},
			Caveat:    "현재값은 국내 5개 거래소의 공식 rolling 24시간 KRW 티커가 모두 LIVE일 때만 비교합니다. WebSocket 구간값으로 비어 있는 공식 티커를 보완하지 않습니다.",
			Source:    "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
		}
	}
	if summary.Status != "LIVE" || summary.TotalVolumeKRW == nil || *summary.TotalVolumeKRW <= 0 {
		return marketanalysis.MarketQuestionAnswer{
			Answer:    fmt.Sprintf("현재 %s 공식 rolling 24시간 거래대금은 %s입니다. 다만 국내 5개 거래소 티커가 모두 LIVE가 아니어서 국내 전체 비중은 확정하지 않았습니다.", label, formatQuestionKRWAmount(*snapshot.VolumeKRW)),
			KeyPoints: []string{label + " 공식 rolling 24시간 거래대금 " + formatQuestionKRWAmount(*snapshot.VolumeKRW), "국내 5개 거래소 비중 확정 전"},
			Caveat:    "선택한 거래소의 공식 티커 거래대금은 확인됐지만 국내시장 전체의 거래대금 집계가 불완전합니다. 누락된 거래소를 0원으로 간주해 비중을 만들지 않았습니다.",
			Source:    "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
		}
	}
	share := *snapshot.VolumeKRW / *summary.TotalVolumeKRW * 100
	answer := fmt.Sprintf(
		"현재 %s 공식 rolling 24시간 거래대금은 %s이며, 국내 5개 거래소 공식 거래대금 %s 중 %.2f%%를 차지합니다.",
		label, formatQuestionKRWAmount(*snapshot.VolumeKRW), formatQuestionKRWAmount(*summary.TotalVolumeKRW), share,
	)
	return marketanalysis.MarketQuestionAnswer{
		Answer: answer,
		KeyPoints: []string{
			label + " 공식 rolling 24시간 거래대금 " + formatQuestionKRWAmount(*snapshot.VolumeKRW),
			fmt.Sprintf("%s 국내 5개 거래소 비중 %.2f%%", label, share),
			"국내 5개 거래소 공식 rolling 24시간 거래대금 " + formatQuestionKRWAmount(*summary.TotalVolumeKRW),
		},
		Caveat: "이 값은 각 거래소 공식 rolling 24시간 KRW 티커의 현재 snapshot입니다. 특정 1시간 동안 발생한 거래대금이나 정렬 시간봉 24개 합계와는 정의가 다릅니다.",
		Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
	}
}

func enrichTargetExchangeCurrentTickerReference(
	answer marketanalysis.MarketQuestionAnswer,
	payload marketanalysis.Analysis,
	query marketanalysis.MarketQuestionQuery,
) marketanalysis.MarketQuestionAnswer {
	exchangeName := strings.ToLower(strings.TrimSpace(query.Exchange))
	if exchangeName == "" || exchangeName == string(domain.ExchangeBithumb) {
		return answer
	}
	summary := payload.OfficialDomestic24h
	snapshot, exists := summary.Exchanges[domain.Exchange(exchangeName)]
	if !exists || snapshot.Status != "LIVE" || snapshot.VolumeKRW == nil {
		return answer
	}
	label := rollingExchangeLabel(exchangeName)
	point := "현재 " + label + " 공식 rolling 24시간 거래대금 " + formatQuestionKRWAmount(*snapshot.VolumeKRW)
	if summary.Status == "LIVE" && summary.TotalVolumeKRW != nil && *summary.TotalVolumeKRW > 0 {
		share := *snapshot.VolumeKRW / *summary.TotalVolumeKRW * 100
		point += fmt.Sprintf(" · 국내 5개 거래소 비중 %.2f%%", share)
	} else {
		point += " · 국내 5개 거래소 비중 확정 전"
	}
	if !strings.Contains(answer.Answer, point) {
		answer.Answer = strings.TrimSpace(answer.Answer) + "\n\n" + point + "입니다. 이 현재 티커값은 1시간 WebSocket 구간의 공백을 보완하거나 1시간 변화로 환산하지 않았습니다."
	}
	if !marketQuestionContainsString(answer.KeyPoints, point) && len(answer.KeyPoints) < marketQuestionPointLimit {
		answer.KeyPoints = append(answer.KeyPoints, point)
	}
	return answer
}

func buildMarketQuestionDataQualityFallback(
	payload marketanalysis.Analysis,
	asOfMS int64,
) marketanalysis.MarketQuestionAnswer {
	paragraphs := make([]string, 0, 3)
	points := make([]string, 0, 4)

	official := payload.OfficialShareMonitor.Current
	if official != nil && official.Status == "LIVE" {
		paragraphs = append(paragraphs, fmt.Sprintf(
			"공식 정렬 24시간 자료 품질은 LIVE입니다. 최신 기준 시각은 KST %s이고 빗썸 점유율은 %.2f%%입니다.",
			marketQuestionKST(official.AsOfMS), official.BithumbSharePct,
		))
		points = append(points, "공식 정렬 24시간 자료 LIVE")
	} else {
		status := "UNAVAILABLE"
		if official != nil && strings.TrimSpace(official.Status) != "" {
			status = official.Status
		}
		paragraphs = append(paragraphs, "공식 정렬 24시간 자료는 현재 "+status+" 상태여서 점유율 수치를 확정하지 않았습니다.")
		points = append(points, "공식 정렬 24시간 자료 "+status)
	}

	coverageStatus := strings.TrimSpace(payload.Context.SelectedWindowStatus)
	if coverageStatus == "" {
		coverageStatus = "UNAVAILABLE"
	}
	if payload.Context.SelectedWindowComplete && payload.Summary.SelectedWindowComplete {
		paragraphs = append(paragraphs, fmt.Sprintf(
			"별도 WebSocket 직접 관측 rolling 구간은 %s이며 coverage는 %d/%d초로 완전합니다. 이 상태는 공식 시간봉 품질과 별도로 판단합니다.",
			coverageStatus,
			payload.Context.SelectedWindowCoverageSec,
			payload.Context.SelectedWindowRequiredSec,
		))
	} else {
		paragraphs = append(paragraphs, fmt.Sprintf(
			"별도 WebSocket 직접 관측 rolling 구간은 %s이고 coverage는 %d/%d초로 불완전합니다. 따라서 해당 구간의 체결 점유율은 확정값으로 사용하지 않습니다.",
			coverageStatus,
			payload.Context.SelectedWindowCoverageSec,
			payload.Context.SelectedWindowRequiredSec,
		))
	}
	points = append(points, fmt.Sprintf(
		"WebSocket rolling coverage %d/%d초 · %s",
		payload.Context.SelectedWindowCoverageSec,
		payload.Context.SelectedWindowRequiredSec,
		coverageStatus,
	))

	if payload.Summary.DomesticUnresolvedFXCount > 0 {
		paragraphs = append(paragraphs, fmt.Sprintf(
			"국내 거래대금 원화 환산 미해결 항목은 %d건입니다. 이 항목은 점유율의 국내시장 거래금액에 조용히 추정해서 넣지 않습니다.",
			payload.Summary.DomesticUnresolvedFXCount,
		))
		points = append(points, fmt.Sprintf("국내 원화 환산 미해결 %d건", payload.Summary.DomesticUnresolvedFXCount))
	} else {
		points = append(points, "국내 원화 환산 미해결 0건")
	}

	return marketanalysis.MarketQuestionAnswer{
		Answer: strings.Join(paragraphs, "\n\n"), KeyPoints: points,
		Caveat: "LIVE·CONFIRMED·coverage는 자료 가용성과 완전성을 뜻하며 시장 방향 신호가 아닙니다. 공식 정렬 시간봉과 WebSocket 직접 관측 자료의 상태를 서로 대신 사용하지 않습니다.",
		Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
	}
}
