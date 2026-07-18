package tokencounter

import "testing"

func TestCountUsesTiktokenForKnownOpenAIModel(t *testing.T) {
	result := Count("openai_compatible", "gpt-4o", "hello world")
	if result.Method != MethodTiktoken || result.Tokens <= 0 || result.Error != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCountFallsBackConservativelyForUnknownModel(t *testing.T) {
	result := Count("openai_compatible", "private-model-2026", "中文测试")
	if result.Method != MethodFallback || result.Tokens < 4 || result.Error == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
