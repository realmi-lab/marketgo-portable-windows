package notice

import (
	"regexp"
	"strings"
)

const marketNoticeOpenEndedMaxAgeMS = int64(86_400_000)

var marketNoticeTerminalTitle = regexp.MustCompile(`(?i)완료|재개|해제|종료|취소|복구|종결|거래지원\s*종료|resum(?:e|ed)?|reopen|complete(?:d)?|closed|ended|cancel(?:led)?`)

// IsMarketEvidenceExchange limits market-reaction notice evidence to the two
// domestic venues whose announcements are used by the product policy.
func IsMarketEvidenceExchange(exchange string) bool {
	switch strings.ToLower(strings.TrimSpace(exchange)) {
	case "upbit", "bithumb":
		return true
	default:
		return false
	}
}

// IsReactionLifecycleKind identifies announcements whose publication and
// lifecycle boundaries can themselves coincide with an immediate reaction.
func IsReactionLifecycleKind(kind string) bool {
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "LISTING_STATUS", "DEPOSIT_WITHDRAWAL", "CAUTION":
		return true
	default:
		return false
	}
}

func IsReactionLifecycleCategory(category string) bool {
	switch strings.ToUpper(strings.TrimSpace(category)) {
	case "LISTING", "DELISTING", "MARKET_ADDITION", "QUOTE_MARKET_CHANGE",
		"DEPOSIT", "WITHDRAWAL", "DEPOSIT_WITHDRAWAL", "CAUTION":
		return true
	default:
		return false
	}
}

func IsTerminalLifecycleTitle(title string) bool {
	return marketNoticeTerminalTitle.MatchString(title)
}

// ScheduleActiveDuring reports whether a notice schedule was in force at any
// point in the selected analysis interval. Open-ended one-shot schedules do
// not remain active forever, and completion/resumption notices are boundaries
// rather than active states.
func ScheduleActiveDuring(schedules []Schedule, category, title string, startMS, endMS int64) bool {
	if startMS <= 0 && endMS <= 0 {
		return false
	}
	if startMS <= 0 {
		startMS = endMS
	}
	if endMS <= 0 {
		endMS = startMS
	}
	if endMS < startMS {
		startMS, endMS = endMS, startMS
	}
	terminal := IsTerminalLifecycleTitle(title)
	oneShot := isMarketNoticeOneShot(category)
	for _, schedule := range schedules {
		start, end := schedule.StartTSMS, schedule.EndTSMS
		if start <= 0 && end <= 0 {
			continue
		}
		if terminal && end <= 0 {
			continue
		}
		if oneShot && start > 0 && end <= 0 && start < endMS-marketNoticeOpenEndedMaxAgeMS {
			continue
		}
		if (start <= 0 || start <= endMS) && (end <= 0 || end >= startMS) {
			return true
		}
	}
	return false
}

func isMarketNoticeOneShot(category string) bool {
	switch strings.ToUpper(strings.TrimSpace(category)) {
	case "EVENT", "AIRDROP", "SNAPSHOT", "LISTING", "DELISTING", "MARKET_ADDITION",
		"QUOTE_MARKET_CHANGE", "TOKEN_SWAP", "REBRAND":
		return true
	default:
		return false
	}
}
