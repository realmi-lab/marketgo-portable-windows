package app

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
)

var rollingBriefingPriority = []int64{1200, 600, 1800, 300, 60, 3600}

type rollingBriefingSelection struct {
	horizon *marketanalysis.RollingHorizon
	bridge  *marketanalysis.RollingBridge
	exact   bool
}

func buildRollingBriefing(monitor marketanalysis.RollingMonitor, notices []marketanalysis.RollingNoticeEvidence) marketanalysis.RollingBriefing {
	currentShare := monitor.Rolling24h.CurrentSharePct
	if currentShare == nil {
		return marketanalysis.RollingBriefing{Status: "UNAVAILABLE", Text: "24시간 점유율과 변화 원인을 계산할 자료가 아직 없습니다."}
	}
	status := monitor.Rolling24h.Status
	if status != "CONFIRMED" && status != "PROVISIONAL" && status != "RESTORED" && status != "ESTIMATED" {
		text := fmt.Sprintf(
			"최근 %s 중 관측한 시간은 %s, 누락된 시간은 %s입니다. 관측한 거래의 빗썸 점유율은 %s입니다. 일부만 관측한 값이므로 완전한 24시간 점유율로 보지 않으며, 변화 시작 시각과 새로 들어오거나 24시간 범위에서 빠진 거래의 정확한 영향도 계산하지 않습니다.",
			rollingBriefDuration(monitor.Rolling24h.Coverage.RequiredSec), rollingBriefDuration(monitor.Rolling24h.Coverage.CoverageSec), rollingBriefDuration(monitor.Rolling24h.Coverage.GapSeconds), rollingBriefPercent(currentShare),
		)
		return marketanalysis.RollingBriefing{Status: "DATA_INCOMPLETE", Text: text}
	}
	selection := selectRollingBriefingData(&monitor)
	horizon, bridge := selection.horizon, selection.bridge
	movementStatus, _ := monitor.DataQuality["movement_status"].(string)
	incomplete := movementStatus == "BLOCKED_BY_DATA_GAP" || !selection.exact
	parts := make([]string, 0, 7)
	if incomplete {
		if movementStatus == "BLOCKED_BY_DATA_GAP" {
			parts = append(parts, fmt.Sprintf("현재 24시간 빗썸 점유율은 %s입니다. 10·20·30분 비교값이 두 구간 이상 쌓이기 전에는 변화가 언제 시작해 얼마나 이어졌는지, 가장 크게 움직인 폭과 전체 변화 원인을 확정하지 않습니다.", rollingBriefPercent(currentShare)))
		} else {
			parts = append(parts, fmt.Sprintf("현재 24시간 빗썸 점유율은 %s입니다. 새로 들어오거나 24시간 범위에서 빠진 거래의 기여를 비교할 자료가 아직 없어 변화 원인을 확정하지 않습니다.", rollingBriefPercent(currentShare)))
		}
	} else if (monitor.Movement.Direction == "UP" || monitor.Movement.Direction == "DOWN") && monitor.Movement.TotalChangePP != nil {
		direction := "하락"
		if *monitor.Movement.TotalChangePP > 0 {
			direction = "상승"
		}
		// The verb carries the direction, so the size is written without a sign.
		parts = append(parts, fmt.Sprintf("24시간 빗썸 점유율은 최근 %s 동안 %s에서 %s로 %s %s했습니다.", rollingBriefDuration(monitor.Movement.DurationSec), rollingBriefPercent(monitor.Movement.ShareAtStart), rollingBriefPercent(currentShare), rollingBriefMagnitude(*monitor.Movement.TotalChangePP), direction))
	} else {
		parts = append(parts, fmt.Sprintf("현재 24시간 빗썸 점유율은 %s이며, 여러 비교 구간에서 일관된 상승·하락 흐름은 아직 확정되지 않았습니다.", rollingBriefPercent(currentShare)))
	}
	if horizon != nil && horizon.RecentFlowSharePct != nil {
		gap := *horizon.RecentFlowSharePct - *currentShare
		comparison := "현재 24시간 점유율과 같았습니다"
		if gap > 0 {
			comparison = "현재 24시간 점유율보다 " + rollingBriefMagnitude(gap) + " 높았습니다"
		} else if gap < 0 {
			comparison = "현재 24시간 점유율보다 " + rollingBriefMagnitude(gap) + " 낮았습니다"
		}
		sentence := fmt.Sprintf("최근 %s 동안 새로 들어온 거래의 빗썸 비중은 %s로 %s.", horizon.Window, rollingBriefPercent(horizon.RecentFlowSharePct), comparison)
		if horizon.ExpiringFlowSharePct != nil {
			sentence += fmt.Sprintf(" 24시간 범위에서 빠진 같은 길이 구간의 빗썸 비중은 %s였습니다.", rollingBriefPercent(horizon.ExpiringFlowSharePct))
		}
		parts = append(parts, sentence)
	}
	if !incomplete && horizon != nil && horizon.IncomingEffectPP != nil {
		parts = append(parts, fmt.Sprintf("최근 %s 동안의 점유율 변화를 나누면 새로 들어온 거래 %s, 24시간 범위에서 빠진 거래 %s, 합계 %s입니다.", horizon.Window, rollingBriefPoints(horizon.IncomingEffectPP), rollingBriefPoints(horizon.ExpiringEffectPP), rollingBriefPoints(horizon.TotalEffectPP)))
	}
	var assetDriver, exchangeDriver *marketanalysis.RollingDriver
	if bridge != nil {
		assetDriver = firstRollingAssetDriver(bridge.AssetDrivers)
		exchangeDriver = selectRollingExchangeDriver(bridge.ExchangeDrivers)
	}
	if assetDriver != nil {
		prefix := "점유율 변화에 가장 크게 기여한 코인은 "
		if incomplete {
			prefix = firstNonEmptyText(bridge.Window, "선택 구간") + " 기준 계산 가능한 범위에서 가장 크게 기여한 코인은 "
		}
		text := prefix + firstNonEmptyText(assetDriver.Base, "자료 없음") + "(" + rollingBriefPoints(assetDriver.ContributionPP) + ")"
		if exchangeDriver != nil {
			change := exchangeDriver.RecentCompetitorVolumeKRW - exchangeDriver.ExpiringCompetitorVolumeKRW
			phrase := "경쟁 거래소 중 최근 거래금액이 가장 큰 곳은"
			if change > 0 {
				phrase = "경쟁 거래소 중 거래금액이 가장 크게 늘어난 곳은"
			}
			text += fmt.Sprintf("이며, %s %s(%s)입니다. 이 거래소가 빗썸 점유율에 준 영향은 %s입니다.", phrase, rollingExchangeLabel(exchangeDriver.Exchange), rollingBriefMoney(change), rollingBriefPoints(exchangeDriver.ContributionPP))
		} else {
			text += "입니다."
		}
		parts = append(parts, text)
	}
	if monitor.Movement.StartedAtMS != nil && *monitor.Movement.StartedAtMS > 0 {
		if relevant := relevantRollingNotice(notices); relevant != nil {
			if relevant.ContentRelated {
				timing := firstNonEmptyText(relevant.TimingLabel, "시간 차이 미확인")
				if strings.HasSuffix(timing, "정보 없음") {
					// "…이며, 공지 시각 정보 없음입니다" does not read; say it as a sentence.
					parts = append(parts, fmt.Sprintf("본문에서 확인된 코인과 연결된 국내 공지는 %s의 ‘%s’이며, 공지와 변화의 시간 차이는 확인되지 않았습니다. 인과관계가 아닌 시간상 연관입니다.", rollingExchangeLabel(relevant.Exchange), firstNonEmptyText(relevant.Title, "제목 없음")))
				} else {
					parts = append(parts, fmt.Sprintf("본문에서 확인된 코인과 연결된 국내 공지는 %s의 ‘%s’이며, %s입니다. 인과관계가 아닌 시간상 연관입니다.", rollingExchangeLabel(relevant.Exchange), firstNonEmptyText(relevant.Title, "제목 없음"), timing))
				}
			} else {
				parts = append(parts, fmt.Sprintf("국내 공지 중 %s의 ‘%s’ 공지가 변화 시간대와 겹치지만, 시간상 연관일 뿐 공지가 원인이라고 단정하지 않습니다.", rollingExchangeLabel(relevant.Exchange), firstNonEmptyText(relevant.Title, "제목 없음")))
			}
		} else if len(notices) > 0 {
			parts = append(parts, "공지 목록은 확인했지만 본문 내용·기여 코인·변화 시간대가 함께 겹치는 공지는 확인되지 않았습니다.")
		}
	} else if len(notices) > 0 {
		parts = append(parts, "공지 목록은 확인했지만 본문 내용·기여 코인·변화 시간대가 함께 겹치는 공지는 확인되지 않았습니다.")
	}
	briefingStatus := "MEASURED"
	if incomplete {
		briefingStatus = "DATA_INCOMPLETE"
	}
	var window *string
	if horizon != nil {
		value := horizon.Window
		window = &value
	}
	return marketanalysis.RollingBriefing{Status: briefingStatus, Window: window, Text: strings.Join(parts, " ")}
}

