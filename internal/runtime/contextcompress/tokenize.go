package contextcompress

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func tokenizeText(text string) []string {
	text = strings.ToLower(text)
	tokens := make([]string, 0, len(text)/4)
	var word strings.Builder
	flushWord := func() {
		if word.Len() == 0 {
			return
		}
		token := word.String()
		if !isStopToken(token) {
			tokens = append(tokens, token)
		}
		word.Reset()
	}
	for _, r := range text {
		switch {
		case isCJK(r):
			flushWord()
			tokens = append(tokens, string(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-':
			word.WriteRune(r)
		default:
			flushWord()
		}
	}
	flushWord()
	return appendCJKShingles(tokens)
}

func appendCJKShingles(tokens []string) []string {
	if len(tokens) < 2 {
		return tokens
	}
	result := make([]string, 0, len(tokens)*2)
	result = append(result, tokens...)
	for i := 0; i+1 < len(tokens); i++ {
		if isSingleCJKToken(tokens[i]) && isSingleCJKToken(tokens[i+1]) {
			result = append(result, tokens[i]+tokens[i+1])
		}
	}
	for i := 0; i+2 < len(tokens); i++ {
		if isSingleCJKToken(tokens[i]) && isSingleCJKToken(tokens[i+1]) && isSingleCJKToken(tokens[i+2]) {
			result = append(result, tokens[i]+tokens[i+1]+tokens[i+2])
		}
	}
	return result
}

func splitFragments(item Item) []Fragment {
	content := strings.TrimSpace(item.Content)
	if content == "" {
		return nil
	}
	parts := splitSentences(content)
	fragments := make([]Fragment, 0, len(parts))
	for i, part := range parts {
		part = strings.Join(strings.Fields(part), " ")
		if part == "" {
			continue
		}
		fragments = append(fragments, Fragment{ItemID: item.ID, Index: i, Content: part, Tokens: estimateTokens(part), Turn: item.Turn})
	}
	return fragments
}

func splitSentences(content string) []string {
	lines := strings.Split(content, "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "```") {
			parts = append(parts, line)
			continue
		}
		start := 0
		for pos, r := range line {
			if isSentenceBoundary(r) {
				end := pos + utf8.RuneLen(r)
				parts = append(parts, strings.TrimSpace(line[start:end]))
				start = end
			}
		}
		if start < len(line) {
			parts = append(parts, strings.TrimSpace(line[start:]))
		}
	}
	return parts
}

func isSentenceBoundary(r rune) bool {
	switch r {
	case '.', '!', '?', ';', '。', '！', '？', '；':
		return true
	default:
		return false
	}
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) || (r >= 0x3040 && r <= 0x30FF) || (r >= 0xAC00 && r <= 0xD7AF)
}

func isSingleCJKToken(token string) bool {
	r, size := utf8.DecodeRuneInString(token)
	return size == len(token) && isCJK(r)
}

func isStopToken(token string) bool {
	switch token {
	case "a", "an", "the", "and", "or", "to", "of", "in", "on", "for", "with", "is", "are", "was", "were", "be", "been", "this", "that", "it":
		return true
	default:
		return false
	}
}

func tokenCounts(tokens []string) map[string]float64 {
	counts := make(map[string]float64, len(tokens))
	for _, token := range tokens {
		counts[token]++
	}
	return counts
}
