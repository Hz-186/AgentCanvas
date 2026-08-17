package toolruntime

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type textMatch struct {
	start int
	end   int
}

type fuzzyStrategy struct {
	name        string
	approximate bool
	find        func(string, string) []textMatch
}

var hermesUnicodeMap = map[rune]string{
	'\u201c': `"`, '\u201d': `"`, '\u2018': "'", '\u2019': "'", '\u2014': "--", '\u2013': "-",
	'\u2026': "...", '\u00a0': " ", '\u2212': "-", '\u2000': " ", '\u2001': " ", '\u2002': " ",
	'\u2003': " ", '\u2004': " ", '\u2005': " ", '\u2006': " ", '\u2007': " ", '\u2008': " ",
	'\u2009': " ", '\u200a': " ", '\u202f': " ", '\u205f': " ", '\u3000': " ",
}

func fuzzyFindAndReplace(content, oldString, newString string, replaceAll bool) (string, int, string, error) {
	if oldString == "" {
		return content, 0, "", errors.New("old_string cannot be empty")
	}
	if strings.TrimSpace(oldString) == "" {
		return content, 0, "", errors.New("old_string is only whitespace; provide non-blank text to match")
	}
	if oldString == newString {
		return content, 0, "", errors.New("old_string and new_string are identical")
	}
	strategies := []fuzzyStrategy{
		{name: "exact", find: exactMatches},
		{name: "line_trimmed", find: lineTrimmedMatches},
		{name: "whitespace_normalized", find: whitespaceNormalizedMatches},
		{name: "indentation_flexible", find: indentationFlexibleMatches},
		{name: "escape_normalized", find: escapeNormalizedMatches},
		{name: "trimmed_boundary", find: trimmedBoundaryMatches},
		{name: "unicode_normalized", find: unicodeNormalizedMatches},
		{name: "block_anchor", approximate: true, find: blockAnchorMatches},
		{name: "context_aware", approximate: true, find: contextAwareMatches},
	}
	for _, strategy := range strategies {
		matches := strategy.find(content, oldString)
		if len(matches) == 0 {
			continue
		}
		if len(matches) > 1 && !replaceAll {
			return content, 0, "", fmt.Errorf("found %d matches for old_string; provide more context or use replace_all: %s", len(matches), formatMatchLocations(content, matches))
		}
		if len(matches) > 1 && replaceAll && strategy.approximate {
			return content, 0, "", fmt.Errorf("found %d approximate matches via %s; replace_all requires a precise strategy", len(matches), strategy.name)
		}
		if strategy.name != "exact" {
			if err := detectEscapeDrift(content, matches, oldString, newString); err != nil {
				return content, 0, "", err
			}
		}
		effectiveNew := maybeUnescapeReplacement(newString, content, matches)
		if strategy.name == "unicode_normalized" && len(matches) == 1 {
			effectiveNew = preserveUnicodeReplacement(content[matches[0].start:matches[0].end], oldString, effectiveNew)
		}
		return applyTextMatches(content, matches, effectiveNew, oldString, strategy.name != "exact"), len(matches), strategy.name, nil
	}
	return content, 0, "", errors.New("could not find a match for old_string in the file")
}

func exactMatches(content, pattern string) []textMatch {
	if pattern == "" {
		return nil
	}
	var matches []textMatch
	for offset := 0; offset <= len(content)-len(pattern); {
		index := strings.Index(content[offset:], pattern)
		if index < 0 {
			break
		}
		start := offset + index
		matches = append(matches, textMatch{start: start, end: start + len(pattern)})
		offset = start + len(pattern)
	}
	return matches
}

func lineTrimmedMatches(content, pattern string) []textMatch {
	return normalizedLineMatches(content, pattern, func(line string) string { return strings.TrimSpace(line) })
}

func whitespaceNormalizedMatches(content, pattern string) []textMatch {
	return normalizedLineMatches(content, pattern, collapseHorizontalWhitespace)
}

