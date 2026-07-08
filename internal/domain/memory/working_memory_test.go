package memory

import (
	"strings"
	"testing"
)

func TestWorkingMemory_ToContextBlock_Empty(t *testing.T) {
	wm := &WorkingMemory{
		OwnerID:        100,
		ConversationID: 1,
	}
	if content := wm.ToContextBlock(); content != "" {
		t.Fatalf("expected empty content for empty working memory, got: %s", content)
	}
	if !wm.IsEmpty() {
		t.Fatal("expected IsEmpty to return true")
	}
}

func TestWorkingMemory_ToContextBlock_WithTask(t *testing.T) {
	wm := &WorkingMemory{
		OwnerID:        100,
		ConversationID: 1,
		ActiveTask: &WorkingTask{
			Goal:        "Implement user authentication",
			CurrentStep: "Design database schema",
			Progress:    "已完成 40%",
		},
	}
	content := wm.ToContextBlock()
	if content == "" {
		t.Fatal("expected non-empty content")
	}
	if !strings.Contains(content, "CURRENT TASK:") {
		t.Fatalf("expected CURRENT TASK in content, got: %s", content)
	}
	if !strings.Contains(content, "Implement user authentication") {
		t.Fatalf("expected task goal in content, got: %s", content)
	}
	if !strings.Contains(content, "Design database schema") {
		t.Fatalf("expected current step in content, got: %s", content)
	}
}

func TestWorkingMemory_ToContextBlock_WithFacts(t *testing.T) {
	wm := &WorkingMemory{
		OwnerID:        100,
		ConversationID: 1,
		RecentFacts: []WorkingFact{
			{Fact: "user prefers concise answers", Confidence: 0.9},
			{Fact: "project uses Clean Architecture", Confidence: 1.0},
			{Fact: "low confidence fact", Confidence: 0.3},
		},
	}
	content := wm.ToContextBlock()
	if content == "" {
		t.Fatal("expected non-empty content")
	}
	if !strings.Contains(content, "user prefers concise answers") {
		t.Fatalf("expected high-confidence fact, got: %s", content)
	}
	if !strings.Contains(content, "project uses Clean Architecture") {
		t.Fatalf("expected high-confidence fact, got: %s", content)
	}
	if strings.Contains(content, "low confidence fact") {
		t.Fatal("low-confidence fact should not appear")
	}
}

func TestWorkingMemory_ToContextBlock_Full(t *testing.T) {
	wm := &WorkingMemory{
		OwnerID:        100,
		ConversationID: 1,
		ActiveTask: &WorkingTask{
			Goal:        "Build a REST API",
			CurrentStep: "Implement handlers",
		},
		AttentionFocus: "error handling middleware",
		RecentFacts:    []WorkingFact{{Fact: "user is senior Go dev", Confidence: 1.0}},
		ContextSummary: "Discussed API design and chose Gin framework",
		RoundNumber:    5,
	}
	content := wm.ToContextBlock()
	if content == "" {
		t.Fatal("expected non-empty content")
	}
	if !strings.Contains(content, "CURRENT TASK") || !strings.Contains(content, "FOCUS") || !strings.Contains(content, "KEY FACTS") || !strings.Contains(content, "RECENT SUMMARY") {
		t.Fatalf("expected all sections, got: %s", content)
	}
	if wm.IsEmpty() {
		t.Fatal("expected IsEmpty to return false for populated working memory")
	}
}

func TestWorkingMemory_ToContextBlock_NoFactsIfLowConfidence(t *testing.T) {
	wm := &WorkingMemory{
		OwnerID:        100,
		ConversationID: 1,
		ActiveTask:     &WorkingTask{Goal: "test"},
		RecentFacts:    []WorkingFact{{Fact: "uncertain", Confidence: 0.5}},
	}
	content := wm.ToContextBlock()
	if strings.Contains(content, "KEY FACTS") {
		t.Fatal("should not show KEY FACTS if all are low confidence")
	}
}

func TestWorkingMemory_ToContextBlock_OnlyAttentionFocus(t *testing.T) {
	wm := &WorkingMemory{
		OwnerID:        100,
		ConversationID: 1,
		AttentionFocus: "reviewing pull request #42",
	}
	content := wm.ToContextBlock()
	if content == "" {
		t.Fatal("expected non-empty content for attention focus only")
	}
	if !strings.Contains(content, "FOCUS:") {
		t.Fatalf("expected FOCUS in content, got: %s", content)
	}
}
