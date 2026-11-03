package hermesrunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"market-share-go/internal/notice"
	"market-share-go/services/collector/restbudget"
)

const (
	maximumNoticeImages     = 16
	maximumNoticeImageBytes = int64(24 << 20)
)

// The Bithumb financial-partner banner is repeated site chrome, not evidence
// about the notice it is attached to. Match reviewed bytes rather than aspect
// ratio so wide tables and event images remain eligible evidence.
const bithumbFinancialPartnerBannerSHA256 = "a504666751770103f2ee648b150dca20e93323a69fc9fdb43bf7dd9bd64d8298"

var knownDecorativeNoticeImageURLs = map[string]struct{}{
	"https://feed-content.bithumb.com/cms/1e8ba4b0-446f-4600-a036-70514e5327de.png": {},
	"https://feed-content.bithumb.com/cms/3d3ca08d-24bc-49c0-a0d0-63970ebe997b.png": {},
	"https://feed-content.bithumb.com/cms/4937e875-6e12-496b-86e0-840543a05720.png": {},
	"https://feed-content.bithumb.com/cms/4b85b00d-5a9d-4904-b4cb-79bad54d80e4.png": {},
	"https://feed-content.bithumb.com/cms/51d3bc1a-64ef-44aa-a153-b7a399c57382.png": {},
	"https://feed-content.bithumb.com/cms/538b2b85-b6fd-41fd-b57f-5716b87d5748.png": {},
	"https://feed-content.bithumb.com/cms/60a6a8a0-5af5-40e9-9b03-fce42b479c9d.png": {},
	"https://feed-content.bithumb.com/cms/7339236a-5026-43a3-9913-af2091a698af.png": {},
	"https://feed-content.bithumb.com/cms/f5afc89e-c155-43b7-b3e7-a8e831699d6a.png": {},
	"https://feed-content.bithumb.com/cms/f9e474a2-2fae-4368-8f56-2255032b8988.png": {},
	"https://feed-content.bithumb.com/cms/fd39daeb-504a-43da-b1bc-cf7f366cc344.png": {},
}

func (p *pipeline) materializeImages(
	ctx context.Context,
	evidence notice.HermesEvidence,
	stem string,
) (materializedEvidence, func(), error) {
	result := materializedEvidence{HermesEvidence: evidence, LocalImages: []localImage{}}
	if len(evidence.ImageURLs) == 0 {
		return result, func() {}, nil
	}
	cacheRoot := filepath.Join(p.outputDir, "image-cache")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return materializedEvidence{}, func() {}, err
	}
	directory, err := os.MkdirTemp(cacheRoot, stem+"-")
	if err != nil {
		return materializedEvidence{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	var total int64
	for index, imageURL := range evidence.ImageURLs {
		if index >= maximumNoticeImages {
			result.LocalImages = append(result.LocalImages, localImage{URL: imageURL, Error: "local image limit exceeded; use official URL"})
			continue
		}
		payload, fetchErr := p.config.Fetcher.GetBytes(ctx, restbudget.Request{
			Exchange: evidence.Item.Exchange,
			Group:    "public",
			URL:      imageURL,
			Priority: restbudget.PriorityRegistry,
			Cost:     1,
			Accept:   "image/avif,image/webp,image/png,image/jpeg,image/gif",
			Headers: http.Header{
				"User-Agent":      {"Mozilla/5.0 (compatible; MarketShareHermesNotice/1.0)"},
				"Accept-Language": {"ko-KR,ko;q=0.9,en;q=0.7"},
			},
		}, maximumNoticeImageBytes)
		if fetchErr != nil {
			result.LocalImages = append(result.LocalImages, localImage{URL: imageURL, Error: truncate(fetchErr.Error(), 300)})
			continue
		}
		extension := imageExtension(payload)
		if extension == "" {
			result.LocalImages = append(result.LocalImages, localImage{URL: imageURL, Error: "unsupported image signature"})
			continue
		}
		if total+int64(len(payload)) > maximumNoticeImageBytes {
			result.LocalImages = append(result.LocalImages, localImage{URL: imageURL, Error: "per-notice image byte limit exceeded"})
			continue
		}
		path := filepath.Join(directory, fmt.Sprintf("image-%02d%s", index, extension))
		if writeErr := os.WriteFile(path, payload, 0o600); writeErr != nil {
			result.LocalImages = append(result.LocalImages, localImage{URL: imageURL, Error: truncate(writeErr.Error(), 300)})
			continue
		}
		absolute, absoluteErr := filepath.Abs(path)
		if absoluteErr != nil {
			absolute = path
		}
		digest := sha256.Sum256(payload)
		result.LocalImages = append(result.LocalImages, localImage{
			URL: imageURL, LocalPath: absolute, Bytes: int64(len(payload)), SHA256: fmt.Sprintf("%x", digest[:]),
		})
		total += int64(len(payload))
	}
	return filterDecorativeMaterializedImages(result), cleanup, nil
}

func filterDecorativeMaterializedImages(evidence materializedEvidence) materializedEvidence {
	if !strings.EqualFold(strings.TrimSpace(evidence.Item.Exchange), "bithumb") {
		return evidence
	}
	decorativeURLs := make(map[string]bool)
	keptLocal := make([]localImage, 0, len(evidence.LocalImages))
	for _, image := range evidence.LocalImages {
		_, knownURL := knownDecorativeNoticeImageURLs[strings.TrimSpace(image.URL)]
		knownBytes := strings.EqualFold(strings.TrimSpace(image.SHA256), bithumbFinancialPartnerBannerSHA256)
		if knownURL || knownBytes {
			decorativeURLs[image.URL] = true
			continue
		}
		keptLocal = append(keptLocal, image)
	}
	keptURLs := make([]string, 0, len(evidence.ImageURLs))
	for _, imageURL := range evidence.ImageURLs {
		if decorativeURLs[imageURL] {
			continue
		}
		if _, known := knownDecorativeNoticeImageURLs[strings.TrimSpace(imageURL)]; known {
			continue
		}
		keptURLs = append(keptURLs, imageURL)
	}
	evidence.ImageURLs = keptURLs
	evidence.LocalImages = keptLocal
	return evidence
}

func hasKnownDecorativeNoticeImageURL(urls []string) bool {
	for _, imageURL := range urls {
		if _, known := knownDecorativeNoticeImageURLs[strings.TrimSpace(imageURL)]; known {
			return true
		}
	}
	return false
}

func imageExtension(payload []byte) string {
	switch {
	case bytes.HasPrefix(payload, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}):
		return ".png"
	case bytes.HasPrefix(payload, []byte{0xff, 0xd8, 0xff}):
		return ".jpg"
	case bytes.HasPrefix(payload, []byte("GIF87a")), bytes.HasPrefix(payload, []byte("GIF89a")):
		return ".gif"
	case len(payload) >= 12 && string(payload[:4]) == "RIFF" && string(payload[8:12]) == "WEBP":
		return ".webp"
	case len(payload) >= 12 && strings.HasPrefix(string(payload[4:12]), "ftypavi"):
		return ".avif"
	default:
		return ""
	}
}
