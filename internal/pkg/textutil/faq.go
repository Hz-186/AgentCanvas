package textutil

import "strings"

type FAQ struct {
	Question string
	Answer   string
	Aliases  []string
	Category string
}

func ParseFAQs(text string) []FAQ {
	lines := strings.Split(text, "\n")
	faqs := make([]FAQ, 0)
	for i := 0; i < len(lines); {
		question := strings.TrimSpace(lines[i])
		if !IsQuestionLine(question) {
			i++
			continue
		}

		faq := FAQ{Question: trimPrefixes(question, "Q:", "问:")}
		answerParts := make([]string, 0, 2)
		j := i + 1
		for ; j < len(lines); j++ {
			line := strings.TrimSpace(lines[j])
			if line == "" {
				if len(answerParts) > 0 {
					break
				}
				continue
			}
			if IsQuestionLine(line) {
				break
			}
			if values, ok := metadataValues(line, "aliases:", "alias:", "别名:"); ok {
				faq.Aliases = append(faq.Aliases, values...)
				continue
			}
			if values, ok := metadataValues(line, "category:", "分类:"); ok {
				if len(values) > 0 {
					faq.Category = values[0]
				}
				continue
			}
			answerParts = append(answerParts, trimPrefixes(line, "A:", "答:"))
		}
		if faq.Question != "" && len(answerParts) > 0 {
			faq.Answer = strings.TrimSpace(strings.Join(answerParts, "\n"))
			faqs = append(faqs, faq)
		}
		i = j
	}
	return faqs
}

func IsQuestionLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "Q:") || strings.HasPrefix(line, "问:") || strings.HasSuffix(line, "?") || strings.HasSuffix(line, "？")
}

func metadataValues(line string, prefixes ...string) ([]string, bool) {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, prefix := range prefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		value := strings.TrimSpace(line[len(prefix):])
		if value == "" {
			return nil, true
		}
		parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == '；' })
		values := make([]string, 0, len(parts))
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				values = append(values, part)
			}
		}
		return values, true
	}
	return nil, false
}

func trimPrefixes(line string, prefixes ...string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range prefixes {
		line = strings.TrimPrefix(line, prefix)
	}
	return strings.TrimSpace(line)
}
