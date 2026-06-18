package chat_usecase

import (
	"strings"
	"testing"

	"agentcanvas/internal/domain/retrieval"
)

func TestContextPackerSortsLimitsAndTruncates(t *testing.T) {
	packer := &ContextPacker{BudgetTokens: 4, MaxChunksPerDoc: 1}
	packed := packer.Pack(1, []retrieval.RetrievalResult{
		{ChunkID: 1, DocumentID: 10, KBID: 100, Score: 0.5, Content: "low score", DocumentName: "low.md"},
		{ChunkID: 2, DocumentID: 10, KBID: 100, Score: 0.9, Content: strings.Repeat("a", 100), DocumentName: "high.md"},
		{ChunkID: 3, DocumentID: 11, KBID: 100, Score: 0.8, Content: "second doc", DocumentName: "second.md"},
	})
	if len(packed.References) != 1 {
		t.Fatalf("expected one reference, got %d", len(packed.References))
	}
	if packed.References[0].ChunkID != 2 || packed.References[0].RefIndex != 1 {
		t.Fatalf("unexpected reference: %+v", packed.References[0])
	}
	if estimateTokens(packed.References[0].QuoteText) > 4 {
		t.Fatalf("quote exceeded budget: %q", packed.References[0].QuoteText)
	}
}

func TestPromptBuilderUsesEmptyContextInstruction(t *testing.T) {
	prompt := NewPromptBuilder().Build("问题", "")
	if !strings.Contains(prompt, "不知道") || !strings.Contains(prompt, emptyContextText) {
		t.Fatalf("prompt missing expected safeguards: %s", prompt)
	}
}
