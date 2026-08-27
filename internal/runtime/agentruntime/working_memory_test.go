package agentruntime

import "testing"

func TestTruncateStringKeepsUTF8Valid(t *testing.T) {
	got := truncateString("你好世界", 3)
	if got != "你好世..." {
		t.Fatalf("unexpected truncate result: %q", got)
	}
}
