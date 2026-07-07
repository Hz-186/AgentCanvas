package deepdoc

import (
	"sort"
	"strings"
)

type TextChar struct {
	Char rune
	Rect Rect
	W    float64
	H    float64
}

type TextBox struct {
	ID      string
	Text    string
	Rect    Rect
	PageNo  int
	Type    string
}

type Section struct {
	ID       string
	Type     string
	Text     string
	PageNo   int
	BBox     *Rect
	Children []Section
	Metadata map[string]any
}

func (s *Section) AddChild(child Section) {
	s.Children = append(s.Children, child)
}

func GroupCharsToLines(chars []TextChar, pageHeight float64, lineOverlapRatio float64) [][]TextChar {
	if lineOverlapRatio <= 0 {
		lineOverlapRatio = 0.3
	}
	if pageHeight <= 0 {
		pageHeight = 800
	}
	sorted := make([]TextChar, len(chars))
	copy(sorted, chars)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Rect.Top != sorted[j].Rect.Top {
			return sorted[i].Rect.Top < sorted[j].Rect.Top
		}
		return sorted[i].Rect.Left < sorted[j].Rect.Left
	})
	lines := make([][]TextChar, 0)
	for _, ch := range sorted {
		placed := false
		for i := range lines {
			ref := lines[i][0]
			overlap := ref.Rect.OverlapRatioY(ch.Rect)
			if overlap >= lineOverlapRatio {
				lines[i] = append(lines[i], ch)
				placed = true
				break
			}
		}
		if !placed {
			lines = append(lines, []TextChar{ch})
		}
	}
	return lines
}

func LineToTextBox(chars []TextChar, pageNo int) *TextBox {
	if len(chars) == 0 {
		return nil
	}
	rect := chars[0].Rect
	var text strings.Builder
	var prevChar rune
	for i, ch := range chars {
		if i > 0 {
			gap := ch.Rect.Left - chars[i-1].Rect.Right
			if gap > chars[i-1].W*0.5 && prevChar != ' ' {
				text.WriteByte(' ')
			}
		}
		text.WriteRune(ch.Char)
		prevChar = ch.Char
		rect = rect.Union(ch.Rect)
	}
	return &TextBox{
		Text:   text.String(),
		Rect:   rect,
		PageNo: pageNo,
		Type:   "text",
	}
}

func CharsToBoxes(chars []TextChar, pageNo int, pageHeight float64) []TextBox {
	lines := GroupCharsToLines(chars, pageHeight, 0.3)
	boxes := make([]TextBox, 0, len(lines))
	for _, line := range lines {
		if box := LineToTextBox(line, pageNo); box != nil {
			boxes = append(boxes, *box)
		}
	}
	return boxes
}

func AssignColumn(boxes []TextBox) []TextBox {
	if len(boxes) <= 1 {
		for i := range boxes {
			boxes[i].Type = assignBoxType(boxes[i], 0)
		}
		return boxes
	}
	centers := make([]float64, len(boxes))
	for i, b := range boxes {
		centers[i] = b.Rect.CenterX()
	}
	km := BestKMeans1D(centers, 3)
	if km == nil || km.K <= 1 {
		for i := range boxes {
			boxes[i].Type = assignBoxType(boxes[i], 0)
		}
		return boxes
	}
	for i := range boxes {
		boxes[i].Type = assignBoxType(boxes[i], km.Labels[i])
	}
	return boxes
}

func assignBoxType(box TextBox, column int) string {
	if box.Type != "text" {
		return box.Type
	}
	if column > 0 {
		return "text_column2"
	}
	return "text"
}

