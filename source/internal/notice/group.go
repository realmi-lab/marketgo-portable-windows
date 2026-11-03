package notice

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

const dayMS = int64(86_400_000)

var (
	groupFollowUpPattern = regexp.MustCompile(`(?i)재개|해제|연장|종료|완료|개시|시작|resume|resumed|reopen|delist|terminated|completed|start`)
	groupInitialPattern  = regexp.MustCompile(`(?i)중단|중지|지정|신규|상장|추가|예정|suspend|suspended|halt|new listing|caution|scheduled`)
	groupBracketPattern  = regexp.MustCompile(`\([^)]*\)|\[[^]]*\]`)
	groupDatePattern     = regexp.MustCompile(`\b20\d{2}(?:[./-]\d{1,2}){1,2}(?:\s+\d{1,2}:\d{2})?\b`)
	groupNumberPattern   = regexp.MustCompile(`\b\d+(?:[./:-]\d+)*\b`)
	groupNoisePattern    = regexp.MustCompile(`[^a-z0-9가-힣]+`)
)

var groupNoisePhrases = []string{
	"일시", "중단", "중지", "재개", "해제", "연장", "종료", "지정", "개시",
	"시작", "완료", "변경", "예정", "신규", "추가", "안내", "공지", "알림",
	"관련", "suspended", "suspend", "resume", "resumed", "reopen", "delist",
	"terminated", "completed", "start", "scheduled",
}

type groupRecord struct {
	index     int
	exchange  string
	family    string
	assets    []string
	signature string
	time      int64
}

type lifecycleGroup struct {
	firstTime int64
	firstID   string
	lastTime  int64
	lastItem  Item
}

func AssignEventGroupIDs(items []Item) []Item {
	result := append([]Item(nil), items...)
	pending := make([]groupRecord, 0, len(result))
	for index := range result {
		item := &result[index]
		if strings.TrimSpace(item.EventGroupID) != "" {
			continue
		}
		exchange := strings.ToLower(strings.TrimSpace(item.Exchange))
		family := eventGroupFamily(item.Category)
		assets := normalizedGroupAssets(item.Assets)
		signature := eventGroupTitleSignature(item.Title)
		if exchange == "" || family == "" || len(assets) == 0 || signature == "" {
			item.EventGroupID = exchange + ":" + item.NoticeID
			continue
		}
		pending = append(pending, groupRecord{
			index: index, exchange: exchange, family: family, assets: assets,
			signature: signature, time: eventGroupTime(*item),
		})
	}
	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].time == pending[j].time {
			return pending[i].index < pending[j].index
		}
		return pending[i].time < pending[j].time
	})
	groups := make(map[string][]*lifecycleGroup)
	for _, record := range pending {
		item := &result[record.index]
		key := groupKey(record)
		group := matchingLifecycleGroup(groups[key], record, *item)
		if group == nil {
			group = &lifecycleGroup{
				firstTime: record.time, firstID: item.NoticeID,
				lastTime: record.time, lastItem: *item,
			}
			groups[key] = append(groups[key], group)
		} else {
			group.lastTime, group.lastItem = record.time, *item
		}
		item.EventGroupID = derivedGroupID(record, group)
	}
	return result
}

func eventGroupFamily(category string) string {
	switch strings.ToUpper(strings.TrimSpace(category)) {
	case "DEPOSIT_WITHDRAWAL", "DEPOSIT", "WITHDRAWAL":
		return "DEPOSIT_WITHDRAWAL"
	case "LISTING", "MARKET_ADDITION", "QUOTE_MARKET_CHANGE":
		return "LISTING_FLOW"
	case "CAUTION", "DELISTING":
		return "RISK_FLOW"
	case "SECURITY", "WALLET", "NETWORK", "NETWORK_OUTAGE", "NETWORK_UPGRADE", "TOKEN_SWAP", "REBRAND":
		return "ASSET_OPERATION"
	default:
		return ""
	}
}

