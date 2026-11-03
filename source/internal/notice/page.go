package notice

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	coinoneDatePattern  = regexp.MustCompile(`(?s)href="/info/notice/(?P<id>\d+)"[^>]*>.*?StyledMuiTypography-Date[^>]*>(?P<date>\d{4}-\d{2}-\d{2})</p>`)
	bybitArticlePattern = regexp.MustCompile(`(?s)<a href="(?P<url>/en/article/[^"]+)"[^>]*>(?P<body>.*?)</a>`)
	bybitDatePattern    = regexp.MustCompile(`article-item-date[^>]*>(?P<date>[^<]+)</div>`)
	spanPattern         = regexp.MustCompile(`(?s)<span[^>]*>(.*?)</span>`)
	tagPattern          = regexp.MustCompile(`<[^>]+>`)
	imagePattern        = regexp.MustCompile(`(?i)<img[^>]+(?:src|data-src)=["']([^"']+)`)
	articlePattern      = regexp.MustCompile(`(?is)<article[^>]*>(.*?)</article>`)
	richContentPattern  = regexp.MustCompile(`(?is)<div[^>]+class=["'][^"']*richTextContent[^"']*["'][^>]*>(.*?)</div>`)
	imageExtension      = regexp.MustCompile(`(?i)\.(?:avif|gif|jpe?g|png|webp)(?:\?|$)`)
)

func ParsePage(exchange, page string, detected time.Time, knownAssets map[string]struct{}) ([]Item, error) {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	detectedMS := detected.UnixMilli()
	switch exchange {
	case "coinone":
		payload, err := scriptJSON(page, "notice-list-item-list-json-ld")
		if err != nil {
			return nil, err
		}
		dates := coinoneDates(page)
		result := make([]Item, 0)
		for _, row := range objectRows(path(payload, "itemListElement")) {
			url := textValue(row["url"])
			id := strings.TrimSuffix(url, "/")
			if index := strings.LastIndex(id, "/"); index >= 0 {
				id = id[index+1:]
			}
			item := normalizedItem(exchange, id, textValue(row["name"]), url,
				dates[id], detectedMS, "", nil, "", knownAssets)
			item.TimestampPrecision = "DAY"
			result = append(result, item)
		}
		return result, nil
	case "bithumb":
		frame, ok := domesticFrame(exchange)
		if !ok || frame.PageScript == "" || frame.PageRows == nil {
			return nil, fmt.Errorf("missing domestic page frame: %s", exchange)
		}
		payload, err := scriptJSON(page, frame.PageScript)
		if err != nil {
			return nil, err
		}
		rows, validList := path(payload, frame.PageRows.RowsPath...).([]any)
		if !validList {
			return nil, fmt.Errorf("unresolved %s official notice list schema", exchange)
		}
		for _, row := range rows {
			if _, ok := row.(map[string]any); !ok {
				return nil, fmt.Errorf("invalid %s official notice list row", exchange)
			}
		}
		return parseRowsWithFrame(exchange, payload, *frame.PageRows, detectedMS, knownAssets), nil
	case "bybit":
		return parseBybitPage(page, detectedMS, knownAssets), nil
	case "okx":
		payload, err := scriptJSON(page, "appState")
		if err != nil {
			return nil, err
		}
		result := make([]Item, 0)
		for _, row := range objectRows(path(payload, "appContext", "initialProps", "sectionData", "articleList", "list")) {
			slug := firstText(row["slug"], row["id"])
			id := firstText(row["id"], slug)
			result = append(result, normalizedItem(exchange, id, textValue(row["title"]),
				"https://www.okx.com/help/"+slug, row["publishTime"], detectedMS,
				textValue(row["sectionSlug"]), row["updatedAt"], "", knownAssets))
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported notice page exchange: %s", exchange)
	}
}

func scriptJSON(page, scriptID string) (any, error) {
	pattern := regexp.MustCompile(`(?s)<script[^>]+id=["']` + regexp.QuoteMeta(scriptID) + `["'][^>]*>(.*?)</script>`)
	match := pattern.FindStringSubmatch(page)
	if len(match) < 2 {
		return nil, fmt.Errorf("missing script payload: %s", scriptID)
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(html.UnescapeString(match[1])))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode script payload %s: %w", scriptID, err)
	}
	return value, nil
}

