package deepdoc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// BBox Bounding Box
type BBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Block unit of PDF
type Block struct {
	ID   string
	Type string
	// "heading", "text", "table", "list", "faq", "caption", "scrap"
	Text     string
	PageNo   int
	BBox     *BBox
	Metadata map[string]any
}

type Page struct {
	PageNo int
	Blocks []Block
}

type PDFTextExtraction struct {
	Text     string // context of all pdf
	Pages    []Page // block divide by page
	Metadata map[string]any
}

type OCRClient interface {
	Recognize(ctx context.Context, filename string, reader io.Reader) ([]Block, error)
}

type ExtractOptions struct {
	OCR OCRClient
}

func ExtractPDFText(ctx context.Context, filename string, reader io.Reader) (*PDFTextExtraction, error) {
	return ExtractPDF(ctx, filename, reader, ExtractOptions{})
}

func ExtractPDF(ctx context.Context, filename string, reader io.Reader, opts ExtractOptions) (*PDFTextExtraction, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	text := ""
	source := "plain_text"
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF")) {
		source = "pdf_content_stream"
		text = extractPDFLiteralText(data)
	} else {
		text = strings.TrimPrefix(string(data), "\ufeff")
	}
	if needsOCR(text) {
		if opts.OCR == nil {
			return nil, fmt.Errorf("pdf text extraction produced no usable text for %s; configure OCR for scanned or garbled PDFs", filename)
		}
		blocks, err := opts.OCR.Recognize(ctx, filename, bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		pages := blocksToPages(blocks, "deepdoc_pdf_ocr", "deepdoc_pdf_ocr_v1")
		if len(pages) == 0 {
			return nil, fmt.Errorf("pdf OCR produced no text for %s", filename)
		}
		return &PDFTextExtraction{
			Text:  joinPages(pages),
			Pages: pages,
			Metadata: map[string]any{
				"parser": "deepdoc_pdf_ocr",
				"source": "ocr",
			},
		}, nil
	}
	pages := textPages(text)
	if len(pages) == 0 {
		return nil, fmt.Errorf("pdf text extraction produced no usable text for %s; configure OCR for scanned or garbled PDFs", filename)
	}
	return &PDFTextExtraction{
		Text:  joinPages(pages),
		Pages: pages,
		Metadata: map[string]any{
			"parser": "deepdoc_pdf_text",
			"source": source,
		},
	}, nil
}

// test -> page
func textPages(text string) []Page {
	text = splitLoosePageMarkers(text)
	rawPages := strings.Split(text, "\f")
	pages := make([]Page, 0, len(rawPages))
	for i, raw := range rawPages {
		pageText := normalizeExtractedText(raw)
		if pageText == "" {
			continue
		}
		pageNo := i + 1
		blocks := pageBlocks(pageNo, pageText)
		pages = append(pages, Page{
			PageNo: pageNo,
			Blocks: blocks,
		})
	}
	return pages
}

func pageBlocks(pageNo int, pageText string) []Block {
	if blocks := faqBlocks(pageNo, pageText); len(blocks) > 0 {
		return blocks
	}
	lines := strings.Split(pageText, "\n")
	blocks := make([]Block, 0, len(lines))
	var paragraph strings.Builder
	var structured strings.Builder
	structuredType := ""
	flush := func() {
		text := normalizeExtractedText(paragraph.String())
		paragraph.Reset()
		if text == "" {
			return
		}
		blocks = append(blocks, newBlock(pageNo, len(blocks)+1, classifyTextBlock(text), text, nil))
	}
	flushStructured := func() {
		text := normalizeExtractedText(structured.String())
		if text != "" {
			blocks = append(blocks, newBlock(pageNo, len(blocks)+1, structuredType, text, nil))
		}
		structured.Reset()
		structuredType = ""
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			flushStructured()
			flush()
			continue
		}
		blockType := classifyTextBlock(line)
		if blockType == "heading" || blockType == "caption" || blockType == "scrap" {
			flushStructured()
			flush()
			blocks = append(blocks, newBlock(pageNo, len(blocks)+1, blockType, line, nil))
			continue
		}
		if blockType == "table" || blockType == "list" {
			flush()
			if structuredType != "" && structuredType != blockType {
				flushStructured()
			}
			structuredType = blockType
			if structured.Len() > 0 {
				structured.WriteByte('\n')
			}
			structured.WriteString(line)
			continue
		}
		flushStructured()
		if paragraph.Len() > 0 {
			paragraph.WriteByte('\n')
		}
		paragraph.WriteString(line)
	}
	flushStructured()
	flush()
	return blocks
}

