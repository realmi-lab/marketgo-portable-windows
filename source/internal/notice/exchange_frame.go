package notice

import (
	"fmt"
	"net/url"
	"strings"
)

// domesticExchangeFrame describes stable exchange-owned document locations.
// It is intentionally about venue formats, never a notice ID, asset, or title.
// Complex venue-specific transports may still use a dedicated adapter, while
// ordinary row mappings share the deterministic parser below.
type domesticExchangeFrame struct {
	Source      SourceConfig
	SourceType  string
	Query       noticeQueryFrame
	PayloadRows *noticeRowFrame
	PageScript  string
	PageRows    *noticeRowFrame
	Detail      noticeDetailFrame
}

type noticeQueryFrame struct {
	PageParameter string
	PageOffset    int
	Fixed         [][2]string
}

type noticeRowFrame struct {
	RowsPath           []string
	IDFields           []string
	IDMode             string
	TitleFields        []string
	URLFields          []string
	URLFormat          string
	PublishedFields    []string
	UpdatedFields      []string
	CategoryFields     []string
	CategoryListFields []string
	BodyFields         []string
}

type noticeDetailFrame struct {
	Adapter       string
	Kind          string
	Endpoint      string
	Query         noticeQueryFrame
	Accept        string
	DataPath      []string
	BodyFields    []string
	UpdatedFields []string
}

var domesticExchangeOrder = []string{"upbit", "bithumb", "coinone", "korbit", "gopax"}

var domesticExchangeFrames = map[string]domesticExchangeFrame{
	"upbit": {
		Source:     SourceConfig{Exchange: "upbit", URL: "https://api-manager.upbit.com/api/v1/announcements", PageStart: 1, Paginated: true, FetchDetail: true},
		SourceType: "official_page_json",
		Detail: noticeDetailFrame{
			Adapter: "JSON", Kind: "OFFICIAL_DETAIL_API",
			Endpoint: "https://api-manager.upbit.com/api/v1/announcements/%s",
			Query:    noticeQueryFrame{Fixed: [][2]string{{"os", "web"}}}, Accept: "application/json",
			DataPath: []string{"data"}, BodyFields: []string{"body"}, UpdatedFields: []string{"listed_at"},
		},
		Query: noticeQueryFrame{PageParameter: "page", Fixed: [][2]string{{"os", "web"}, {"per_page", "20"}, {"category", "all"}}},
		PayloadRows: &noticeRowFrame{
			RowsPath: []string{"data", "notices"}, IDFields: []string{"id", "uuid"},
			TitleFields: []string{"title"}, URLFormat: "https://upbit.com/service_center/notice?id=%s",
			PublishedFields: []string{"first_listed_at", "listed_at"}, UpdatedFields: []string{"listed_at"},
			CategoryFields: []string{"category"}, BodyFields: []string{"body"},
		},
	},
	"bithumb": {
		Source:     SourceConfig{Exchange: "bithumb", URL: "https://feed.bithumb.com/notice", PageStart: 1, Paginated: true, PageParser: true, FetchDetail: true},
		SourceType: "official_page_json",
		Detail:     noticeDetailFrame{Adapter: "PAGE", Kind: "OFFICIAL_DETAIL_PAGE", Accept: "text/html,application/xhtml+xml"},
		Query:      noticeQueryFrame{PageParameter: "page"},
		PayloadRows: &noticeRowFrame{
			IDFields: []string{"pc_url"}, IDMode: "URL", TitleFields: []string{"title"}, URLFields: []string{"pc_url"},
			PublishedFields: []string{"published_at"}, UpdatedFields: []string{"modified_at"},
			CategoryListFields: []string{"categories"},
		},
		PageScript: "__NEXT_DATA__",
		PageRows: &noticeRowFrame{
			RowsPath: []string{"props", "pageProps", "noticeList"}, IDFields: []string{"id"},
			TitleFields: []string{"title"}, URLFormat: "https://feed.bithumb.com/notice/%s",
			PublishedFields: []string{"publicationDateTime"}, UpdatedFields: []string{"modifyDateTime"},
			CategoryFields: []string{"categoryName1", "categoryName2"},
		},
	},
	"coinone": {
		Source:     SourceConfig{Exchange: "coinone", URL: "https://api-gateway.coinone.co.kr/notice/v1/announcements/posts", PageStart: 1, Paginated: true, FetchDetail: true},
		SourceType: "official_page_api",
		Detail: noticeDetailFrame{
			Adapter: "JSON", Kind: "OFFICIAL_DETAIL_API",
			Endpoint: "https://api-gateway.coinone.co.kr/notice/v1/announcements/posts/%s", Accept: "application/json",
			DataPath: []string{"body"}, BodyFields: []string{"content"}, UpdatedFields: []string{"updatedAt"},
		},
		Query: noticeQueryFrame{PageParameter: "page", PageOffset: -1, Fixed: [][2]string{{"size", "20"}}},
		PayloadRows: &noticeRowFrame{
			RowsPath: []string{"body", "notices"}, IDFields: []string{"id"}, TitleFields: []string{"title"},
			URLFormat: "https://coinone.co.kr/info/notice/%s", PublishedFields: []string{"exposedAt", "createdAt"},
			UpdatedFields: []string{"updatedAt"},
		},
	},
	"korbit": {
		Source:     SourceConfig{Exchange: "korbit", URL: "https://api.korbit.co.kr/v2/notices", PageStart: 1, FetchDetail: true},
		SourceType: "official_api",
		Detail:     noticeDetailFrame{Adapter: "KORBIT_CONTENTFUL", Kind: "OFFICIAL_DETAIL_API", Accept: "application/json"},
		PayloadRows: &noticeRowFrame{
			RowsPath: []string{"data"}, IDFields: []string{"url"}, IDMode: "KORBIT_CONTENTFUL",
			TitleFields: []string{"title"}, URLFields: []string{"url"},
			PublishedFields: []string{"createdAt"}, UpdatedFields: []string{"updatedAt"},
		},
	},
	"gopax": {
		Source:     SourceConfig{Exchange: "gopax", URL: "https://api.gopax.co.kr/notices", PageStart: 0, Paginated: true, FetchDetail: true},
		SourceType: "official_page_json",
		Detail:     noticeDetailFrame{Adapter: "PAGE", Kind: "OFFICIAL_DETAIL_PAGE", Accept: "text/html,application/xhtml+xml"},
		Query:      noticeQueryFrame{PageParameter: "page", Fixed: [][2]string{{"limit", "20"}, {"type", "0"}, {"format", "1"}}},
		PayloadRows: &noticeRowFrame{
			IDFields: []string{"id"}, TitleFields: []string{"title"},
			URLFormat: "https://www.gopax.co.kr/notice/detail?id=%s", PublishedFields: []string{"createdAt"},
			UpdatedFields: []string{"updatedAt"}, CategoryFields: []string{"type"}, BodyFields: []string{"content"},
		},
	},
}

