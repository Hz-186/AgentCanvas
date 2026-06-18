package chunker

import (
	"strings"
	"testing"
)

func TestFixedTokenChunkerSplitsWithOverlap(t *testing.T) {
	c := NewFixedTokenChunker()
	got := c.Chunk("abcdef", 4, 2)

	if len(got) != 2 {
		t.Fatalf("len(chunks) = %d, want 2", len(got))
	}
	if got[0].Content != "abcd" || got[1].Content != "cdef" {
		t.Fatalf("chunks = %#v, want overlapping chunks", got)
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
