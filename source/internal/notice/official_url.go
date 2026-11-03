package notice

import (
	"net/url"
	"strings"
)

type officialNoticeURLRule struct {
	hosts       []string
	exactPaths  []string
	pathPrefix  string
	queryIDName string
}

var hermesOfficialHosts = map[string][]string{
	"upbit":   {"upbit.com"},
	"bithumb": {"bithumb.com"},
	"coinone": {"coinone.co.kr"},
	"korbit":  {"korbit.co.kr"},
	"gopax":   {"gopax.co.kr"},
	"binance": {"binance.com"},
	"bybit":   {"bybit.com"},
	"okx":     {"okx.com"},
}

var domesticOfficialNoticeURLRules = map[string]officialNoticeURLRule{
	"upbit": {
		hosts: []string{"upbit.com"}, exactPaths: []string{"/service_center/notice"}, queryIDName: "id",
	},
	"bithumb": {
		hosts: []string{"feed.bithumb.com"}, pathPrefix: "/notice/",
	},
	"coinone": {
		hosts: []string{"coinone.co.kr"}, pathPrefix: "/info/notice/",
	},
	"korbit": {
		hosts: []string{"www.korbit.co.kr"}, exactPaths: []string{"/notice/detail", "/notice/detail/"}, queryIDName: "noticeId",
	},
	"gopax": {
		hosts: []string{"www.gopax.co.kr"}, exactPaths: []string{"/notice/detail"}, queryIDName: "id",
	},
}

// IsOfficialNoticeURL proves that a URL is on the supported exchange's
// official notice surface. Domestic exchanges use their canonical public
// notice path, while foreign exchanges retain the official-domain boundary.
func IsOfficialNoticeURL(exchange, rawURL string) bool {
	_, ok := officialNoticeURLID(exchange, rawURL)
	return ok
}

// OfficialNoticeURLMatchesID additionally proves that the public URL embeds
// the same notice identity returned by the official listing API.
func OfficialNoticeURLMatchesID(exchange, noticeID, rawURL string) bool {
	wanted := strings.TrimSpace(noticeID)
	if _, domestic := domesticOfficialNoticeURLRules[strings.ToLower(strings.TrimSpace(exchange))]; !domestic {
		return wanted != "" && IsOfficialNoticeURL(exchange, rawURL)
	}
	actual, ok := officialNoticeURLID(exchange, rawURL)
	return ok && wanted != "" && actual == wanted
}

func officialNoticeURLID(exchange, rawURL string) (string, bool) {
	exchange = strings.ToLower(strings.TrimSpace(exchange))
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" {
		return "", false
	}
	rule, domestic := domesticOfficialNoticeURLRules[exchange]
	if !domestic {
		hosts, supported := hermesOfficialHosts[exchange]
		return "", supported && matchesOfficialHost(parsed.Hostname(), hosts)
	}
	if !matchesCanonicalHost(parsed.Hostname(), rule.hosts) {
		return "", false
	}
	if rule.pathPrefix != "" {
		if parsed.RawQuery != "" || !strings.HasPrefix(parsed.Path, rule.pathPrefix) {
			return "", false
		}
		identity := strings.Trim(strings.TrimPrefix(parsed.Path, rule.pathPrefix), "/")
		if identity == "" || strings.Contains(identity, "/") {
			return "", false
		}
		decoded, err := url.PathUnescape(identity)
		return decoded, err == nil && strings.TrimSpace(decoded) != ""
	}
	if !containsExactString(rule.exactPaths, parsed.Path) {
		return "", false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query[rule.queryIDName]) != 1 {
		return "", false
	}
	identity := strings.TrimSpace(query.Get(rule.queryIDName))
	return identity, identity != ""
}

func matchesCanonicalHost(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, value := range allowed {
		if host == value {
			return true
		}
	}
	return false
}

func containsExactString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func matchesOfficialHost(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, suffix := range allowed {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
