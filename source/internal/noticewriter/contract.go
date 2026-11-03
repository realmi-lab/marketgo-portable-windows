// Package noticewriter owns the declarative contract used to compose and
// validate exchange-style notice drafts. It deliberately contains no crawler,
// application, or model orchestration behavior.
package noticewriter

import "strings"

const ContractVersion = "DOMESTIC_NOTICE_DRAFT_V1"

type Action string

const (
	ActionCautionDesignation Action = "CAUTION_DESIGNATION"
	ActionCautionExtension   Action = "CAUTION_EXTENSION"
	ActionCautionRelease     Action = "CAUTION_RELEASE"
	ActionCautionUrge        Action = "CAUTION_URGE"
	ActionDelisting          Action = "DELISTING"
	ActionDepositStop        Action = "DEPOSIT_STOP"
	ActionDepositResume      Action = "DEPOSIT_RESUME"
)

type Field struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// DraftContract is both the renderer input contract and the compact schema
// sent to the model. Adding a notice family changes one registry entry instead
// of separate prompt, renderer, and validator switches.
type DraftContract struct {
	Version       string   `json:"version"`
	Exchange      string   `json:"exchange"`
	Action        Action   `json:"action"`
	ActionLabel   string   `json:"action_label"`
	TitleSuffix   string   `json:"title_suffix"`
	Lead          string   `json:"lead"`
	SectionTitle  string   `json:"section_title"`
	FactFields    []Field  `json:"fact_fields"`
	CautionFields []Field  `json:"caution_fields"`
	ClosingKind   string   `json:"closing_kind"`
	DocumentOrder []string `json:"document_order"`
}

var contracts = map[Action]DraftContract{
	ActionCautionDesignation: contract(ActionCautionDesignation, "거래유의종목 지정", "거래유의종목 지정 안내",
		"빗썸은 [적용 정책·절차 확인 필요]에 따라 아래 가상자산을 거래유의종목으로 지정하고자 합니다.",
		[]string{"지정 사유", "지정일", "연장·해제 검토 일정", "거래지원 종료 검토 여부·일정"},
		[]string{"일정 변경 가능 여부", "투자자 확인 사항", "후속 안내 방식"}, "RISK"),
	ActionCautionExtension: contract(ActionCautionExtension, "거래유의종목 지정 연장", "거래유의종목 지정 연장 안내",
		"빗썸은 [적용 정책·절차 확인 필요]에 따라 아래 가상자산의 거래유의종목 지정 기간을 연장하고자 합니다.",
		[]string{"연장 사유", "연장 적용일", "연장 기간·후속 검토 일정", "거래지원 종료 검토 여부·일정"},
		[]string{"일정 변경 가능 여부", "투자자 확인 사항", "후속 안내 방식"}, "RISK"),
	ActionCautionRelease: contract(ActionCautionRelease, "거래유의종목 지정 해제", "거래유의종목 지정 해제 안내",
		"빗썸은 [적용 정책·절차 확인 필요]에 따라 아래 가상자산의 거래유의종목 지정을 해제하고자 합니다.",
		[]string{"해제 사유", "해제 적용일", "후속 조치"},
		[]string{"이용 가능 서비스 영향", "투자자 확인 사항", "후속 안내 방식"}, "RISK"),
	ActionCautionUrge: contract(ActionCautionUrge, "투자유의 촉구", "투자유의 촉구 안내",
		"빗썸은 아래 가상자산과 관련하여 이용자 여러분께 투자유의를 촉구드립니다.",
		[]string{"유의 촉구 사유", "확인 기준일", "후속 조치·안내 일정"},
		[]string{"이용자 확인 사항", "거래·입출금 영향", "후속 안내 방식"}, "RISK"),
	ActionDelisting: contract(ActionDelisting, "거래지원 종료", "거래지원 종료 안내",
		"빗썸은 [적용 정책·절차 확인 필요]에 따라 아래 가상자산의 거래지원을 종료하고자 합니다.",
		[]string{"대상 마켓", "종료 사유", "거래지원 종료 일시", "출금 지원 종료 일시"},
		[]string{"미체결 주문 처리", "출금 지원 범위·절차", "일정 변경 및 후속 안내 방식"}, "RISK"),
	ActionDepositStop: contract(ActionDepositStop, "입출금 중단", "입출금 일시 중단 안내",
		"[중단 사유 확인 필요]로 인해 아래 가상자산의 입출금을 일시 중단할 예정입니다.",
		[]string{"대상 네트워크", "중단 시점", "중단 사유", "재개 조건·시점", "재개 안내 방식"},
		[]string{"중단 전 신청 건 처리", "입금 반영 지연 여부", "중단 중 거래 가능 여부", "재개 후 처리 순서"}, "OPERATIONS"),
	ActionDepositResume: contract(ActionDepositResume, "입출금 재개", "입출금 재개 안내",
		"[재개 조건 충족 여부 확인 필요]에 따라 아래 가상자산의 입출금을 재개할 예정입니다.",
		[]string{"대상 네트워크", "재개 시점", "재개 조건·사유", "입금 반영 시작 기준"},
		[]string{"중단 중 신청 건 처리", "입금 반영 순서·지연 여부", "이용자 확인 사항", "후속 안내 방식"}, "OPERATIONS"),
}

