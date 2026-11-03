package noticewriter

import (
	"sort"
	"strings"
)

const (
	ReviewMarker = "[공식 게시 전 검토용 초안]"
	ReviewNote   = "※ 이 초안의 자산, 일정, 사유, 정책 및 후속 조치는 공식 게시 전에 반드시 확인해 주세요."
)

type Envelope struct {
	Salutation string
	BrandLine  string
	Closing    string
}

type RenderInput struct {
	Action   Action
	Assets   []string
	Envelope Envelope
}

func Render(input RenderInput) string {
	schema, ok := contracts[input.Action]
	if !ok {
		return renderUnknown(input.Envelope)
	}
	frame := exchangeFrames[schema.Exchange]
	envelope := normalizedEnvelope(input.Envelope, frame, schema.ClosingKind)
	assets := normalizedAssets(input.Assets)
	assetTitle, assetValue := "[대상 가상자산 확인 필요]", "[확인 필요]"
	if len(assets) > 0 {
		assetTitle, assetValue = strings.Join(assets, ", "), strings.Join(assets, ", ")
	}

	var builder strings.Builder
	builder.Grow(1800)
	writeOpening(&builder, envelope, frame)
	builder.WriteString(assetTitle)
	builder.WriteByte(' ')
	builder.WriteString(schema.TitleSuffix)
	builder.WriteString("\n\n")
	builder.WriteString(schema.Lead)
	builder.WriteString("\n\n■ ")
	builder.WriteString(schema.SectionTitle)
	builder.WriteString("\n- 대상 가상자산: ")
	builder.WriteString(assetValue)
	builder.WriteByte('\n')
	for _, field := range schema.FactFields {
		builder.WriteString("- ")
		builder.WriteString(field.Label)
		builder.WriteString(": [확인 필요]\n")
	}
	builder.WriteString("\n■ 유의사항\n")
	for index, field := range schema.CautionFields {
		builder.WriteString("- ")
		builder.WriteString(field.Label)
		builder.WriteString(": [확인 필요]")
		if index+1 < len(schema.CautionFields) {
			builder.WriteByte('\n')
		}
	}
	writeClosing(&builder, envelope, frame)
	return builder.String()
}

func renderUnknown(value Envelope) string {
	frame := exchangeFrames["bithumb"]
	envelope := normalizedEnvelope(value, frame, "OPERATIONS")
	var builder strings.Builder
	builder.Grow(900)
	writeOpening(&builder, envelope, frame)
	builder.WriteString("[공지 제목 확인 필요]\n\n■ 적용 내용\n")
	for _, label := range []string{"대상 가상자산", "적용 일시", "적용 사유", "후속 조치"} {
		builder.WriteString("- ")
		builder.WriteString(label)
		builder.WriteString(": [확인 필요]\n")
	}
	builder.WriteString("\n■ 유의사항\n- 이용자 확인 사항: [확인 필요]\n- 후속 안내 방식: [확인 필요]")
	writeClosing(&builder, envelope, frame)
	return builder.String()
}

func writeOpening(builder *strings.Builder, envelope Envelope, frame ExchangeFrame) {
	builder.WriteString(ReviewMarker)
	builder.WriteString("\n\n")
	separator := frame.OpeningSeparator
	if separator == "" {
		separator = "\n"
	}
	builder.WriteString(envelope.Salutation)
	builder.WriteString(separator)
	builder.WriteString(envelope.BrandLine)
	builder.WriteString("\n\n")
}

func writeClosing(builder *strings.Builder, envelope Envelope, frame ExchangeFrame) {
	builder.WriteString("\n\n")
	builder.WriteString(envelope.Closing)
	finalLines := frame.DefaultFinalLines
	if len(finalLines) == 0 {
		finalLines = []string{"감사합니다."}
	}
	finalText := finalLines[0]
	if len(finalLines) > 1 {
		finalText = strings.Join(finalLines, "\n")
	}
	builder.WriteString("\n\n")
	builder.WriteString(finalText)
	builder.WriteString("\n\n")
	builder.WriteString(ReviewNote)
}

func normalizedEnvelope(value Envelope, frame ExchangeFrame, closingKind string) Envelope {
	if strings.TrimSpace(value.Salutation) == "" {
		value.Salutation = firstOrDefault(frame.Salutations, "안녕하세요.")
	}
	if strings.TrimSpace(value.BrandLine) == "" {
		value.BrandLine = frame.DefaultBrandLine
	}
	if strings.TrimSpace(value.Closing) == "" {
		value.Closing = CanonicalClosingFor(frame.Exchange, closingKind)
	}
	return value
}

func CanonicalClosing(kind string) string {
	return CanonicalClosingFor("bithumb", kind)
}

func CanonicalClosingFor(exchange, kind string) string {
	frame, ok := exchangeFrames[strings.ToLower(strings.TrimSpace(exchange))]
	if !ok {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(kind), "RISK") && frame.RiskClosing != "" {
		return frame.RiskClosing
	}
	return frame.DefaultClosing
}

func firstOrDefault(values []string, fallback string) string {
	if len(values) > 0 && strings.TrimSpace(values[0]) != "" {
		return values[0]
	}
	return fallback
}

func normalizedAssets(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	if len(values) == 1 {
		value := strings.ToUpper(strings.TrimSpace(values[0]))
		if value == "" {
			return nil
		}
		return []string{value}
	}
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
