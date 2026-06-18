package chunker

import (
	"strings"
	"testing"
)

func TestFixedTokenChunkerSplitsWithOverlap(t *testing.T) {
	c := NewFixedTokenChunker()
	got := c.Chunk("abcdef", 4, 2)

	if len(got) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(got))
	}
	if got[0].Content != "abcdef" {
		t.Fatalf("content = %q, want abcdef", got[0].Content)
	}
}

func TestFixedTokenChunkerRespectsEstimatedTokenBudget(t *testing.T) {
	c := NewFixedTokenChunker()
	cases := []struct {
		name string
		text string
	}{
		{name: "chinese", text: "一二三四五六七八九十"},
		{name: "english", text: "abcdefghijklmnopqrst"},
		{name: "mixed", text: "一二abc三四def五六ghi"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Chunk(tc.text, 4, 1)
			if len(got) < 2 {
				t.Fatalf("len(chunks) = %d, want split chunks", len(got))
			}
			for _, chunk := range got {
				if chunk.TokenCount > 4 {
					t.Fatalf("TokenCount = %d, want <= 4, chunk=%q", chunk.TokenCount, chunk.Content)
				}
			}
		})
	}
}

func TestFixedTokenChunkerInfersMarkdownSection(t *testing.T) {
	c := NewFixedTokenChunker()
	got := c.Chunk("# Intro\ncontent", 100, 0)

	if len(got) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(got))
	}
	if got[0].SectionTitle != "Intro" {
		t.Fatalf("SectionTitle = %q, want Intro", got[0].SectionTitle)
	}
}

func TestFixedTokenChunkerIgnoresBlankText(t *testing.T) {
	c := NewFixedTokenChunker()
	if got := c.Chunk(strings.Repeat(" ", 10), 4, 1); len(got) != 0 {
		t.Fatalf("len(chunks) = %d, want 0", len(got))
	}
}
