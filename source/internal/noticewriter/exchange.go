package noticewriter

import "strings"

// ExchangeFrame identifies stable document roles. It never stores a notice
// body or notice ID; the actual wording still comes from retrieved official
// examples. Layout fields make the fixed frame explicit for renderers and
// evaluators without asking a model to rediscover it on every request.
type ExchangeFrame struct {
	Exchange          string   `json:"exchange"`
	Salutations       []string `json:"salutations"`
	BrandEndings      []string `json:"brand_endings"`
	SectionPrefixes   []string `json:"section_prefixes"`
	CautionHeadings   []string `json:"caution_headings"`
	ClosingStarts     []string `json:"closing_starts"`
	ClosingSuffix     string   `json:"closing_suffix"`
	ThanksLines       []string `json:"thanks_lines"`
	SignoffLines      []string `json:"signoff_lines,omitempty"`
	OpeningSeparator  string   `json:"opening_separator"`
	TitlePosition     string   `json:"title_position"`
	DatePosition      string   `json:"date_position"`
	BodyPosition      string   `json:"body_position"`
	CautionPosition   string   `json:"caution_position"`
	ClosingPosition   string   `json:"closing_position"`
	DefaultBrandLine  string   `json:"-"`
	DefaultClosing    string   `json:"-"`
	RiskClosing       string   `json:"-"`
	DefaultFinalLines []string `json:"-"`
}

var exchangeFrames = map[string]ExchangeFrame{
	"upbit": exchangeFrame(
		"upbit", []string{"안녕하세요.", "안녕하세요,"}, []string{"업비트입니다."},
		[]string{"유의사항", "주의사항", "※"}, []string{"업비트는"},
		"가장 신뢰받는 디지털 자산 거래소 업비트입니다.", "", []string{"감사합니다."}, nil,
	),
	"bithumb": withRiskClosing(exchangeFrame(
		"bithumb", []string{"안녕하세요.", "안녕하세요,"}, []string{"빗썸입니다."},
		[]string{"유의사항", "주의사항", "※"}, []string{"빗썸은", "고객님의", "회원님의"},
		"당신의 안전한 금융 파트너, 빗썸입니다.",
		"빗썸은 고객님의 편리하고 안전한 거래를 위해 항상 노력하겠습니다.", []string{"감사합니다."}, nil,
	), "빗썸은 거래지원 중인 가상자산에 대한 지속적이고 체계적인 감독 및 외부 전문가, 재단과의 커뮤니케이션을 통해 투자자들이 안심하고 거래할 수 있도록 건전하고 투명한 거래환경 조성에 앞장서겠습니다."),
	"coinone": exchangeFrame(
		"coinone", []string{"안녕하세요.", "안녕하세요,"}, []string{"코인원입니다."},
		[]string{"유의사항", "주의사항", "꼭 알아두세요"}, []string{"항상 코인원을", "앞으로도 고객님께", "코인원은"},
		"믿고 쓰는 코인원입니다.", "항상 코인원을 이용해주셔서 감사드리며 더 나은 서비스로 보답하겠습니다.",
		[]string{"감사합니다."}, []string{"코인원 팀 드림"},
	),
	"korbit": exchangeFrame(
		"korbit", []string{"안녕하세요.", "안녕하세요,"}, []string{"코빗입니다."},
		[]string{"유의사항", "주의사항", "⚠️"}, []string{"언제나 투자자 보호를", "코빗은"},
		"대한민국 최초 가상자산 거래소 코빗입니다.",
		"언제나 투자자 보호를 위해 앞장서는 투명하고 안전한 거래소 코빗이 되겠습니다.", []string{"감사합니다."}, nil,
	),
	"gopax": exchangeFrame(
		"gopax", []string{"안녕하세요.", "안녕하세요,"}, []string{"고팍스입니다."},
		[]string{"유의사항", "주의사항", "※"}, []string{"고팍스는", "앞으로도 법령을"},
		"새로운 금융의 개척, 고팍스입니다.", "고팍스는 더욱 신뢰할 수 있는 거래 환경 제공을 위해 최선을 다하겠습니다.",
		[]string{"감사합니다."}, []string{"고팍스 드림"},
	),
}

func exchangeFrame(
	exchange string,
	salutations, brandEndings, cautionHeadings, closingStarts []string,
	defaultBrand, defaultClosing string,
	thanks, signoffs []string,
) ExchangeFrame {
	return ExchangeFrame{
		Exchange: exchange, Salutations: salutations, BrandEndings: brandEndings,
		SectionPrefixes: []string{"■", "※", "[", "- ", "ㆍ", "· ", "📌", "⚠️"},
		CautionHeadings: cautionHeadings, ClosingStarts: closingStarts, ClosingSuffix: "겠습니다.",
		ThanksLines: thanks, SignoffLines: signoffs, OpeningSeparator: "\n",
		TitlePosition: "AFTER_OPENING", DatePosition: "ACTION_FIELDS", BodyPosition: "AFTER_TITLE",
		CautionPosition: "AFTER_FACTS", ClosingPosition: "AFTER_CAUTION",
		DefaultBrandLine: defaultBrand, DefaultClosing: defaultClosing,
		DefaultFinalLines: append(append([]string(nil), thanks...), signoffs...),
	}
}

