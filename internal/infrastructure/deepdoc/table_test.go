package deepdoc

import (
	"testing"
)

func TestTableBuilderDetectTables(t *testing.T) {
	builder := NewTableBuilder()
	boxes := []TextBox{
		{Text: "| Name | Age | City |", Rect: NewRect(0, 0, 200, 10), PageNo: 1},
		{Text: "| Alice | 30 | NY |", Rect: NewRect(0, 12, 200, 10), PageNo: 1},
		{Text: "| Bob | 25 | SF |", Rect: NewRect(0, 24, 200, 10), PageNo: 1},
	}
	tables := builder.DetectTables(boxes)
	if len(tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(tables))
	}
	if tables[0].NumRows != 3 {
		t.Fatalf("NumRows = %d, want 3", tables[0].NumRows)
	}
	if tables[0].NumCols != 3 {
		t.Fatalf("NumCols = %d, want 3", tables[0].NumCols)
	}
}

func TestTableBuilderNoTable(t *testing.T) {
	builder := NewTableBuilder()
	boxes := []TextBox{
		{Text: "Just some text", Rect: NewRect(0, 0, 100, 10), PageNo: 1},
	}
	tables := builder.DetectTables(boxes)
	if len(tables) != 0 {
		t.Fatalf("tables = %d, want 0", len(tables))
	}
}

func TestSplitTableRow(t *testing.T) {
	cols := splitTableRow("| a | b | c |")
	if len(cols) != 3 {
		t.Fatalf("cols = %d, want 3", len(cols))
	}
}

func TestSplitTableRowNoPipe(t *testing.T) {
	cols := splitTableRow("just text")
	if len(cols) != 1 {
		t.Fatalf("cols = %d, want 1", len(cols))
	}
}

func TestSimpleRowsToHTML(t *testing.T) {
	cells := []TableCell{
		{Row: 0, Col: 0, Text: "A"},
		{Row: 0, Col: 1, Text: "B"},
		{Row: 1, Col: 0, Text: "C"},
		{Row: 1, Col: 1, Text: "D"},
	}
	html := SimpleRowsToHTML(cells, 2, 2)
	if html == "" {
		t.Fatal("HTML is empty")
	}
	if !containsString(html, "<table>") || !containsString(html, "</table>") {
		t.Fatalf("HTML missing table tags: %s", html)
	}
	if !containsString(html, "A") || !containsString(html, "B") || !containsString(html, "C") || !containsString(html, "D") {
		t.Fatalf("HTML missing cell content: %s", html)
	}
}

func TestEscapeHTML(t *testing.T) {
	if escaped := escapeHTML("<b>test</b>"); escaped != "&lt;b&gt;test&lt;/b&gt;" {
		t.Fatalf("escaped = %q", escaped)
	}
	if escaped := escapeHTML("a&b"); escaped != "a&amp;b" {
		t.Fatalf("escaped = %q", escaped)
	}
}

func TestBuildTableFromEmptyRows(t *testing.T) {
	builder := NewTableBuilder()
	table := builder.buildTable(nil, 1)
	if table != nil {
		t.Fatal("buildTable with nil should return nil")
	}
}

func TestCellRangeKey(t *testing.T) {
	if key := CellRangeKey(1, 2); key != "1_2" {
		t.Fatalf("key = %q, want 1_2", key)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