func faqBlocks(pageNo int, pageText string) []Block {
	lines := strings.Split(pageText, "\n")
	blocks := make([]Block, 0)
	for i := 0; i < len(lines); i++ {
		question := strings.TrimSpace(lines[i])
		if !isQuestionLine(question) {
			continue
		}
		canonical := strings.TrimSpace(trimQuestionPrefix(question))
		if canonical == "" {
			continue
		}
		answerParts := make([]string, 0, 2)
		aliases := make([]string, 0)
		category := ""
		j := i + 1
		for ; j < len(lines); j++ {
			line := strings.TrimSpace(lines[j])
			if line == "" {
				if len(answerParts) > 0 {
					break
				}
				continue
			}
			if isQuestionLine(line) {
				break
			}
			if values, ok := faqMetadataValues(line, "aliases:", "alias:", "别名:"); ok {
				aliases = append(aliases, values...)
				continue
			}
			if values, ok := faqMetadataValues(line, "category:", "分类:"); ok {
				if len(values) > 0 {
					category = values[0]
				}
				continue
			}
			answerParts = append(answerParts, strings.TrimSpace(trimAnswerPrefix(line)))
		}
		if len(answerParts) == 0 {
			continue
		}
		answer := normalizeExtractedText(strings.Join(answerParts, "\n"))
		metadata := map[string]any{
			"parser":         "deepdoc_pdf_text",
			"parser_version": "deepdoc_pdf_text_v1",
			"page_no":        pageNo,
			"block_type":     "faq",
			"faq_question":   canonical,
			"faq_answer":     answer,
			"faq_aliases":    aliases,
			"chunk_hint":     "single_faq",
		}
		if category != "" {
			metadata["faq_category"] = category
		}
		blocks = append(blocks, Block{
			ID:       fmt.Sprintf("p%d_faq%d", pageNo, len(blocks)+1),
			Type:     "faq",
			Text:     canonical + "\n" + answer,
			PageNo:   pageNo,
			Metadata: metadata,
		})
		i = j - 1
	}
	return blocks
}

func newBlock(pageNo, index int, blockType, text string, bbox *BBox) Block {
	metadata := map[string]any{
		"parser":         "deepdoc_pdf_text",
		"parser_version": "deepdoc_pdf_text_v1",
		"page_no":        pageNo,
		"block_type":     blockType,
	}
	if blockType == "heading" {
		metadata["section_title"] = strings.TrimSpace(strings.TrimLeft(text, "#0123456789.、 "))
	}
	if blockType == "caption" {
		metadata["caption"] = text
	}
	if bbox != nil {
		metadata["bbox"] = bbox
	}
	return Block{ID: fmt.Sprintf("p%d_b%d", pageNo, index), Type: blockType, Text: text, PageNo: pageNo, BBox: bbox, Metadata: metadata}
}

func classifyTextBlock(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "text"
	}
	if strings.HasPrefix(trimmed, "#") || headingPattern.MatchString(trimmed) {
		if utf8RuneCount(trimmed) <= 80 {
			return "heading"
		}
	}
	if captionPattern.MatchString(trimmed) && utf8RuneCount(trimmed) <= 120 {
		return "caption"
	}
	if scrapPattern.MatchString(trimmed) && utf8RuneCount(trimmed) <= 80 {
		return "scrap"
	}
	if strings.Contains(trimmed, "|") && strings.Count(trimmed, "|") >= 2 {
		return "table"
	}
	if listPattern.MatchString(trimmed) {
		return "list"
	}
	if isQuestionLine(trimmed) {
		return "faq"
	}
	return "text"
}