func contract(action Action, label, title, lead string, facts, cautions []string, closingKind string) DraftContract {
	return DraftContract{
		Version: ContractVersion, Exchange: "bithumb", Action: action, ActionLabel: label,
		TitleSuffix: title, Lead: lead, SectionTitle: label,
		FactFields: fields(facts), CautionFields: fields(cautions), ClosingKind: closingKind,
		DocumentOrder: []string{"review_marker", "salutation", "brand_line", "title", "lead", "facts", "cautions", "closing", "thanks", "review_note"},
	}
}

func fields(labels []string) []Field {
	result := make([]Field, 0, len(labels))
	for _, label := range labels {
		result = append(result, Field{Key: fieldKey(label), Label: label})
	}
	return result
}

func fieldKey(label string) string {
	replacer := strings.NewReplacer("·", "_", " ", "_", "·", "_", "-", "_")
	return strings.ToUpper(replacer.Replace(label))
}

func ContractFor(action Action) (DraftContract, bool) {
	value, ok := contracts[action]
	if !ok {
		return DraftContract{}, false
	}
	value.FactFields = append([]Field(nil), value.FactFields...)
	value.CautionFields = append([]Field(nil), value.CautionFields...)
	value.DocumentOrder = append([]string(nil), value.DocumentOrder...)
	return value, true
}

func RequiredPlaceholderFields(action Action) []string {
	value, ok := contracts[action]
	if !ok {
		return nil
	}
	result := make([]string, 0, len(value.FactFields))
	for _, field := range value.FactFields {
		// These two rows are deterministic renderer metadata, not facts that a
		// model may fill from prose.
		if field.Label == "재개 안내 방식" {
			continue
		}
		result = append(result, field.Label+": [확인 필요]")
	}
	return result
}

func InferAction(value string) Action {
	text := strings.ToLower(strings.TrimSpace(value))
	switch {
	case containsAny(text, "거래지원 종료", "거래지원종료", "상장폐지", "마켓 제거"):
		return ActionDelisting
	case containsAny(text, "입출금", "입금", "출금") && containsAny(text, "재개", "재개완료", "중단 완료", "중지 완료", "(완료)"):
		return ActionDepositResume
	case containsAny(text, "입출금", "입금", "출금") && containsAny(text, "중단", "중지", "차단"):
		return ActionDepositStop
	case containsAny(text, "유의촉구", "유의 촉구"):
		return ActionCautionUrge
	case containsAny(text, "유의종목", "유의 종목", "거래유의", "투자유의") && strings.Contains(text, "해제"):
		return ActionCautionRelease
	case containsAny(text, "유의종목", "유의 종목", "거래유의", "투자유의") && strings.Contains(text, "연장"):
		return ActionCautionExtension
	case containsAny(text, "유의종목", "유의 종목", "거래유의", "투자유의"):
		return ActionCautionDesignation
	default:
		return ""
	}
}

func ActionLabel(action Action) string {
	if value, ok := contracts[action]; ok {
		return value.ActionLabel
	}
	return "조치 확인 필요"
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}
