package agentruntime

import "testing"

func TestPythonToolAllowlistIsDenyByDefault(t *testing.T) {
	if allowed := intersectNames([]string{"python_text_stats"}, nil); len(allowed) != 0 {
		t.Fatalf("empty global allowlist must deny all Python tools: %v", allowed)
	}
	allowed := intersectNames(
		[]string{"python_text_stats", "python_json_transform"},
		[]string{"python_text_stats"},
	)
	if len(allowed) != 1 || allowed[0] != "python_text_stats" {
		t.Fatalf("unexpected allowlist intersection: %v", allowed)
	}
}