func blocksToPages(blocks []Block, parserName, parserVersion string) []Page {
	byPage := map[int][]Block{}
	for i := range blocks {
		block := blocks[i]
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		if block.PageNo <= 0 {
			block.PageNo = 1
		}
		if block.ID == "" {
			block.ID = fmt.Sprintf("p%d_ocr%d", block.PageNo, len(byPage[block.PageNo])+1)
		}
		if block.Type == "" {
			block.Type = "ocr_text"
		}
		if block.Metadata == nil {
			block.Metadata = map[string]any{}
		}
		block.Metadata["parser"] = parserName
		block.Metadata["parser_version"] = parserVersion
		block.Metadata["page_no"] = block.PageNo
		block.Metadata["block_type"] = block.Type
		if block.BBox != nil {
			block.Metadata["bbox"] = block.BBox
		}
		byPage[block.PageNo] = append(byPage[block.PageNo], block)
	}
	pages := make([]Page, 0, len(byPage))
	pageNos := make([]int, 0, len(byPage))
	for pageNo := range byPage {
		pageNos = append(pageNos, pageNo)
	}
	sort.Ints(pageNos)
	for _, pageNo := range pageNos {
		pages = append(pages, Page{PageNo: pageNo, Blocks: byPage[pageNo]})
	}
	return pages
}

func joinPages(pages []Page) string {
	parts := make([]string, 0, len(pages))
	for _, page := range pages {
		pageParts := make([]string, 0, len(page.Blocks))
		for _, block := range page.Blocks {
			if strings.TrimSpace(block.Text) != "" {
				pageParts = append(pageParts, block.Text)
			}
		}
		if len(pageParts) > 0 {
			parts = append(parts, strings.Join(pageParts, "\n"))
		}
	}
	return strings.Join(parts, "\n\n")
}

var (
	pdfTextObjectPattern = regexp.MustCompile(`(?s)BT\s*(.*?)\s*ET`)
	pdfTextArrayPattern  = regexp.MustCompile(`\[(?s:.*?)\]\s*TJ`)
	pdfLiteralPattern    = regexp.MustCompile(`\((?:\\.|[^\\)])*\)`)
	pdfHexPattern        = regexp.MustCompile(`<([0-9A-Fa-f\s]{4,})>`)
	cidPattern           = regexp.MustCompile(`(?i)\(?cid\s*:\s*\d+\s*\)?`)
	headingPattern       = regexp.MustCompile(`^([0-9]+(\.[0-9]+)*[、. ]+|第[一二三四五六七八九十百千万0-9]+[章节条])`)
	captionPattern       = regexp.MustCompile(`^(图|表|Figure|Table)\s*[-0-9一二三四五六七八九十.：: ]+`)
	scrapPattern         = regexp.MustCompile(`(?i)^(page\s+\d+\s*(of\s+\d+)?|第\s*\d+\s*页|版权所有|copyright\b)`)
	pageMarkerPattern    = regexp.MustCompile(`(?i)^page\s+(\d+)$`)
	zhPageMarkerPattern  = regexp.MustCompile(`^第\s*(\d+)\s*页$`)
	listPattern          = regexp.MustCompile(`^([-*•]|[0-9]+[.)、])\s+`)
)

func extractPDFLiteralText(data []byte) string {
	source := string(data)
	objects := pdfTextObjectPattern.FindAllStringSubmatch(source, -1)
	if len(objects) > 0 {
		parts := make([]string, 0, len(objects))
		for _, object := range objects {
			if len(object) != 2 {
				continue
			}
			if decoded := extractPDFTextOperands(object[1]); decoded != "" {
				parts = append(parts, decoded)
			}
		}
		return strings.Join(parts, "\f")
	}
	return extractPDFTextOperands(source)
}

