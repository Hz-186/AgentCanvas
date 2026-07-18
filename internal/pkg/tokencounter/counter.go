package tokencounter

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

const (
	MethodTiktoken = "tiktoken"
	MethodFallback = "conservative_utf8"
)

type Result struct {
	Tokens int
	Method string
	Error  string
}

// Count uses the model tokenizer when tiktoken knows the model. Unknown model
// families deliberately use a conservative UTF-8 estimate so an unsupported
// tokenizer cannot cause an over-window provider request.
func Count(providerType, model, text string) Result {
	if text == "" {
		return Result{Method: MethodTiktoken}
	}
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	model = strings.TrimSpace(model)
	if providerType == "openai" || providerType == "openai_compatible" || providerType == "azure_openai" || providerType == "" {
		if encoding, err := tiktoken.EncodingForModel(model); err == nil && encoding != nil {
			return Result{Tokens: len(encoding.Encode(text, nil, nil)), Method: MethodTiktoken}
		} else if model != "" {
			fallback := conservative(text)
			return Result{Tokens: fallback, Method: MethodFallback, Error: fmt.Sprintf("tiktoken model %q is unknown", model)}
		}
	}
	return Result{Tokens: conservative(text), Method: MethodFallback, Error: fmt.Sprintf("tokenizer for provider %q is unknown", providerType)}
}

func conservative(text string) int {
	bytes := len([]byte(text))
	runes := utf8.RuneCountInString(text)
	if bytes == 0 {
		return 0
	}
	// ASCII is usually around four bytes/token while CJK can approach one
	// rune/token. Taking the larger estimate keeps the guard conservative.
	asciiEstimate := (bytes + 2) / 3
	if runes > asciiEstimate {
		return runes
	}
	return asciiEstimate
}
