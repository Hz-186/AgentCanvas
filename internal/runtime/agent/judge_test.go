package agent

import (
	"testing"
)

func TestScoreToolsExact(t *testing.T) {
	score := scoreTools([]string{"search_knowledge", "call_workflow"}, []string{"search_knowledge", "call_workflow"})
	if score != 1.0 {
		t.Fatalf("expected 1.0, got %.2f", score)
	}
}

func TestScoreToolsPartial(t *testing.T) {
	score := scoreTools([]string{"search_knowledge", "write_memory"}, []string{"search_knowledge"})
	if score != 0.5 {
		t.Fatalf("expected 0.5, got %.2f", score)
	}
}

func TestScoreToolsNone(t *testing.T) {
	score := scoreTools([]string{"search_knowledge"}, []string{})
	if score != 0.0 {
		t.Fatalf("expected 0.0, got %.2f", score)
	}
}

func TestScoreToolsNoExpected(t *testing.T) {
	score := scoreTools([]string{}, []string{"tool_a"})
	if score != 1.0 {
		t.Fatalf("expected 1.0 for no expected tools, got %.2f", score)
	}
}

func TestScoreContentContains(t *testing.T) {
	score := scoreContent("Hello World", "This is Hello World indeed")
	if score != 1.0 {
		t.Fatalf("expected 1.0, got %.2f", score)
	}
}

func TestScoreContentPartial(t *testing.T) {
	score := scoreContent("Hello", "Goodbye")
	if score == 1.0 {
		t.Fatalf("expected less than 1.0, got %.2f", score)
	}
}

func TestScoreContentEmpty(t *testing.T) {
	score := scoreContent("", "anything")
	if score != 1.0 {
		t.Fatalf("expected 1.0 for empty expected, got %.2f", score)
	}
}

func TestCountWordOverlap(t *testing.T) {
	count := countWordOverlap("hello world agent", "hello agent canvas")
	if count < 2 {
		t.Fatalf("expected at least 2 overlapping words, got %d", count)
	}
}

func TestCountWordOverlapShortWordsIgnored(t *testing.T) {
	count := countWordOverlap("a b c hello", "a hello world")
	if count != 1 {
		t.Fatalf("expected 1 overlapping word (hello), got %d", count)
	}
}

func TestTruncate(t *testing.T) {
	result := truncate("hello world", 5)
	if result != "hello..." {
		t.Fatalf("expected 'hello...', got %s", result)
	}
	result = truncate("hi", 10)
	if result != "hi" {
		t.Fatalf("expected 'hi', got %s", result)
	}
}