func indentationFlexibleMatches(content, pattern string) []textMatch {
	return normalizedLineMatches(content, pattern, func(line string) string { return strings.TrimLeft(line, " \t") })
}

func escapeNormalizedMatches(content, pattern string) []textMatch {
	unescaped := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\r`, "\r").Replace(pattern)
	if unescaped == pattern {
		return nil
	}
	return exactMatches(content, unescaped)
}

func trimmedBoundaryMatches(content, pattern string) []textMatch {
	patternLines := strings.Split(pattern, "\n")
	if len(patternLines) == 0 {
		return nil
	}
	patternLines[0] = strings.TrimSpace(patternLines[0])
	if len(patternLines) > 1 {
		patternLines[len(patternLines)-1] = strings.TrimSpace(patternLines[len(patternLines)-1])
	}
	contentLines := strings.Split(content, "\n")
	var matches []textMatch
	for start := 0; start+len(patternLines) <= len(contentLines); start++ {
		candidate := append([]string(nil), contentLines[start:start+len(patternLines)]...)
		candidate[0] = strings.TrimSpace(candidate[0])
		if len(candidate) > 1 {
			candidate[len(candidate)-1] = strings.TrimSpace(candidate[len(candidate)-1])
		}
		if strings.Join(candidate, "\n") == strings.Join(patternLines, "\n") {
			matches = append(matches, lineMatchSpan(content, contentLines, start, len(patternLines)))
		}
	}
	return matches
}

func unicodeNormalizedMatches(content, pattern string) []textMatch {
	normalizedContent, boundaries := normalizeUnicodeWithBoundaries(content)
	normalizedPattern := normalizeUnicode(pattern)
	if normalizedContent == content && normalizedPattern == pattern {
		return nil
	}
	normalizedMatches := exactMatches(normalizedContent, normalizedPattern)
	if len(normalizedMatches) == 0 {
		return normalizedLineMatches(content, pattern, func(line string) string { return strings.TrimSpace(normalizeUnicode(line)) })
	}
	matches := make([]textMatch, 0, len(normalizedMatches))
	for _, match := range normalizedMatches {
		if match.start < 0 || match.end >= len(boundaries) {
			continue
		}
		mapped := textMatch{start: boundaries[match.start], end: boundaries[match.end]}
		if mapped.end > mapped.start {
			matches = append(matches, mapped)
		}
	}
	return matches
}

func blockAnchorMatches(content, pattern string) []textMatch {
	patternLines := strings.Split(normalizeUnicode(pattern), "\n")
	if len(patternLines) < 2 {
		return nil
	}
	normalizedLines := strings.Split(normalizeUnicode(content), "\n")
	originalLines := strings.Split(content, "\n")
	first, last := strings.TrimSpace(patternLines[0]), strings.TrimSpace(patternLines[len(patternLines)-1])
	var candidates []int
	for start := 0; start+len(patternLines) <= len(normalizedLines); start++ {
		if strings.TrimSpace(normalizedLines[start]) == first && strings.TrimSpace(normalizedLines[start+len(patternLines)-1]) == last {
			candidates = append(candidates, start)
		}
	}
	threshold := 0.5
	if len(candidates) > 1 {
		threshold = 0.7
	}
	var matches []textMatch
	for _, start := range candidates {
		ratio := 1.0
		if len(patternLines) > 2 {
			ratio = sequenceSimilarity(strings.Join(normalizedLines[start+1:start+len(patternLines)-1], "\n"), strings.Join(patternLines[1:len(patternLines)-1], "\n"))
		}
		if ratio >= threshold {
			matches = append(matches, lineMatchSpan(content, originalLines, start, len(patternLines)))
		}
	}
	return matches
}

func contextAwareMatches(content, pattern string) []textMatch {
	patternLines := strings.Split(pattern, "\n")
	contentLines := strings.Split(content, "\n")
	if len(patternLines) == 0 || len(patternLines) > len(contentLines) {
		return nil
	}
	var matches []textMatch
	for start := 0; start+len(patternLines) <= len(contentLines); start++ {
		block := contentLines[start : start+len(patternLines)]
		if sequenceSimilarity(strings.TrimSpace(patternLines[0]), strings.TrimSpace(block[0])) < 0.8 ||
			sequenceSimilarity(strings.TrimSpace(patternLines[len(patternLines)-1]), strings.TrimSpace(block[len(block)-1])) < 0.8 {
			continue
		}
		valid := true
		for index, patternLine := range patternLines {
			if strings.TrimSpace(patternLine) == "" {
				continue
			}
			if sequenceSimilarity(strings.TrimSpace(patternLine), strings.TrimSpace(block[index])) < 0.8 {
				valid = false
				break
			}
		}
		if valid {
			matches = append(matches, lineMatchSpan(content, contentLines, start, len(patternLines)))
		}
	}
	return matches
}

func normalizedLineMatches(content, pattern string, normalize func(string) string) []textMatch {
	contentLines := strings.Split(content, "\n")
	patternLines := strings.Split(pattern, "\n")
	normalizedPattern := make([]string, len(patternLines))
	for index, line := range patternLines {
		normalizedPattern[index] = normalize(line)
	}
	var matches []textMatch
	for start := 0; start+len(patternLines) <= len(contentLines); start++ {
		matched := true
		for index := range patternLines {
			if normalize(contentLines[start+index]) != normalizedPattern[index] {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, lineMatchSpan(content, contentLines, start, len(patternLines)))
		}
	}
	return matches
}

func lineMatchSpan(content string, lines []string, start, count int) textMatch {
	startByte := 0
	for index := 0; index < start; index++ {
		startByte += len(lines[index]) + 1
	}
	endByte := startByte
	for index := start; index < start+count; index++ {
		endByte += len(lines[index])
		if index+1 < start+count {
			endByte++
		}
	}
	if endByte > len(content) {
		endByte = len(content)
	}
	return textMatch{start: startByte, end: endByte}
}

func collapseHorizontalWhitespace(value string) string {
	var builder strings.Builder
	inWhitespace := false
	for _, char := range value {
		if char == ' ' || char == '\t' {
			if !inWhitespace {
				builder.WriteByte(' ')
				inWhitespace = true
			}
			continue
		}
		inWhitespace = false
		builder.WriteRune(char)
	}
	return builder.String()
}

func normalizeUnicode(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if replacement, ok := hermesUnicodeMap[char]; ok {
			builder.WriteString(replacement)
		} else {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func normalizeUnicodeWithBoundaries(value string) (string, []int) {
	var builder strings.Builder
	boundaries := make([]int, 0, len(value)+1)
	for originalByte, char := range value {
		replacement, ok := hermesUnicodeMap[char]
		if !ok {
			replacement = string(char)
		}
		for range []byte(replacement) {
			boundaries = append(boundaries, originalByte)
		}
		builder.WriteString(replacement)
	}
	boundaries = append(boundaries, len(value))
	return builder.String(), boundaries
}

func sequenceSimilarity(left, right string) float64 {
	if left == right {
		return 1
	}
	a, b := []rune(left), []rune(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if len(a)*len(b) > 2_000_000 {
		prefix := 0
		for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
			prefix++
		}
		suffix := 0
		for suffix+prefix < len(a) && suffix+prefix < len(b) && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
			suffix++
		}
		return float64(2*(prefix+suffix)) / float64(len(a)+len(b))
	}
	if len(b) > len(a) {
		a, b = b, a
	}
	row := make([]int, len(b)+1)
	for _, leftRune := range a {
		previous := 0
		for index, rightRune := range b {
			old := row[index+1]
			if leftRune == rightRune {
				row[index+1] = previous + 1
			} else if row[index] > row[index+1] {
				row[index+1] = row[index]
			}
			previous = old
		}
	}
	return float64(2*row[len(b)]) / float64(len(a)+len(b))
}

func applyTextMatches(content string, matches []textMatch, replacement, oldString string, reindent bool) string {
	sorted := append([]textMatch(nil), matches...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].start > sorted[j].start })
	result := content
	for _, match := range sorted {
		adjusted := replacement
		if reindent {
			adjusted = reindentReplacement(content[match.start:match.end], oldString, replacement)
		}
		result = result[:match.start] + adjusted + result[match.end:]
	}
	return result
}

func reindentReplacement(fileRegion, oldString, newString string) string {
	if newString == "" {
		return newString
	}
	oldIndent, oldOK := firstMeaningfulIndent(oldString)
	fileIndent, fileOK := firstMeaningfulIndent(fileRegion)
	if !oldOK || !fileOK || oldIndent == fileIndent {
		return newString
	}
	lines := strings.Split(newString, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, oldIndent) {
			lines[index] = fileIndent + strings.TrimPrefix(line, oldIndent)
		} else {
			lines[index] = fileIndent + strings.TrimLeft(line, " \t")
		}
	}
	return strings.Join(lines, "\n")
}

func firstMeaningfulIndent(value string) (string, bool) {
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return line[:len(line)-len(strings.TrimLeft(line, " \t"))], true
	}
	return "", false
}

func detectEscapeDrift(content string, matches []textMatch, oldString, newString string) error {
	for _, suspect := range []string{`\'`, `\"`} {
		if !strings.Contains(newString, suspect) || !strings.Contains(oldString, suspect) {
			continue
		}
		present := false
		for _, match := range matches {
			if strings.Contains(content[match.start:match.end], suspect) {
				present = true
				break
			}
		}
		if !present {
			return fmt.Errorf("escape-drift detected for %q; re-read the file and remove spurious backslashes", suspect)
		}
	}
	return nil
}