func coinoneDates(page string) map[string]string {
	result := make(map[string]string)
	for _, match := range coinoneDatePattern.FindAllStringSubmatch(page, -1) {
		values := namedMatches(coinoneDatePattern, match)
		result[values["id"]] = values["date"]
	}
	return result
}

func parseBybitPage(page string, detectedMS int64, knownAssets map[string]struct{}) []Item {
	result := make([]Item, 0)
	for _, match := range bybitArticlePattern.FindAllStringSubmatch(page, -1) {
		values := namedMatches(bybitArticlePattern, match)
		date := bybitDatePattern.FindStringSubmatch(values["body"])
		if len(date) == 0 {
			continue
		}
		dateValues := namedMatches(bybitDatePattern, date)
		title := ""
		for _, span := range spanPattern.FindAllStringSubmatch(values["body"], -1) {
			candidate := strings.TrimSpace(html.UnescapeString(tagPattern.ReplaceAllString(span[1], "")))
			if candidate != "" && candidate != "lg" && candidate != "..." {
				title = candidate
				break
			}
		}
		if title == "" {
			continue
		}
		url := "https://announcements.bybit.com" + values["url"]
		item := normalizedItem("bybit", values["url"], title, url,
			dateValues["date"], detectedMS, "", nil, "", knownAssets)
		item.TimestampPrecision = "DAY"
		result = append(result, item)
	}
	return result
}

func namedMatches(pattern *regexp.Regexp, match []string) map[string]string {
	result := make(map[string]string)
	for index, name := range pattern.SubexpNames() {
		if index > 0 && name != "" && index < len(match) {
			result[name] = match[index]
		}
	}
	return result
}

func ParseDetailPage(page, sourceURL string) (string, []string, int64) {
	body := ""
	imageSource := ""
	modified := int64(0)
	if payload, err := scriptJSON(page, "notice-article-json-ld"); err == nil {
		article := objectValue(payload)
		body = firstText(article["articleBody"], article["description"])
		imageSource = body
		modified = noticeTimestamp(article["dateModified"])
	}
	for _, scriptID := range []string{"appState", "__NEXT_DATA__"} {
		payload, err := scriptJSON(page, scriptID)
		if err != nil {
			continue
		}
		state := objectValue(payload)
		currentPost := objectValue(path(state, "serverSideProps", "currentPost"))
		candidate := jsonText(firstValue(currentPost["richTextContent"], currentPost["content"], currentPost["body"]))
		body, imageSource = chooseLongerBody(body, imageSource, candidate, candidate)
		modified = max(modified, noticeTimestamp(currentPost["updatedAt"]))

		pageProps := objectValue(path(state, "props", "pageProps"))
		articleDetail := objectValue(pageProps["articleDetail"])
		articleBody := textValue(articleDetail["content_html"])
		startTime := textValue(articleDetail["start_time"])
		endTime := textValue(articleDetail["end_time"])
		published := noticeTimestamp(articleDetail["date"])
		visiblePeriod := false
		for _, schedule := range ExtractSchedules(articleBody, "EVENT", published) {
			if schedule.Kind == "EVENT" && schedule.EndTSMS > 0 {
				visiblePeriod = true
				break
			}
		}
		if startTime != "" && endTime != "" && !visiblePeriod {
			articleBody += "\nEvent period: " + startTime + " UTC ~ " + endTime + " UTC"
		}
		thumbnailURLs := detailThumbnailURLs(articleDetail)
		articleImages := imageMarkup(thumbnailURLs)
		body, imageSource = chooseLongerBody(body, imageSource, articleBody, articleBody+articleImages)
		modified = max(modified, published)

		bithumb := objectValue(pageProps["data"])
		bithumbBody := html.UnescapeString(textValue(bithumb["content"]))
		attachmentURLs := bithumbAttachmentURLs(bithumb)
		body, imageSource = chooseLongerBody(
			body, imageSource, bithumbBody, bithumbBody+imageMarkup(attachmentURLs),
		)
		modified = max(modified, noticeTimestamp(bithumb["modifyDateTime"]))
	}
	for _, pattern := range []*regexp.Regexp{richContentPattern, articlePattern} {
		match := pattern.FindStringSubmatch(page)
		if len(match) > 1 && len(PlainText(match[1])) >= len(PlainText(body)) {
			body = match[1]
			imageSource = body
		}
	}
	return PlainText(body), detailImageURLs(imageSource, sourceURL), modified
}

