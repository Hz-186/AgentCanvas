package deepdoc

import (
	"testing"
)

func TestGroupCharsToLines(t *testing.T) {
	chars := []TextChar{
		{Char: 'H', Rect: NewRect(0, 0, 5, 10), W: 5, H: 10},
		{Char: 'e', Rect: NewRect(5, 0, 5, 10), W: 5, H: 10},
		{Char: 'W', Rect: NewRect(0, 15, 5, 10), W: 5, H: 10},
		{Char: 'o', Rect: NewRect(5, 15, 5, 10), W: 5, H: 10},
	}
	lines := GroupCharsToLines(chars, 100, 0.3)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	if len(lines[0]) != 2 || len(lines[1]) != 2 {
		t.Fatalf("line lengths = %d, %d, want 2, 2", len(lines[0]), len(lines[1]))
	}
}

func TestLineToTextBox(t *testing.T) {
	chars := []TextChar{
		{Char: 'H', Rect: NewRect(0, 0, 5, 10), W: 5, H: 10},
		{Char: 'i', Rect: NewRect(5, 0, 5, 10), W: 5, H: 10},
	}
	box := LineToTextBox(chars, 1)
	if box == nil {
		t.Fatal("LineToTextBox returned nil")
	}
	if box.Text != "Hi" {
		t.Fatalf("Text = %q, want Hi", box.Text)
	}
	if box.Rect.Right != 10 {
		t.Fatalf("box right = %f, want 10", box.Rect.Right)
	}
}

func TestCharsToBoxes(t *testing.T) {
	chars := []TextChar{
		{Char: 'A', Rect: NewRect(0, 0, 5, 10), W: 5, H: 10},
		{Char: 'B', Rect: NewRect(0, 15, 5, 10), W: 5, H: 10},
	}
	boxes := CharsToBoxes(chars, 1, 100)
	if len(boxes) != 2 {
		t.Fatalf("boxes = %d, want 2", len(boxes))
	}
}

func TestAssignColumn(t *testing.T) {
	boxes := []TextBox{
		{Text: "Left1", Rect: NewRect(10, 0, 50, 10), PageNo: 1},
		{Text: "Left2", Rect: NewRect(10, 15, 50, 10), PageNo: 1},
		{Text: "Right1", Rect: NewRect(200, 0, 50, 10), PageNo: 1},
		{Text: "Right2", Rect: NewRect(200, 15, 50, 10), PageNo: 1},
	}
	result := AssignColumn(boxes)
	if len(result) != 4 {
		t.Fatalf("result = %d, want 4", len(result))
	}
}

func TestTextMerge(t *testing.T) {
	boxes := []TextBox{
		{Text: "Hello", Rect: NewRect(0, 0, 30, 10), PageNo: 1},
		{Text: "World", Rect: NewRect(31, 0, 30, 10), PageNo: 1},
	}
	merged := TextMerge(boxes, 0.5)
	if len(merged) != 1 {
		t.Fatalf("merged = %d, want 1", len(merged))
	}
	if merged[0].Text != "Hello World" {
		t.Fatalf("merged text = %q, want 'Hello World'", merged[0].Text)
	}
}

func TestTextMergeSeparateLines(t *testing.T) {
	boxes := []TextBox{
		{Text: "Line1", Rect: NewRect(0, 0, 30, 10), PageNo: 1},
		{Text: "Line2", Rect: NewRect(0, 30, 30, 10), PageNo: 1},
	}
	merged := TextMerge(boxes, 0.5)
	if len(merged) != 2 {
		t.Fatalf("merged = %d, want 2 (separate vertical lines)", len(merged))
	}
}

func TestNaiveVerticalMerge(t *testing.T) {
	boxes := []TextBox{
		{Text: "Para1", Rect: NewRect(0, 0, 100, 10), PageNo: 1, Type: "text"},
		{Text: "Para2", Rect: NewRect(0, 12, 100, 10), PageNo: 1, Type: "text"},
	}
	merged := NaiveVerticalMerge(boxes, 0.5, 0.5)
	if len(merged) != 1 {
		t.Fatalf("merged = %d, want 1", len(merged))
	}
	if merged[0].Text != "Para1\nPara2" {
		t.Fatalf("merged text = %q", merged[0].Text)
	}
}

func TestNaiveVerticalMergeSeparation(t *testing.T) {
	boxes := []TextBox{
		{Text: "Para1", Rect: NewRect(0, 0, 100, 10), PageNo: 1, Type: "text"},
		{Text: "Para2", Rect: NewRect(0, 100, 100, 10), PageNo: 1, Type: "text"},
	}
	merged := NaiveVerticalMerge(boxes, 0.3, 0.5)
	if len(merged) != 2 {
		t.Fatalf("merged = %d, want 2 (large gap)", len(merged))
	}
}

func TestFinalReadingOrderMerge(t *testing.T) {
	boxes := []TextBox{
		{Text: "B", Rect: NewRect(50, 0, 10, 10), PageNo: 1},
		{Text: "A", Rect: NewRect(0, 0, 10, 10), PageNo: 1},
	}
	ordered := FinalReadingOrderMerge(boxes)
	if len(ordered) != 2 || ordered[0].Text != "A" || ordered[1].Text != "B" {
		t.Fatalf("ordered = %+v, want A, B", ordered)
	}
}

func TestMergeSameBullet(t *testing.T) {
	boxes := []TextBox{
		{Text: "- item1", Rect: NewRect(10, 0, 50, 10), PageNo: 1},
		{Text: "  continued", Rect: NewRect(20, 11, 50, 10), PageNo: 1},
	}
	merged := MergeSameBullet(boxes)
	if len(merged) != 1 {
		t.Fatalf("merged = %d, want 1", len(merged))
	}
}

func TestBoxesToSections(t *testing.T) {
	boxes := []TextBox{
		{Text: "# Introduction", Rect: NewRect(0, 0, 100, 10), PageNo: 1, Type: "heading"},
		{Text: "Some content", Rect: NewRect(0, 15, 100, 10), PageNo: 1, Type: "text"},
		{Text: "| col1 | col2 |", Rect: NewRect(0, 30, 100, 10), PageNo: 1, Type: "table"},
	}
	sections := BoxesToSections(boxes)
	if len(sections) < 1 {
		t.Fatalf("sections = %d, want >= 1", len(sections))
	}
}

func TestGarbledText(t *testing.T) {
	if !isGarbledText("") {
		t.Fatal("empty text should be garbled")
	}
	if isGarbledText("Hello World") {
		t.Fatal("plain text should not be garbled")
	}
	if !isGarbledText(string([]rune{'\ufffd', '\ufffd', '\ufffd'})) {
		t.Fatal("replacement chars should be garbled")
	}
}

func TestIsLikelyEnglish(t *testing.T) {
	if !isLikelyEnglish("Hello World") {
		t.Fatal("ASCII text should be English")
	}
	if isLikelyEnglish("中文测试") {
		t.Fatal("Chinese text should not be English")
	}
}
