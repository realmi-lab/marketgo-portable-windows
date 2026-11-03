package hermesrunner

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// These records describe OCR of the current downloaded official image bytes.
// Their text is untrusted notice content, never model/tool instructions.
type workerImagePassage struct {
	SourceID string `json:"source_id"`
	URL      string `json:"url"`
	SHA256   string `json:"content_sha256"`
	Bytes    int64  `json:"bytes"`
	Status   string `json:"status"`
	Engine   string `json:"ocr_engine"`
	Text     string `json:"text"`
}

func verifiedWorkerImagePassages(e materializedEvidence) ([]workerImagePassage, error) {
	if len(e.LocalImageAnalysis) == 0 {
		return nil, nil
	}
	if len(e.LocalImageAnalysis) != len(e.ImageURLs) || len(e.LocalImages) != len(e.ImageURLs) {
		return nil, fmt.Errorf("worker OCR evidence does not cover the current image set")
	}
	local := map[string]localImage{}
	for _, img := range e.LocalImages {
		if _, exists := local[img.URL]; exists {
			return nil, fmt.Errorf("worker OCR duplicate downloaded image")
		}
		local[img.URL] = img
	}
	out := make([]workerImagePassage, 0, len(e.ImageURLs))
	seen := map[string]bool{}
	for i, url := range e.ImageURLs {
		row := e.LocalImageAnalysis[i]
		img, ok := local[url]
		digest, err := hex.DecodeString(img.SHA256)
		if seen[url] || !ok || url == "" || mapString(row, "url") != url || img.LocalPath == "" || img.Error != "" || img.Bytes <= 0 || err != nil || len(digest) != 32 || !strings.EqualFold(mapString(row, "content_sha256"), img.SHA256) || mapInt64(row["bytes"]) != img.Bytes || mapString(row, "ocr_engine") != "apple_vision" {
			return nil, fmt.Errorf("worker OCR image %d provenance does not match downloaded bytes", i+1)
		}
		seen[url] = true
		status := mapString(row, "status")
		text := strings.TrimSpace(mapString(row, "text"))
		if status != "ANALYZED" && status != "NO_TEXT" || status == "ANALYZED" && text == "" || status == "NO_TEXT" && text != "" {
			return nil, fmt.Errorf("worker OCR image %d is not verified readable evidence", i+1)
		}
		out = append(out, workerImagePassage{SourceID: fmt.Sprintf("notice-image-%d:%s", i+1, img.SHA256), URL: url, SHA256: img.SHA256, Bytes: img.Bytes, Status: status, Engine: "apple_vision", Text: text})
	}
	return out, nil
}

func workerOfficialPassages(e materializedEvidence) ([]string, error) {
	images, err := verifiedWorkerImagePassages(e)
	if err != nil {
		return nil, err
	}
	passages := []string{e.BodyText}
	for _, img := range images {
		if img.Status == "ANALYZED" {
			passages = append(passages, workerImageRewardParagraph(img.Text))
		}
	}
	return passages, nil
}

// Bind only the contiguous reward amount and its explicit quantity-valuation
// footnote within one verified image. Never join facts from separate images.
func workerImageRewardParagraph(text string) string {
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		if !noticeRewardEquivalentAsset.MatchString(lines[i]) || !strings.Contains(lines[i+1], "수량") || !strings.Contains(lines[i+1], "산정") || !strings.Contains(lines[i+1], "기준") {
			continue
		}
		lines[i] += " " + strings.TrimSpace(lines[i+1])
		lines[i+1] = ""
		if i+2 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+2]), "되며") {
			lines[i] += " " + strings.TrimSpace(lines[i+2])
			lines[i+2] = ""
		}
	}
	return strings.Join(lines, "\n")
}

var workerRewardMoney = regexp.MustCompile(`\d[\d,.]*\s*(?:만|억|천)?\s*(?:원|KRW)`)
var workerRewardBasis = regexp.MustCompile(`(?:[12][0-9]{3}\s*년\s*\d{1,2}\s*월\s*\d{1,2}\s*일|[12][0-9]{3}\s*\.\s*\d{1,2}\s*\.\s*\d{1,2}\s*\.?)\s*([^\n]+?)\s*기준`)
var workerRewardValueMayVary = regexp.MustCompile(`원화\s*(?:상당\s*)?(?:가치|금액)(?:가|는|은|이)?\s*(?:달라질|다를|상이할|변동할|변동될|변할)\s*수\s*있(?:습니다|어요|다|음)\s*$`)
var workerRewardValueContradiction = regexp.MustCompile(`(?:달라지|변동하|변하|상이하)지(?:는|도)?\s*(?:않|못)|(?:달라질|다를|상이할|변동할|변동될|변할)\s*수(?:는|도)?\s*없|(?:고정|동일|일정)(?:됩|합|되|하|한|인)|차이(?:가|는)?\s*없`)

// A Korean particle changes the grammatical role, not the asset identity.
// Match only a complete known asset plus one particle, never a name prefix
// such as 비트코인 in 비트코인캐시 or a neighboring noun phrase.
func workerRewardAssetNounMatches(noun, asset string) bool {
	if noun == asset {
		return true
	}
	for _, particle := range []string{"으로", "로", "을", "를", "은", "는", "이", "가"} {
		if noun == asset+particle {
			return true
		}
	}
	return false
}