func maybeUnescapeReplacement(replacement, content string, matches []textMatch) string {
	var regions strings.Builder
	for _, match := range matches {
		regions.WriteString(content[match.start:match.end])
	}
	result := replacement
	if strings.Contains(regions.String(), "\t") {
		result = strings.ReplaceAll(result, `\t`, "\t")
	}
	if strings.Contains(regions.String(), "\r") {
		result = strings.ReplaceAll(result, `\r`, "\r")
	}
	return result
}

func preserveUnicodeReplacement(fileRegion, oldString, newString string) string {
	normalizedRegion, boundaries := normalizeUnicodeWithBoundaries(fileRegion)
	normalizedOld := normalizeUnicode(oldString)
	if normalizedRegion != normalizedOld {
		return newString
	}
	prefix := 0
	for prefix < len(normalizedOld) && prefix < len(newString) && normalizedOld[prefix] == newString[prefix] {
		prefix++
	}
	suffix := 0
	for suffix+prefix < len(normalizedOld) && suffix+prefix < len(newString) && normalizedOld[len(normalizedOld)-1-suffix] == newString[len(newString)-1-suffix] {
		suffix++
	}
	if prefix >= len(boundaries) || len(normalizedRegion)-suffix >= len(boundaries) {
		return newString
	}
	return fileRegion[:boundaries[prefix]] + newString[prefix:len(newString)-suffix] + fileRegion[boundaries[len(normalizedRegion)-suffix]:]
}

func patchAlreadyApplied(content, oldString, newString string) bool {
	if len(strings.TrimSpace(newString)) < 8 || !strings.Contains(content, newString) {
		return false
	}
	return oldString == newString || !strings.Contains(content, oldString)
}

func formatMatchLocations(content string, matches []textMatch) string {
	limit := len(matches)
	if limit > 5 {
		limit = 5
	}
	rows := make([]string, 0, limit)
	for _, match := range matches[:limit] {
		line := strings.Count(content[:match.start], "\n") + 1
		rows = append(rows, fmt.Sprintf("L%d", line))
	}
	return strings.Join(rows, ", ")
}
