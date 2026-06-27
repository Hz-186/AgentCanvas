package chunker

type EstimatedTokenizer struct{}

func (EstimatedTokenizer) Name() string { return "estimated" }

func (EstimatedTokenizer) Count(text string) int {
	runes := []rune(text)
	if len(runes) == 0 {
		return 0
	}
	ascii := 0
	nonASCII := 0
	for _, r := range runes {
		if r <= 127 {
			ascii++
		} else {
			nonASCII++
		}
	}
	estimate := nonASCII + ascii/4
	if ascii%4 != 0 {
		estimate++
	}
	if estimate == 0 {
		return 1
	}
	return estimate
}
