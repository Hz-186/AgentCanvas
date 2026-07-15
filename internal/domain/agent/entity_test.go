package agent

import "testing"

func TestDefinitionSnapshotIsNormalizedAndDeterministic(t *testing.T) {
	definition := Definition{ProviderID: 7, Mode: "react", SystemPrompt: "help"}
	first, firstHash, err := definition.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := definition.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || firstHash == "" || firstHash != secondHash {
		t.Fatalf("snapshot must be deterministic: %s %s", firstHash, secondHash)
	}
	if definition.Normalize().MaxIterations != 8 || definition.Normalize().MaxToolCalls != 16 {
		t.Fatalf("expected safe defaults: %+v", definition.Normalize())
	}
}

func TestDefinitionRejectsWorkspaceWithoutPack(t *testing.T) {
	err := (Definition{ProviderID: 1, Mode: "react", WorkspaceEnabled: true}).Validate()
	if err == nil {
		t.Fatal("expected workspace policy validation error")
	}
}

func TestDefinitionRejectsInvalidLimits(t *testing.T) {
	err := (Definition{ProviderID: 1, Mode: "react", MaxIterations: 51}).Validate()
	if err == nil {
		t.Fatal("expected max_iterations validation error")
	}
}

func TestDefinitionResourceSnapshotHashesCapabilities(t *testing.T) {
	first, _, firstToolHash, err := (Definition{ToolIDs: []int64{1}}).ResourceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	_, _, secondToolHash, err := (Definition{ToolIDs: []int64{2}}).ResourceSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || firstToolHash == secondToolHash {
		t.Fatal("tool schema hash must change with pinned tool IDs")
	}
}