func extractPDFTextOperands(source string) string {
	for _, arrayExpr := range pdfTextArrayPattern.FindAllString(source, -1) {
		arrayExpr = strings.TrimSpace(strings.TrimSuffix(arrayExpr, "TJ"))
		arrayExpr = strings.TrimSpace(strings.TrimPrefix(arrayExpr, "["))
		arrayExpr = strings.TrimSpace(strings.TrimSuffix(arrayExpr, "]"))
		if arrayExpr == "" {
			continue
		}
		parts := tokenizePDFArray(arrayExpr)
		for _, part := range parts {
			if len(part) >= 2 && strings.HasPrefix(part, "(") && strings.HasSuffix(part, ")") {
				source += "\n" + part
				continue
			}
			if len(part) >= 2 && strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">") {
				source += "\n" + part
			}
		}
	}
	parts := make([]string, 0)
	for _, match := range pdfLiteralPattern.FindAllString(source, -1) {
		if decoded := decodePDFLiteral(match[1 : len(match)-1]); decoded != "" {
			parts = append(parts, decoded)
		}
	}
	for _, match := range pdfHexPattern.FindAllStringSubmatch(source, -1) {
		if len(match) != 2 {
			continue
		}
		if decoded := decodePDFHex(match[1]); decoded != "" {
			parts = append(parts, decoded)
		}
	}
	return strings.Join(parts, "\n")
}

func tokenizePDFArray(expr string) []string {
	parts := make([]string, 0)
	for i := 0; i < len(expr); {
		for i < len(expr) && unicode.IsSpace(rune(expr[i])) {
			i++
		}
		if i >= len(expr) {
			break
		}
		switch expr[i] {
		case '(':
			start := i
			depth := 1
			i++
			for i < len(expr) && depth > 0 {
				if expr[i] == '\\' {
					i += 2
					continue
				}
				if expr[i] == '(' {
					depth++
				} else if expr[i] == ')' {
					depth--
				}
				i++
			}
			parts = append(parts, expr[start:i])
		case '<':
			start := i
			i++
			for i < len(expr) && expr[i] != '>' {
				i++
			}
			if i < len(expr) {
				i++
			}
			parts = append(parts, expr[start:i])
		default:
			start := i
			for i < len(expr) && !unicode.IsSpace(rune(expr[i])) {
				i++
			}
			parts = append(parts, expr[start:i])
		}
	}
	return parts
}

func decodePDFLiteral(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch != '\\' || i == len(value)-1 {
			out.WriteByte(ch)
			continue
		}
		i++
		switch value[i] {
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case '(', ')', '\\':
			out.WriteByte(value[i])
		case '\n', '\r':
			// PDF line continuation: a backslash followed by EOL is ignored.
			if value[i] == '\r' && i+1 < len(value) && value[i+1] == '\n' {
				i++
			}
		default:
			if value[i] >= '0' && value[i] <= '7' {
				end := i + 1
				for end < len(value) && end < i+3 && value[end] >= '0' && value[end] <= '7' {
					end++
				}
				if n, err := strconv.ParseUint(value[i:end], 8, 8); err == nil {
					out.WriteByte(byte(n))
					i = end - 1
					continue
				}
			}
			out.WriteByte(value[i])
		}
	}
	return normalizeExtractedText(out.String())
}

