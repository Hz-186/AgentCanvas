package workspace_usecase

import "testing"

func TestWithinAnyRootRejectsPrefixConfusion(t *testing.T) {
	if withinAnyRoot("/srv/workspaces-evil/project", []string{"/srv/workspaces"}) {
		t.Fatal("prefix-only path comparison escaped allowed root")
	}
	if !withinAnyRoot("/srv/workspaces/project", []string{"/srv/workspaces"}) {
		t.Fatal("valid descendant was rejected")
	}
}
