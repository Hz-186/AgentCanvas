package httpserver

import "testing"

func TestLegacyAgentPageRedirectsToAgentHome(t *testing.T) {
	legacyFlow := "/" + "work" + "flow"
	for _, path := range []string{legacyFlow + "s", legacyFlow + "-teams", "/flow-versions/2", "/eval-runs", "/canvas"} {
		if !isLegacyAgentPage(path) {
			t.Fatalf("legacy page %q was not recognized", path)
		}
	}
}

func TestCurrentAgentPagesAreNotLegacy(t *testing.T) {
	for _, path := range []string{"/app/agents", "/app/knowledge", "/login"} {
		if isLegacyAgentPage(path) {
			t.Fatalf("current page %q was incorrectly classified", path)
		}
	}
}
