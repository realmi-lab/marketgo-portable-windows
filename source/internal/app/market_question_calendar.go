package app

import (
	"fmt"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/tickertrend"
)

func buildCalendarDayShareFallback(
	day tickertrend.WindowComparison,
	query marketanalysis.MarketQuestionQuery,
	asOfMS int64,
) marketanalysis.MarketQuestionAnswer {
	dateLabel := strings.TrimSpace(query.TargetDate)
	if dateLabel == "" {
		dateLabel = "요청한 날짜"
	}
	if day.Status != "LIVE" || day.Current == nil {
		points := []string{
			fmt.Sprintf("%s KST 마감 정합 표본 미확보", dateLabel),
			fmt.Sprintf("해당 날짜 1시간 경계 표본 %d/%d개", day.ObservedSampleCount, day.ExpectedSampleCount),
		}
		if reason := marketQuestionReasonLabel(day.Reason); reason != "" {
			points = append(points, "미확보 사유: "+reason)
		}
		return marketanalysis.MarketQuestionAnswer{
			Answer: fmt.Sprintf(
				"%s 빗썸 점유율은 현재 DB에서 확정할 수 없습니다. KST 해당 날짜가 끝나는 다음 날 00:00의 정확한 정렬 시간봉 마감 표본이 없으며, 가장 가까운 시각의 값으로 대체하지 않았습니다.",
				dateLabel,
			),
			KeyPoints: points,
			Caveat:    "날짜 마감은 KST 자정→자정으로 맞춘 거래소별 1시간봉으로 판정합니다. 빗썸 ticker 자체의 09시 경계나 불완전한 WebSocket 누적값을 날짜 마감으로 대체하지 않습니다.",
			Source:    "SERVER_BRIEFING_FALLBACK", AsOfMS: asOfMS,
		}
	}

	close := day.Current
	paragraphs := []string{fmt.Sprintf(
		"%s KST 자정→자정 기준 공식 빗썸 점유율은 %.4f%%입니다. 기준 시각은 KST %s이며, 같은 24개 정렬 1시간봉을 합산한 국내(업비트·빗썸·코인원) 거래대금 %s 중 빗썸 거래대금은 %s입니다.",
		dateLabel, close.BithumbSharePct, marketQuestionKST(close.BucketMS),
		formatQuestionKRWAmount(close.TotalVolumeKRW), formatQuestionKRWAmount(close.BithumbVolumeKRW),
	)}
	points := []string{
		fmt.Sprintf("%s KST 마감 빗썸 점유율 %.4f%%", dateLabel, close.BithumbSharePct),
		"국내(업비트·빗썸·코인원) 공식 KST 일일 거래대금 " + formatQuestionKRWAmount(close.TotalVolumeKRW),
		"빗썸 공식 KST 일일 거래대금 " + formatQuestionKRWAmount(close.BithumbVolumeKRW),
	}
	if day.PathComplete {
		if paragraph, point := officialMarketQuestionPathText(day.PathSummary); paragraph != "" {
			paragraphs = append(paragraphs, paragraph)
			points = append(points, point)
		}
		paragraphs = append(paragraphs, "KST 00:00부터 다음 날 00:00까지 1시간 경계 표본 25개가 모두 LIVE입니다.")
	} else {
		paragraphs = append(paragraphs, fmt.Sprintf(
			"마감 시점 값은 정확히 확보했지만, KST 하루 경로는 1시간 경계 표본 %d/%d개만 있어 최저·최고와 시간별 지속성은 확정하지 않았습니다.",
			day.ObservedSampleCount, day.ExpectedSampleCount,
		))
		points = append(points, fmt.Sprintf("하루 경로 %d/%d개 · 마감값만 확정", day.ObservedSampleCount, day.ExpectedSampleCount))
	}
	if strings.TrimSpace(query.Asset) != "" {
		paragraphs = append(paragraphs, query.Asset+" 자산 세부는 현재 날짜 마감 질의의 확정 근거에 포함되지 않아 전체 점유율만 답했습니다.")
		points = append(points, query.Asset+" 날짜 마감 자산 세부 미제공")
	}
	return marketanalysis.MarketQuestionAnswer{
		Answer: strings.Join(paragraphs, "\n\n"), KeyPoints: points,
		Caveat: "이 값은 빗썸 ticker의 09시→09시 누적값을 그대로 쓴 것이 아니라, 거래소별 공식 1시간봉 24개를 동일한 KST 자정→자정 구간으로 맞춰 합산한 캘린더 일일 점유율입니다. WebSocket 24시간 누적값은 coverage가 완전한 별도 직접관측 근거로만 사용합니다.",
		Source: "SERVER_BRIEFING_FALLBACK", AsOfMS: close.BucketMS,
	}
}