func domesticFrame(exchange string) (domesticExchangeFrame, bool) {
	value, ok := domesticExchangeFrames[strings.ToLower(strings.TrimSpace(exchange))]
	return value, ok
}

func IsDomesticExchange(exchange string) bool {
	_, ok := domesticFrame(exchange)
	return ok
}

func DomesticExchanges() []string {
	return append([]string(nil), domesticExchangeOrder...)
}

func domesticSourceConfigs() []SourceConfig {
	result := make([]SourceConfig, 0, len(domesticExchangeOrder))
	for _, exchange := range domesticExchangeOrder {
		result = append(result, domesticExchangeFrames[exchange].Source)
	}
	return result
}

func (frame noticeQueryFrame) values(page int) url.Values {
	if frame.PageParameter == "" && len(frame.Fixed) == 0 {
		return nil
	}
	result := make(url.Values, len(frame.Fixed)+1)
	if frame.PageParameter != "" {
		result.Set(frame.PageParameter, fmt.Sprint(page+frame.PageOffset))
	}
	for _, pair := range frame.Fixed {
		result.Set(pair[0], pair[1])
	}
	return result
}

func parseRowsWithFrame(
	exchange string,
	root any,
	frame noticeRowFrame,
	detectedMS int64,
	knownAssets map[string]struct{},
) []Item {
	rows := objectRows(path(root, frame.RowsPath...))
	result := make([]Item, 0, len(rows))
	for _, row := range rows {
		id := rowFirstText(row, frame.IDFields)
		sourceURL := rowFirstText(row, frame.URLFields)
		switch frame.IDMode {
		case "URL":
			if id == "" {
				id = sourceURL
			}
		case "KORBIT_CONTENTFUL":
			id = korbitContentfulID(Item{NoticeID: id, URL: sourceURL})
			if id == "" {
				id = sourceURL
			}
		}
		if sourceURL == "" && frame.URLFormat != "" {
			sourceURL = fmt.Sprintf(frame.URLFormat, id)
		}
		categories := make([]string, 0, len(frame.CategoryFields)+len(frame.CategoryListFields))
		for _, field := range frame.CategoryFields {
			if value := textValue(row[field]); value != "" {
				categories = append(categories, value)
			}
		}
		for _, field := range frame.CategoryListFields {
			if value := joinedStrings(row[field]); value != "" {
				categories = append(categories, value)
			}
		}
		result = append(result, normalizedItem(
			exchange, id, rowFirstText(row, frame.TitleFields), sourceURL,
			rowFirstValue(row, frame.PublishedFields), detectedMS, strings.Join(categories, ","),
			rowFirstValue(row, frame.UpdatedFields), rowFirstText(row, frame.BodyFields), knownAssets,
		))
	}
	return result
}

func rowFirstValue(row map[string]any, fields []string) any {
	for _, field := range fields {
		if value := row[field]; textValue(value) != "" {
			return value
		}
	}
	return nil
}

func rowFirstText(row map[string]any, fields []string) string {
	return textValue(rowFirstValue(row, fields))
}

func sourceTypeFor(exchange string) string {
	if frame, ok := domesticFrame(exchange); ok {
		return frame.SourceType
	}
	return noticeSourceTypes[exchange]
}