func TextMerge(boxes []TextBox, maxXGapRatio float64) []TextBox {
	if len(boxes) <= 1 {
		return boxes
	}
	if maxXGapRatio <= 0 {
		maxXGapRatio = 0.5
	}
	sorted := make([]TextBox, len(boxes))
	copy(sorted, boxes)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].PageNo != sorted[j].PageNo {
			return sorted[i].PageNo < sorted[j].PageNo
		}
		if sorted[i].Rect.Top != sorted[j].Rect.Top {
			return sorted[i].Rect.Top < sorted[j].Rect.Top
		}
		return sorted[i].Rect.Left < sorted[j].Rect.Left
	})
	merged := make([]TextBox, 0, len(sorted))
	i := 0
	for i < len(sorted) {
		current := sorted[i]
		j := i + 1
		for j < len(sorted) {
			if current.PageNo != sorted[j].PageNo {
				break
			}
			if sorted[j].Rect.Top-current.Rect.Bottom > current.Rect.Height()*0.5 {
				break
			}
			xGap := sorted[j].Rect.Left - current.Rect.Right
			maxGap := current.Rect.Width() * maxXGapRatio
			if xGap > maxGap && xGap > 5 {
				break
			}
			if current.Text != "" && sorted[j].Text != "" {
				current.Text += " " + sorted[j].Text
			} else if sorted[j].Text != "" {
				current.Text += sorted[j].Text
			}
			current.Rect = current.Rect.Union(sorted[j].Rect)
			j++
		}
		merged = append(merged, current)
		i = j
	}
	return merged
}

func NaiveVerticalMerge(boxes []TextBox, maxYGapRatio float64, columnThreshold float64) []TextBox {
	if len(boxes) <= 1 {
		return boxes
	}
	if maxYGapRatio <= 0 {
		maxYGapRatio = 0.3
	}
	if columnThreshold <= 0 {
		columnThreshold = 0.5
	}
	sorted := make([]TextBox, len(boxes))
	copy(sorted, boxes)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].PageNo != sorted[j].PageNo {
			return sorted[i].PageNo < sorted[j].PageNo
		}
		return sorted[i].Rect.Top < sorted[j].Rect.Top
	})
	columns := groupByColumn(sorted, columnThreshold)
	merged := make([]TextBox, 0, len(sorted))
	for _, col := range columns {
		if len(col) == 0 {
			continue
		}
		current := col[0]
		for i := 1; i < len(col); i++ {
			gap := col[i].Rect.Top - current.Rect.Bottom
			maxGap := current.Rect.Height() * maxYGapRatio
			if gap > maxGap && gap > 3 {
				merged = append(merged, current)
				current = col[i]
				continue
			}
			if current.Text != "" && col[i].Text != "" {
				current.Text += "\n" + col[i].Text
			} else if col[i].Text != "" {
				current.Text += col[i].Text
			}
			current.Rect = current.Rect.Union(col[i].Rect)
		}
		merged = append(merged, current)
	}
	return merged
}

func groupByColumn(boxes []TextBox, threshold float64) [][]TextBox {
	if len(boxes) == 0 {
		return nil
	}
	centers := make([]float64, len(boxes))
	for i, b := range boxes {
		centers[i] = b.Rect.CenterX()
	}
	km := BestKMeans1D(centers, 3)
	if km == nil || km.K <= 1 {
		return [][]TextBox{boxes}
	}
	cols := make([][]TextBox, km.K)
	for i, b := range boxes {
		label := km.Labels[i]
		cols[label] = append(cols[label], b)
	}
	for i := range cols {
		sort.SliceStable(cols[i], func(a, b int) bool {
			return cols[i][a].Rect.Top < cols[i][b].Rect.Top
		})
	}
	return cols
}

func MergeSameBullet(boxes []TextBox) []TextBox {
	if len(boxes) <= 1 {
		return boxes
	}
	merged := make([]TextBox, 0, len(boxes))
	for i := 0; i < len(boxes); {
		current := boxes[i]
		j := i + 1
		if j < len(boxes) && (strings.HasPrefix(strings.TrimSpace(current.Text), "-") ||
			strings.HasPrefix(strings.TrimSpace(current.Text), "•")) {
			for j < len(boxes) && boxes[j].Rect.Left > current.Rect.Left &&
				boxes[j].Rect.Left-current.Rect.Right < current.Rect.Width()*0.3 {
				if current.Text != "" && boxes[j].Text != "" {
					current.Text += "\n" + boxes[j].Text
				}
				current.Rect = current.Rect.Union(boxes[j].Rect)
				j++
			}
		}
		merged = append(merged, current)
		i = j
	}
	return merged
}