func decodePDFHex(value string) string {
	value = strings.Join(strings.Fields(value), "")
	if len(value) < 2 {
		return ""
	}
	if len(value)%2 != 0 {
		value += "0"
	}
	bytesValue := make([]byte, 0, len(value)/2)
	for i := 0; i < len(value); i += 2 {
		n, err := strconv.ParseUint(value[i:i+2], 16, 8)
		if err != nil {
			return ""
		}
		bytesValue = append(bytesValue, byte(n))
	}
	if len(bytesValue) >= 2 && bytesValue[0] == 0xFE && bytesValue[1] == 0xFF {
		return normalizeExtractedText(decodeUTF16BE(bytesValue[2:]))
	}
	if len(bytesValue)%2 == 0 && looksUTF16BE(bytesValue) {
		return normalizeExtractedText(decodeUTF16BE(bytesValue))
	}
	return normalizeExtractedText(string(bytesValue))
}

func decodeUTF16BE(data []byte) string {
	runes := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		runes = append(runes, uint16(data[i])<<8|uint16(data[i+1]))
	}
	return string(utf16.Decode(runes))
}

func looksUTF16BE(data []byte) bool {
	if len(data) < 4 || len(data)%2 != 0 {
		return false
	}
	zeros := 0
	for i := 0; i < len(data); i += 2 {
		if data[i] == 0 {
			zeros++
		}
	}
	return float64(zeros)/float64(len(data)/2) >= 0.6
}

func normalizeExtractedText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	clean := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			clean = append(clean, line)
		}
	}
	return strings.TrimSpace(strings.Join(clean, "\n"))
}

func splitLoosePageMarkers(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return text
	}
	converted := make([]string, 0, len(lines))
	lastPageNo := 1
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if pageNo, ok := parseLoosePageMarker(line); ok && pageNo > lastPageNo && pageNo == lastPageNo+1 {
			if len(converted) > 0 && converted[len(converted)-1] != "\f" {
				converted = append(converted, "\f")
			}
			lastPageNo = pageNo
			converted = append(converted, raw)
			continue
		}
		converted = append(converted, raw)
	}
	return strings.Join(converted, "\n")
}

func parseLoosePageMarker(line string) (int, bool) {
	line = strings.TrimSpace(line)
	if match := pageMarkerPattern.FindStringSubmatch(line); len(match) == 2 {
		pageNo, err := strconv.Atoi(match[1])
		if err == nil && pageNo > 0 {
			return pageNo, true
		}
	}
	if match := zhPageMarkerPattern.FindStringSubmatch(line); len(match) == 2 {
		pageNo, err := strconv.Atoi(match[1])
		if err == nil && pageNo > 0 {
			return pageNo, true
		}
	}
	return 0, false
}

func needsOCR(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	if cidPattern.MatchString(text) {
		return true
	}
	nonSpace := 0
	garbled := 0
	letters := 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		nonSpace++
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			letters++
		}
		if r == '\ufffd' || unicode.IsControl(r) || (r >= 0xE000 && r <= 0xF8FF) || (r >= 0xF0000 && r <= 0x10FFFF) {
			garbled++
		}
	}
	if nonSpace == 0 {
		return true
	}
	if float64(garbled)/float64(nonSpace) >= 0.3 {
		return true
	}
	return nonSpace >= 20 && float64(letters)/float64(nonSpace) < 0.15
}

func faqMetadataValues(line string, prefixes ...string) ([]string, bool) {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, prefix := range prefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		value := strings.TrimSpace(line[len(prefix):])
		if value == "" {
			return nil, true
		}
		parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == '；' })
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out, true
	}
	return nil, false
}

func isQuestionLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "Q:") || strings.HasPrefix(line, "问:") || strings.HasSuffix(line, "?") || strings.HasSuffix(line, "？")
}

func trimQuestionPrefix(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "Q:")
	line = strings.TrimPrefix(line, "问:")
	return strings.TrimSpace(line)
}

func trimAnswerPrefix(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "A:")
	line = strings.TrimPrefix(line, "答:")
	return strings.TrimSpace(line)
}

func max(a, b int) int {
	return int(math.Max(float64(a), float64(b)))
}

func utf8RuneCount(text string) int {
	count := 0
	for range text {
		count++
	}
	return count
}
