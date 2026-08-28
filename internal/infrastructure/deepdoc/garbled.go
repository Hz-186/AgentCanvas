package deepdoc

import "unicode"

const garbledThreshold = 0.3

func isGarbledText(text string) bool {
	if text == "" {
		return true
	}
	nonSpace := 0
	garbled := 0
	letters := 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		nonSpace++
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			letters++
		}
		if isGarbledRune(r) {
			garbled++
		}
	}
	if nonSpace == 0 {
		return true
	}
	if float64(garbled)/float64(nonSpace) >= garbledThreshold {
		return true
	}
	return nonSpace >= 20 && float64(letters)/float64(nonSpace) < 0.15
}

func isLikelyEnglish(text string) bool {
	asciiRunes := 0
	total := 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if r <= 0x7F {
			asciiRunes++
		}
	}
	if total == 0 {
		return false
	}
	return float64(asciiRunes)/float64(total) > 0.5
}

func isGarbledRune(r rune) bool {
	return r == '\ufffd' || unicode.IsControl(r) ||
		(r >= 0xE000 && r <= 0xF8FF) ||
		(r >= 0xF0000 && r <= 0x10FFFF)
}