func FinalReadingOrderMerge(boxes []TextBox) []TextBox {
	sorted := make([]TextBox, len(boxes))
	copy(sorted, boxes)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].PageNo != sorted[j].PageNo {
			return sorted[i].PageNo < sorted[j].PageNo
		}
		if isMultiColumn(sorted) {
			colI := columnIndex(sorted, i)
			colJ := columnIndex(sorted, j)
			if colI != colJ {
				return colI < colJ
			}
		}
		if sorted[i].Rect.Top != sorted[j].Rect.Top {
			return sorted[i].Rect.Top < sorted[j].Rect.Top
		}
		return sorted[i].Rect.Left < sorted[j].Rect.Left
	})
	return sorted
}

func isMultiColumn(boxes []TextBox) bool {
	if len(boxes) < 4 {
		return false
	}
	centers := make([]float64, len(boxes))
	for i, b := range boxes {
		centers[i] = b.Rect.CenterX()
	}
	km := BestKMeans1D(centers, 3)
	return km != nil && km.K > 1
}

func columnIndex(boxes []TextBox, idx int) int {
	if idx >= len(boxes) {
		return 0
	}
	centers := make([]float64, len(boxes))
	for i, b := range boxes {
		centers[i] = b.Rect.CenterX()
	}
	km := BestKMeans1D(centers, 3)
	if km == nil || km.K <= 1 {
		return 0
	}
	return km.Labels[idx]
}

type LayoutBlock struct {
	Type     string
	Text     string
	PageNo   int
	BBox     Rect
	Metadata map[string]any
}

func BoxesToSections(boxes []TextBox) []Section {
	sections := make([]Section, 0, len(boxes))
	for i, box := range boxes {
		sectionType := classifyBoxType(box)
		metadata := map[string]any{
			"layout_type": sectionType,
			"box_index":   i + 1,
		}
		section := Section{
			ID:       sectionID(i + 1),
			Type:     sectionType,
			Text:     box.Text,
			PageNo:   box.PageNo,
			BBox:     &box.Rect,
			Metadata: metadata,
		}
		sections = append(sections, section)
	}
	return sectionsToHierarchy(sections)
}

func sectionsToHierarchy(sections []Section) []Section {
	if len(sections) <= 1 {
		return sections
	}
	root := make([]Section, 0, len(sections))
	for i := 0; i < len(sections); i++ {
		if sections[i].Type == "heading" || sections[i].Type == "title" {
			section := sections[i]
			j := i + 1
			for j < len(sections) && sections[j].Type != "heading" && sections[j].Type != "title" {
				section.AddChild(sections[j])
				j++
			}
			root = append(root, section)
			i = j - 1
			continue
		}
		root = append(root, sections[i])
	}
	return root
}

func classifyBoxType(box TextBox) string {
	trimmed := strings.TrimSpace(box.Text)
	if trimmed == "" {
		return "text"
	}
	if strings.HasPrefix(trimmed, "#") || headingPattern.MatchString(trimmed) {
		return "heading"
	}
	if captionPattern.MatchString(trimmed) {
		return "caption"
	}
	if strings.Contains(trimmed, "|") && strings.Count(trimmed, "|") >= 2 {
		return "table"
	}
	if box.Type == "text_column2" {
		return "text"
	}
	return "text"
}

func sectionID(index int) string {
	return "s" + formatInt(index)
}

func formatInt(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 10)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	if neg {
		digits = append(digits, '-')
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