func withRiskClosing(frame ExchangeFrame, value string) ExchangeFrame {
	frame.RiskClosing = value
	return frame
}

func ExchangeFrameFor(exchange string) (ExchangeFrame, bool) {
	value, ok := exchangeFrames[strings.ToLower(strings.TrimSpace(exchange))]
	if !ok {
		return ExchangeFrame{}, false
	}
	value.Salutations = append([]string(nil), value.Salutations...)
	value.BrandEndings = append([]string(nil), value.BrandEndings...)
	value.SectionPrefixes = append([]string(nil), value.SectionPrefixes...)
	value.CautionHeadings = append([]string(nil), value.CautionHeadings...)
	value.ClosingStarts = append([]string(nil), value.ClosingStarts...)
	value.ThanksLines = append([]string(nil), value.ThanksLines...)
	value.SignoffLines = append([]string(nil), value.SignoffLines...)
	value.DefaultFinalLines = append([]string(nil), value.DefaultFinalLines...)
	return value, true
}

func ExtractStyleExcerpts(exchange, body string) (string, string) {
	frame, ok := ExchangeFrameFor(exchange)
	if !ok {
		return "", ""
	}
	lines := normalizedDocumentLines(body)
	if len(lines) == 0 {
		return "", ""
	}
	return extractOpening(frame, lines), extractClosing(frame, lines)
}

func IsSalutation(exchange, line string) bool {
	frame, ok := exchangeFrames[strings.ToLower(strings.TrimSpace(exchange))]
	return ok && startsWithAny(strings.TrimSpace(line), frame.Salutations)
}

func IsBrandLine(exchange, line string) bool {
	frame, ok := exchangeFrames[strings.ToLower(strings.TrimSpace(exchange))]
	return ok && containsAnyValue(strings.TrimSpace(line), frame.BrandEndings)
}

func IsClosingLine(exchange, line string) bool {
	frame, ok := exchangeFrames[strings.ToLower(strings.TrimSpace(exchange))]
	line = strings.TrimSpace(line)
	return ok && startsWithAny(line, frame.ClosingStarts) && strings.HasSuffix(line, frame.ClosingSuffix)
}

func extractOpening(frame ExchangeFrame, lines []string) string {
	for index, line := range lines {
		if !startsWithAny(line, frame.Salutations) {
			continue
		}
		selected := make([]string, 0, 4)
		for cursor := index; cursor < len(lines) && cursor < index+4; cursor++ {
			candidate := lines[cursor]
			if cursor > index && startsWithAny(candidate, frame.SectionPrefixes) {
				break
			}
			if endingIndex := firstEndingIndex(candidate, frame.BrandEndings); endingIndex >= 0 && cursor == index {
				selected = append(selected, strings.TrimSpace(candidate[:endingIndex]))
				break
			}
			selected = append(selected, candidate)
		}
		return truncateRunes(strings.Join(selected, "\n"), 520)
	}
	return ""
}

func extractClosing(frame ExchangeFrame, lines []string) string {
	for thanksIndex := len(lines) - 1; thanksIndex >= 0; thanksIndex-- {
		if !equalsAny(lines[thanksIndex], frame.ThanksLines) {
			continue
		}
		selected := []string{}
		for cursor := thanksIndex - 1; cursor >= 0 && cursor >= thanksIndex-6; cursor-- {
			candidate := strings.Join(lines[cursor:thanksIndex], " ")
			if startsWithAny(candidate, frame.ClosingStarts) && strings.HasSuffix(candidate, frame.ClosingSuffix) {
				selected = append(selected, candidate)
				break
			}
		}
		selected = append(selected, lines[thanksIndex])
		for cursor := thanksIndex + 1; cursor < len(lines) && cursor <= thanksIndex+2; cursor++ {
			if equalsAny(lines[cursor], frame.SignoffLines) {
				selected = append(selected, lines[cursor])
			}
		}
		return truncateRunes(strings.Join(selected, "\n"), 700)
	}
	return ""
}

func normalizedDocumentLines(body string) []string {
	result := []string{}
	for _, raw := range strings.FieldsFunc(body, func(character rune) bool {
		return character == '\n' || character == '\r'
	}) {
		line := strings.Join(strings.Fields(raw), " ")
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func firstEndingIndex(value string, endings []string) int {
	best := -1
	for _, ending := range endings {
		if index := strings.Index(value, ending); index >= 0 {
			end := index + len(ending)
			if best < 0 || end < best {
				best = end
			}
		}
	}
	return best
}

func startsWithAny(value string, options []string) bool {
	for _, option := range options {
		if strings.HasPrefix(value, option) {
			return true
		}
	}
	return false
}

func containsAnyValue(value string, options []string) bool {
	for _, option := range options {
		if strings.Contains(value, option) {
			return true
		}
	}
	return false
}

func equalsAny(value string, options []string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func truncateRunes(value string, maximum int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maximum {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:maximum]))
}