func selectRollingBriefingData(monitor *marketanalysis.RollingMonitor) rollingBriefingSelection {
	type windowData struct {
		horizon *marketanalysis.RollingHorizon
		bridge  *marketanalysis.RollingBridge
	}
	windows := make(map[int64]windowData, len(monitor.Horizons)+len(monitor.Bridges))
	for index := range monitor.Horizons {
		duration := monitor.Horizons[index].DurationSec
		window := windows[duration]
		window.horizon = &monitor.Horizons[index]
		windows[duration] = window
	}
	for index := range monitor.Bridges {
		duration := monitor.Bridges[index].DurationSec
		window := windows[duration]
		window.bridge = &monitor.Bridges[index]
		windows[duration] = window
	}

	var exact, observed rollingBriefingSelection
	for _, duration := range rollingBriefingPriority {
		window := windows[duration]
		if window.horizon == nil {
			continue
		}
		candidate := rollingBriefingSelection{horizon: window.horizon, bridge: window.bridge}
		if exact.horizon == nil && window.horizon.IncomingEffectPP != nil && window.horizon.ExpiringEffectPP != nil {
			candidate.exact = true
			exact = candidate
		}
		if observed.horizon == nil && (window.horizon.RecentFlowSharePct != nil || window.horizon.ExpiringFlowSharePct != nil) {
			observed = candidate
		}
	}

	selected := exact
	if selected.horizon == nil {
		selected = observed
	}
	if selected.horizon == nil {
		if rollingBridgeHasDrivers(&monitor.Bridge) {
			selected.bridge = &monitor.Bridge
		}
		return selected
	}
	if selected.bridge == nil && legacyRollingBridgeMatches(&monitor.Bridge, selected.horizon.DurationSec) {
		selected.bridge = &monitor.Bridge
	}
	return selected
}