func jsonText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := jsonText(item); text != "" {
				values = append(values, text)
			}
		}
		return strings.Join(values, "\n")
	case map[string]any:
		for _, key := range []string{"child", "children"} {
			if nested, found := typed[key]; found {
				return jsonText(nested)
			}
		}
		values := make([]string, 0)
		for _, key := range []string{"value", "text", "body", "content", "description"} {
			if nested, found := typed[key]; found {
				if text := jsonText(nested); text != "" {
					values = append(values, text)
				}
			}
		}
		if len(values) > 0 {
			return strings.Join(values, "\n")
		}
		for _, nested := range typed {
			if text := jsonText(nested); text != "" {
				values = append(values, text)
			}
		}
		return strings.Join(values, "\n")
	default:
		return ""
	}
}

func detailThumbnailURLs(article map[string]any) []string {
	connection := objectValue(article["thumbnailConnection"])
	result := make([]string, 0)
	for _, edge := range objectRows(connection["edges"]) {
		if value := textValue(objectValue(edge["node"])["url"]); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func bithumbAttachmentURLs(detail map[string]any) []string {
	result := make([]string, 0)
	for _, attachment := range objectRows(detail["attachfileList"]) {
		fileURL := textValue(attachment["fileUrl"])
		if imageExtension.MatchString(fileURL) {
			result = append(result, fileURL)
		}
	}
	return result
}

func detailImageURLs(content, sourceURL string) []string {
	base, _ := url.Parse(sourceURL)
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, match := range imagePattern.FindAllStringSubmatch(content, -1) {
		candidate := html.UnescapeString(strings.TrimSpace(match[1]))
		parsed, err := url.Parse(candidate)
		if err != nil {
			continue
		}
		if base != nil {
			parsed = base.ResolveReference(parsed)
		}
		resolved := parsed.String()
		if (parsed.Scheme != "http" && parsed.Scheme != "https") || resolved == "" {
			continue
		}
		if _, duplicate := seen[resolved]; duplicate {
			continue
		}
		seen[resolved] = struct{}{}
		result = append(result, resolved)
	}
	return result
}

func chooseLongerBody(current, currentImages, candidate, candidateImages string) (string, string) {
	currentBodyLength := len(PlainText(current))
	candidateBodyLength := len(PlainText(candidate))
	if candidateBodyLength > currentBodyLength ||
		(candidateBodyLength == currentBodyLength &&
			len(imagePattern.FindAllString(candidateImages, -1)) > len(imagePattern.FindAllString(currentImages, -1))) {
		return candidate, candidateImages
	}
	return current, currentImages
}

func objectValue(value any) map[string]any {
	object, _ := value.(map[string]any)
	if object == nil {
		return map[string]any{}
	}
	return object
}

func imageMarkup(urls []string) string {
	var builder strings.Builder
	for _, value := range urls {
		builder.WriteString(`<img src="`)
		builder.WriteString(value)
		builder.WriteString(`">`)
	}
	return builder.String()
}
