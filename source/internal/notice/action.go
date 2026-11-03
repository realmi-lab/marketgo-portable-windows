package notice

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const maxNoticeActions = 20

var noticeActionSubjects = map[string]struct{}{
	"TRADING_SUPPORT":    {},
	"CAUTION":            {},
	"DEPOSIT_WITHDRAWAL": {},
	"EVENT":              {},
	"MAINTENANCE":        {},
	"SECURITY":           {},
	"NETWORK":            {},
	"ASSET":              {},
	"OTHER":              {},
}

var noticeActionVerbs = map[string]struct{}{
	"START":     {},
	"ADD":       {},
	"DESIGNATE": {},
	"URGE":      {},
	"SUSPEND":   {},
	"RESUME":    {},
	"EXTEND":    {},
	"RELEASE":   {},
	"COMPLETE":  {},
	"END":       {},
	"UPDATE":    {},
	"ANNOUNCE":  {},
}

func NormalizeActions(values []Action) ([]Action, error) {
	if len(values) > maxNoticeActions {
		return nil, errors.New("actions must contain at most 20 rows")
	}
	result := make([]Action, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Subject = strings.ToUpper(strings.TrimSpace(value.Subject))
		value.Verb = strings.ToUpper(strings.TrimSpace(value.Verb))
		value.Evidence = strings.TrimSpace(value.Evidence)
		if _, valid := noticeActionSubjects[value.Subject]; !valid {
			return nil, errors.New("unsupported action subject")
		}
		if _, valid := noticeActionVerbs[value.Verb]; !valid {
			return nil, errors.New("unsupported action verb")
		}
		if value.Evidence == "" || utf8.RuneCountInString(value.Evidence) > 2_000 {
			return nil, errors.New("action evidence is required and must be at most 2000 characters")
		}
		key := value.Subject + "\x00" + value.Verb + "\x00" + value.Evidence
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func HasAction(values []Action, subject string, verbs ...string) bool {
	subject = strings.ToUpper(strings.TrimSpace(subject))
	for _, value := range values {
		if strings.ToUpper(strings.TrimSpace(value.Subject)) != subject {
			continue
		}
		if len(verbs) == 0 {
			return true
		}
		candidate := strings.ToUpper(strings.TrimSpace(value.Verb))
		for _, verb := range verbs {
			if candidate == strings.ToUpper(strings.TrimSpace(verb)) {
				return true
			}
		}
	}
	return false
}

// EffectiveActions prefers grounded model facts, then the persisted
// deterministic title facts, and finally computes the title facts for records
// created before title_actions_json existed. The last branch is compatibility,
// not permission to add notice-ID or asset-specific exceptions.
func EffectiveActions(item Item) []Action {
	if len(item.Actions) > 0 {
		return item.Actions
	}
	if len(item.TitleActions) > 0 {
		return item.TitleActions
	}
	return InferTitleActions(item.Title, item.Category)
}

// InferTitleActions is the conservative deterministic layer for list-only or
// failed analyses. It maps a finite notice lifecycle into the same action
// contract as the grounded model and uses the complete official title as
// evidence. Persistence stores it in title_actions_json, never actions_json,
// so model-grounded facts and title-derived facts retain distinct provenance.
// New notices should be handled by these lifecycle semantics, not by notice IDs
// or one-off title exceptions.
func InferTitleActions(title, suppliedCategory string) []Action {
	title = strings.TrimSpace(title)
	if title == "" {
		return []Action{}
	}
	text := strings.ToLower(title)
	category := strings.ToUpper(strings.TrimSpace(suppliedCategory))
	if category == "" {
		category = Classify(title, "")
	}
	result := make([]Action, 0, 4)
	appendAction := func(subject, verb string) {
		if HasAction(result, subject, verb) {
			return
		}
		result = append(result, Action{Subject: subject, Verb: verb, Evidence: title})
	}

	event := category == "EVENT" || category == "AIRDROP" || titleContainsAny(text,
		"이벤트", "에어드랍", "프로모션", "캠페인", "챌린지", "페스타", "상금", "리워드",
		"event", "airdrop", "promotion", "campaign", "competition", "reward",
	)
	if event {
		switch {
		case titleContainsAny(text, "연장", "기간 변경", "extend", "extension"):
			appendAction("EVENT", "EXTEND")
		case titleContainsAny(text, "종료", "마감", "결과 안내", "당첨자", "ended", "closed", "winner"):
			appendAction("EVENT", "COMPLETE")
		default:
			appendAction("EVENT", "START")
		}
	}

	delisting := category == "DELISTING" || category == "TRADING_SUPPORT_TERMINATION" ||
		titleContainsAny(text, "거래지원 종료", "거래 지원 종료", "거래지원종료", "상장폐지", "delist", "trading termination")
	listing := category == "LISTING" || category == "MARKET_ADDITION" ||
		titleContainsAny(text, "신규 거래지원", "거래지원 개시", "원화 마켓 추가", "원화마켓 추가", "마켓 디지털 자산 추가", "디지털 자산 추가", "new listing")
	switch {
	case delisting:
		appendAction("TRADING_SUPPORT", "END")
	case listing && !event:
		appendAction("TRADING_SUPPORT", "ADD")
	}
	// A temporary trading halt is a trading-support lifecycle even when the
	// source categorizes the notice as a rebrand or token operation. Keep this
	// separate from delisting and from deposit/withdrawal suspension: one notice
	// may legitimately carry both trading and transfer actions.
	tradingSuspension := !delisting && titleContainsAny(text,
		"거래 일시 중단", "거래 일시 중지", "거래지원 일시 중단", "거래 지원 일시 중단",
		"temporary trading suspension", "trading temporarily suspended",
	)
	if tradingSuspension {
		appendAction("TRADING_SUPPORT", "SUSPEND")
		if titleContainsAny(text, "(완료)", "중단 완료", "중지 완료", "completed") {
			appendAction("TRADING_SUPPORT", "COMPLETE")
		}
	}

	caution := category == "CAUTION" || titleContainsAny(text,
		"유의종목", "유의 종목", "유의촉구", "유의 촉구", "거래유의", "거래 유의", "투자유의", "투자 유의", "market warning", "caution",
	)
	if caution && !delisting {
		switch {
		case titleContainsAny(text, "해제", "解除", "release", "removed"):
			appendAction("CAUTION", "RELEASE")
		case titleContainsAny(text, "연장", "extend", "extension"):
			appendAction("CAUTION", "EXTEND")
		case titleContainsAny(text, "유의촉구", "유의 촉구", "urge"):
			appendAction("CAUTION", "URGE")
		default:
			appendAction("CAUTION", "DESIGNATE")
		}
	}

	depositCategory := strings.HasPrefix(category, "DEPOSIT_WITHDRAWAL")
	depositSubject := depositCategory || titleContainsAny(text, "입출금", "입금", "출금", "deposit", "withdrawal")
	if depositSubject {
		if titleContainsAny(text, "중단", "중지", "차단", "suspend", "suspended", "halted", "blocked") ||
			strings.HasSuffix(category, "_SUSPENSION") {
			appendAction("DEPOSIT_WITHDRAWAL", "SUSPEND")
		}
		if titleContainsAny(text, "재개", "resumed", "resume") {
			appendAction("DEPOSIT_WITHDRAWAL", "RESUME")
		} else if titleContainsAny(text, "(완료)", "재개완료", "재개 완료") || strings.HasSuffix(category, "_RESUMPTION") {
			appendAction("DEPOSIT_WITHDRAWAL", "COMPLETE")
		}
	}

	if category == "SECURITY" || category == "SECURITY_INCIDENT" ||
		titleContainsAny(text, "보안 사고", "보안 이슈", "해킹", "피싱", "스미싱", "security incident", "security issue", "exploit", "phishing") {
		appendAction("SECURITY", "ANNOUNCE")
	}
	if category == "MAINTENANCE" || titleContainsAny(text, "점검", "maintenance") {
		appendAction("MAINTENANCE", "ANNOUNCE")
	}
	if category == "NETWORK" || category == "NETWORK_UPGRADE" ||
		titleContainsAny(text, "네트워크 업그레이드", "네트워크 장애", "network upgrade", "network outage") {
		appendAction("NETWORK", "UPDATE")
	}
	return result
}

func titleContainsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}