func legacyRollingBridgeMatches(bridge *marketanalysis.RollingBridge, duration int64) bool {
	if !rollingBridgeHasDrivers(bridge) {
		return false
	}
	if bridge.DurationSec == duration {
		return true
	}
	return bridge.DurationSec == 0 && duration == 600
}

func rollingBridgeHasDrivers(bridge *marketanalysis.RollingBridge) bool {
	return bridge != nil && (len(bridge.AssetDrivers) > 0 || len(bridge.ExchangeDrivers) > 0)
}

func firstRollingAssetDriver(values []marketanalysis.RollingDriver) *marketanalysis.RollingDriver {
	if len(values) == 0 {
		return nil
	}
	return &values[0]
}

func selectRollingExchangeDriver(values []marketanalysis.RollingDriver) *marketanalysis.RollingDriver {
	candidates := make([]marketanalysis.RollingDriver, 0, len(values))
	for _, value := range values {
		if value.Exchange != "bithumb" {
			candidates = append(candidates, value)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	growing := make([]marketanalysis.RollingDriver, 0, len(candidates))
	for _, value := range candidates {
		if value.RecentCompetitorVolumeKRW-value.ExpiringCompetitorVolumeKRW > 0 {
			growing = append(growing, value)
		}
	}
	selected := candidates
	if len(growing) > 0 {
		selected = growing
		sort.SliceStable(selected, func(i, j int) bool {
			left := selected[i].RecentCompetitorVolumeKRW - selected[i].ExpiringCompetitorVolumeKRW
			right := selected[j].RecentCompetitorVolumeKRW - selected[j].ExpiringCompetitorVolumeKRW
			if left == right {
				return selected[i].RecentCompetitorVolumeKRW > selected[j].RecentCompetitorVolumeKRW
			}
			return left > right
		})
	} else {
		sort.SliceStable(selected, func(i, j int) bool {
			if selected[i].RecentCompetitorVolumeKRW == selected[j].RecentCompetitorVolumeKRW {
				left := selected[i].RecentCompetitorVolumeKRW - selected[i].ExpiringCompetitorVolumeKRW
				right := selected[j].RecentCompetitorVolumeKRW - selected[j].ExpiringCompetitorVolumeKRW
				return left > right
			}
			return selected[i].RecentCompetitorVolumeKRW > selected[j].RecentCompetitorVolumeKRW
		})
	}
	return &selected[0]
}

func relevantRollingNotice(values []marketanalysis.RollingNoticeEvidence) *marketanalysis.RollingNoticeEvidence {
	candidates := make([]marketanalysis.RollingNoticeEvidence, 0)
	for _, value := range values {
		if value.Related && (value.TimeRelated || value.ScheduleRelated || value.ContentScheduleRelated) {
			candidates = append(candidates, value)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].ContentPriorityScore != candidates[j].ContentPriorityScore {
			return candidates[i].ContentPriorityScore > candidates[j].ContentPriorityScore
		}
		if candidates[i].Brief.DetailAvailable != candidates[j].Brief.DetailAvailable {
			return candidates[i].Brief.DetailAvailable
		}
		left := max(candidates[i].PublishedTSMS, candidates[i].UpdatedTSMS)
		right := max(candidates[j].PublishedTSMS, candidates[j].UpdatedTSMS)
		return left > right
	})
	return &candidates[0]
}

// rollingBriefDecimals follows the dashboard rule: two decimals, and three
// only when two would print a non-zero value as 0.00.
func rollingBriefDecimals(value float64) int {
	if value != 0 && math.Round(math.Abs(value)*100) == 0 {
		return 3
	}
	return 2
}

func rollingBriefPercent(value *float64) string {
	if value == nil {
		return "자료 없음"
	}
	return fmt.Sprintf("%.*f%%", rollingBriefDecimals(*value), *value)
}

func rollingBriefPoints(value *float64) string {
	if value == nil {
		return "자료 없음"
	}
	if *value == 0 {
		return "0.00%p"
	}
	decimals := rollingBriefDecimals(*value)
	if math.Round(math.Abs(*value)*math.Pow10(decimals)) == 0 {
		if *value > 0 {
			return "0.001%p 미만 상승"
		}
		return "0.001%p 미만 하락"
	}
	return fmt.Sprintf("%+.*f%%p", decimals, *value)
}

// rollingBriefMagnitude is a size written before a word that already says
// the direction (상승, 높았습니다), so it has no sign.
func rollingBriefMagnitude(value float64) string {
	magnitude := math.Abs(value)
	decimals := rollingBriefDecimals(magnitude)
	if magnitude != 0 && math.Round(magnitude*math.Pow10(decimals)) == 0 {
		return "0.001%p 미만"
	}
	return fmt.Sprintf("%.*f%%p", decimals, magnitude)
}

func rollingBriefMoney(value float64) string {
	rounded := int64(math.Round(math.Abs(value)))
	digits := strconv.FormatInt(rounded, 10)
	for index := len(digits) - 3; index > 0; index -= 3 {
		digits = digits[:index] + "," + digits[index:]
	}
	sign := "+"
	if value < 0 {
		sign = "-"
	}
	return sign + digits + "원"
}

// rollingBriefDuration writes every non-zero unit, so an observed and a
// missing duration add up to the window ("23시간 52분 6초" + "7분 54초").
func rollingBriefDuration(seconds int64) string {
	seconds = max(int64(0), seconds)
	parts := make([]string, 0, 3)
	if hours := seconds / 3600; hours > 0 {
		parts = append(parts, fmt.Sprintf("%d시간", hours))
	}
	if minutes := seconds % 3600 / 60; minutes > 0 {
		parts = append(parts, fmt.Sprintf("%d분", minutes))
	}
	if rest := seconds % 60; rest > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d초", rest))
	}
	return strings.Join(parts, " ")
}

func rollingExchangeLabel(exchange string) string {
	switch strings.ToLower(exchange) {
	case "upbit":
		return "업비트"
	case "bithumb":
		return "빗썸"
	case "coinone":
		return "코인원"
	case "korbit":
		return "코빗"
	case "gopax":
		return "고팍스"
	case "binance":
		return "바이낸스"
	default:
		return firstNonEmptyText(exchange, "거래소 정보 없음")
	}
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
