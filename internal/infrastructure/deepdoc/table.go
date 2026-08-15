package deepdoc

import (
	"fmt"
	"strings"
)

type TableCell struct {
	Row     int
	Col     int
	RowSpan int
	ColSpan int
	Text    string
	BBox    *Rect
}

type Table struct {
	ID      string
	PageNo  int
	Caption string
	Cells   []TableCell
	NumRows int
	NumCols int
	BBox    *Rect
}

type TableBuilder struct {
	MinSimilarity float64
	MinRows       int
}

func NewTableBuilder() *TableBuilder {
	return &TableBuilder{
		MinSimilarity: 0.6,
		MinRows:       2,
	}
}

func (tb *TableBuilder) DetectTables(boxes []TextBox) []Table {
	tables := make([]Table, 0)
	candidates := tb.findTableCandidates(boxes)
	for i, cand := range candidates {
		table := tb.buildTable(cand, i+1)
		if table != nil {
			tables = append(tables, *table)
		}
	}
	return tables
}

func (tb *TableBuilder) findTableCandidates(boxes []TextBox) [][]TextBox {
	candidates := make([][]TextBox, 0)
	inTable := false
	current := make([]TextBox, 0)
	for _, box := range boxes {
		trimmed := strings.TrimSpace(box.Text)
		if strings.Contains(trimmed, "|") && strings.Count(trimmed, "|") >= 2 {
			if !inTable {
				current = make([]TextBox, 0)
				inTable = true
			}
			current = append(current, box)
		} else {
			if inTable && len(current) >= tb.MinRows {
				candidates = append(candidates, current)
			}
			inTable = false
			current = nil
		}
	}
	if inTable && len(current) >= tb.MinRows {
		candidates = append(candidates, current)
	}
	return candidates
}

func (tb *TableBuilder) buildTable(rows []TextBox, index int) *Table {
	if len(rows) == 0 {
		return nil
	}
	cells := make([]TableCell, 0)
	maxCols := 0
	for i, row := range rows {
		cols := splitTableRow(row.Text)
		if len(cols) > maxCols {
			maxCols = len(cols)
		}
		for j, colText := range cols {
			cells = append(cells, TableCell{
				Row:     i,
				Col:     j,
				Text:    strings.TrimSpace(colText),
				RowSpan: 1,
				ColSpan: 1,
			})
		}
	}
	rect := rows[0].Rect
	for _, row := range rows[1:] {
		rect = rect.Union(row.Rect)
	}
	return &Table{
		ID:      fmt.Sprintf("table_%d", index),
		PageNo:  rows[0].PageNo,
		Cells:   cells,
		NumRows: len(rows),
		NumCols: maxCols,
		BBox:    &rect,
	}
}

func splitTableRow(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "|")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return cleaned
}

func CellRangeKey(row, col int) string {
	return fmt.Sprintf("%d_%d", row, col)
}

func SimpleRowsToHTML(cells []TableCell, numRows, numCols int) string {
	if len(cells) == 0 {
		return ""
	}
	grid := make([][]string, numRows)
	for i := range grid {
		grid[i] = make([]string, numCols)
	}
	for _, cell := range cells {
		if cell.Row < numRows && cell.Col < numCols {
			grid[cell.Row][cell.Col] = cell.Text
		}
	}
	var html strings.Builder
	html.WriteString("<table>")
	for _, row := range grid {
		html.WriteString("<tr>")
		for _, col := range row {
			html.WriteString("<td>")
			html.WriteString(escapeHTML(col))
			html.WriteString("</td>")
		}
		html.WriteString("</tr>")
	}
	html.WriteString("</table>")
	return html.String()
}

func escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	text = strings.ReplaceAll(text, "\"", "&quot;")
	return text
}