func normalizedGroupAssets(values []string) []string {
	seen := make(map[string]struct{})
	for _, value := range values {
		asset := strings.ToUpper(strings.TrimSpace(value))
		if asset != "" {
			seen[asset] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for asset := range seen {
		result = append(result, asset)
	}
	sort.Strings(result)
	return result
}

func eventGroupTime(item Item) int64 {
	if strings.EqualFold(item.TimestampPrecision, "SECOND") && item.PublishedTSMS > 0 {
		return item.PublishedTSMS
	}
	firstSchedule := int64(0)
	for _, schedule := range item.Schedules {
		for _, value := range []int64{schedule.StartTSMS, schedule.EndTSMS} {
			if value > 0 && (firstSchedule == 0 || value < firstSchedule) {
				firstSchedule = value
			}
		}
	}
	if firstSchedule > 0 {
		return firstSchedule
	}
	if item.PublishedTSMS > 0 {
		return item.PublishedTSMS
	}
	return item.DetectedTSMS
}

func eventGroupTitleSignature(title string) string {
	text := strings.ToLower(norm.NFKC.String(title))
	text = groupBracketPattern.ReplaceAllString(text, " ")
	text = groupDatePattern.ReplaceAllString(text, " ")
	text = groupNumberPattern.ReplaceAllString(text, " ")
	for _, phrase := range groupNoisePhrases {
		text = strings.ReplaceAll(text, phrase, " ")
	}
	text = strings.ReplaceAll(text, "유의종목", " riskflow ")
	text = strings.ReplaceAll(text, "거래지원", " riskflow ")
	return groupNoisePattern.ReplaceAllString(text, "")
}

func eventGroupTransition(title string) string {
	if groupInitialPattern.MatchString(title) {
		return "INITIAL"
	}
	if groupFollowUpPattern.MatchString(title) {
		return "FOLLOW_UP"
	}
	return "NEUTRAL"
}

func matchingLifecycleGroup(candidates []*lifecycleGroup, record groupRecord, item Item) *lifecycleGroup {
	window := eventGroupWindow(record.family)
	for index := len(candidates) - 1; index >= 0; index-- {
		candidate := candidates[index]
		gap := record.time - candidate.lastTime
		if record.time > 0 && candidate.lastTime > 0 && gap >= 0 && gap <= window &&
			eventGroupCanJoin(candidate.lastItem, item) {
			return candidate
		}
	}
	return nil
}

func eventGroupWindow(family string) int64 {
	if family == "RISK_FLOW" {
		return 31 * dayMS
	}
	return 14 * dayMS
}

func eventGroupCanJoin(previous, current Item) bool {
	left, right := eventGroupTransition(previous.Title), eventGroupTransition(current.Title)
	if left != right && ((left == "INITIAL" && right == "FOLLOW_UP") ||
		(left == "FOLLOW_UP" && right == "INITIAL")) {
		return true
	}
	if left == right || left == "NEUTRAL" || right == "NEUTRAL" {
		return false
	}
	for _, previousPoint := range eventGroupSchedulePoints(previous) {
		for _, currentPoint := range eventGroupSchedulePoints(current) {
			difference := previousPoint - currentPoint
			if difference < 0 {
				difference = -difference
			}
			if difference <= 3*dayMS {
				return true
			}
		}
	}
	return false
}

func eventGroupSchedulePoints(item Item) []int64 {
	seen := make(map[int64]struct{})
	for _, schedule := range item.Schedules {
		if schedule.StartTSMS > 0 {
			seen[schedule.StartTSMS] = struct{}{}
		}
		if schedule.EndTSMS > 0 {
			seen[schedule.EndTSMS] = struct{}{}
		}
	}
	result := make([]int64, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func groupKey(record groupRecord) string {
	return strings.Join([]string{
		record.exchange, record.family, strings.Join(record.assets, ","), record.signature,
	}, "|")
}

func derivedGroupID(record groupRecord, group *lifecycleGroup) string {
	payload := strings.Join([]string{
		record.exchange, record.family, strings.Join(record.assets, ","),
		record.signature, strconv.FormatInt(group.firstTime, 10), group.firstID,
	}, "|")
	digest := sha1.Sum([]byte(payload))
	return "derived:" + record.exchange + ":" + hex.EncodeToString(digest[:])[:16]
}
