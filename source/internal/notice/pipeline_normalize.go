package notice

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// NormalizePipelineHTML preserves structural boundaries and byte references
// into the separately archived decoded body markup. The HTTP response object
// remains untouched; normalized offsets refer to the returned UTF-8 text.
func NormalizePipelineHTML(markup string) (string, []PipelineBlock, error) {
	tokenizer := html.NewTokenizer(strings.NewReader(markup))
	tokenizer.SetMaxBuf(1_000_000)
	var blocks []PipelineBlock
	var current strings.Builder
	position, start, end := 0, 0, 0
	kind := "TEXT"
	ignored := ""
	flush := func() {
		cells := strings.Split(current.String(), "\t")
		for i, cell := range cells {
			cells[i] = strings.Join(strings.Fields(cell), " ")
		}
		text := strings.TrimSpace(strings.Join(cells, "\t"))
		if text != "" {
			blocks = append(blocks, PipelineBlock{ID: fmt.Sprintf("b%d", len(blocks)+1), Kind: kind, Text: text, SourceStart: start, SourceEnd: end})
		}
		current.Reset()
		kind = "TEXT"
	}
	for {
		typeOf := tokenizer.Next()
		raw := tokenizer.Raw()
		offset := position
		position += len(raw)
		if typeOf == html.ErrorToken {
			if tokenizer.Err() != io.EOF {
				return "", nil, tokenizer.Err()
			}
			flush()
			break
		}
		token := tokenizer.Token()
		if ignored != "" {
			if typeOf == html.EndTagToken && token.Data == ignored {
				ignored = ""
			}
			continue
		}
		if typeOf == html.StartTagToken && (token.Data == "script" || token.Data == "style" || token.Data == "noscript") {
			ignored = token.Data
			continue
		}
		if typeOf == html.TextToken {
			if current.Len() == 0 {
				start = offset
			}
			current.WriteString(token.Data)
			end = position
			continue
		}
		if typeOf != html.StartTagToken && typeOf != html.EndTagToken && typeOf != html.SelfClosingTagToken {
			continue
		}
		switch token.Data {
		case "td", "th":
			if typeOf == html.EndTagToken {
				current.WriteByte('\t')
			}
		case "h1", "h2", "h3", "h4", "h5", "h6":
			flush()
			if typeOf == html.StartTagToken {
				kind = "HEADING"
			}
		case "tr":
			flush()
			if typeOf == html.StartTagToken {
				kind = "TABLE_ROW"
			}
		case "li":
			flush()
			if typeOf == html.StartTagToken {
				kind = "LIST_ITEM"
			}
		case "br", "p", "div", "section", "article", "ul", "ol", "table", "thead", "tbody", "tfoot", "blockquote":
			flush()
		}
	}
	var text strings.Builder
	for i := range blocks {
		if i > 0 {
			text.WriteByte('\n')
		}
		blocks[i].Start = text.Len()
		text.WriteString(blocks[i].Text)
		blocks[i].End = text.Len()
	}
	return text.String(), blocks, nil
}