func workerRewardAffirmsValueUncertainty(text string) bool {
	text = workerImageFullDateSpacing.ReplaceAllString(text, "${1}년 ${2}월 ${3}일")
	affirmed := false
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool { return r == '.' || r == '!' || r == '?' || r == '\n' }) {
		if !strings.Contains(sentence, "원화") || !strings.Contains(sentence, "가치") && !strings.Contains(sentence, "금액") {
			continue
		}
		if workerRewardValueContradiction.MatchString(sentence) {
			return false
		}
		if workerRewardValueMayVary.MatchString(strings.TrimSpace(sentence)) {
			affirmed = true
		}
	}
	return affirmed
}

func validateWorkerImageRewardQualifiers(summary string, e materializedEvidence) error {
	if !workerRewardMoney.MatchString(summary) {
		return nil
	}
	images, err := verifiedWorkerImagePassages(e)
	if err != nil {
		return err
	}
	summaryDates := workerImageCalendarIdentities(summary)
	for _, image := range images {
		if image.Status != "ANALYZED" {
			continue
		}
		lines := strings.Split(image.Text, "\n")
		for lineIndex, line := range lines {
			if !strings.Contains(line, "수량") || !strings.Contains(line, "산정") || !strings.Contains(line, "기준") {
				continue
			}
			dates := workerImageCalendarIdentities(line)
			for date := range dates {
				if !summaryDates[date] {
					return fmt.Errorf("summary omits the verified image reward quantity valuation date")
				}
			}
			if !strings.Contains(summary, "수량") || !strings.Contains(summary, "기준") {
				return fmt.Errorf("summary omits the verified image reward quantity valuation basis")
			}
			basis := workerRewardBasis.FindStringSubmatch(line)
			if len(basis) != 2 {
				return fmt.Errorf("verified image reward valuation basis cannot be bound")
			}
			if lineIndex == 0 {
				return fmt.Errorf("verified image reward valuation has no adjacent reward")
			}
			reward := noticeRewardEquivalentAsset.FindStringSubmatch(lines[lineIndex-1])
			if len(reward) != 2 {
				return fmt.Errorf("verified image reward valuation has no adjacent asset")
			}
			// The source noun is the identity: its final syllable may belong
			// to the asset name itself (for example, 파이).
			asset := reward[1]
			matched := false
			// Keep the basis attached to the reward, not a date elsewhere in the answer.
			canonical := workerImageFullDateSpacing.ReplaceAllString(summary, "${1}년 ${2}월 ${3}일")
			for _, sentence := range strings.FieldsFunc(canonical, func(r rune) bool { return r == '.' || r == '\n' || r == '!' || r == '?' }) {
				if !workerRewardMoney.MatchString(sentence) || !strings.Contains(sentence, "수량") || !strings.Contains(sentence, strings.TrimSpace(basis[1])+" 기준") {
					continue
				}
				claimedReward := noticeRewardEquivalentAsset.FindStringSubmatch(sentence)
				if len(claimedReward) != 2 || !workerRewardAssetNounMatches(claimedReward[1], asset) || noticeRewardDenied.MatchString(sentence) || noticeRewardCompleted.MatchString(sentence) {
					continue
				}
				boundDates := workerImageCalendarIdentities(sentence)
				allDates := true
				for date := range dates {
					if !boundDates[date] {
						allDates = false
					}
				}
				if allDates {
					matched = true
				}
			}
			if !matched {
				return fmt.Errorf("summary reward quantity valuation basis is missing, changed or detached")
			}
			if workerRewardAffirmsValueUncertainty(image.Text) {
				if !workerRewardAffirmsValueUncertainty(summary) {
					return fmt.Errorf("summary omits the verified image reward payout value qualification")
				}
			}
		}
	}
	return nil
}

var workerImageFullDateSpacing = regexp.MustCompile(`([12][0-9]{3})\s*\.\s*([0-9]{1,2})\s*\.\s*([0-9]{1,2})\.?`)
var workerImageShortDate = regexp.MustCompile(`(?:^|[^0-9])([0-9]{2})\s*\.\s*([0-9]{1,2})\s*\.\s*([0-9]{1,2})\b`)

func workerImageCalendarIdentities(text string) map[string]bool {
	canonical := workerImageFullDateSpacing.ReplaceAllString(text, "${1}년 ${2}월 ${3}일")
	_, values := noticeQuestionCalendarDates(canonical)
	for _, m := range workerImageShortDate.FindAllStringSubmatch(text, -1) {
		y, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		check := time.Date(2000+y, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		if int(check.Month()) != month || check.Day() != day {
			values["INVALID:"+m[0]] = true
			continue
		}
		// Preserve the abbreviated year as an identity, not an invented UTC
		// instant or a guessed century. Month/day wording can match it directly.
		values[fmt.Sprintf("SHORT_DATE:%02d-%02d-%02d", y, month, day)] = true
		values[fmt.Sprintf("MONTH_DAY:%02d-%02d", month, day)] = true
	}
	return values
}

func validateWorkerImageSummaryEvidence(summary string, e materializedEvidence) error {
	images, err := verifiedWorkerImagePassages(e)
	if err != nil {
		return err
	}
	if len(images) == 0 {
		return nil
	}
	texts := []string{e.Title, e.BodyText}
	for _, img := range images {
		if img.Status == "ANALYZED" {
			texts = append(texts, img.Text)
		}
	}
	allowed := workerImageCalendarIdentities(strings.Join(texts, "\n"))
	for value := range workerImageCalendarIdentities(summary) {
		if strings.HasPrefix(value, "INVALID:") || !allowed[value] {
			return fmt.Errorf("summary calendar value is absent from verified official passages: %s", value)
		}
	}
	return validateWorkerImageRewardQualifiers(summary, e)
}
