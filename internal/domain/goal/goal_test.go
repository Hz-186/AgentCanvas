package goal

import "testing"

func TestNormalizeBudgetDefaultsAndCaps(t *testing.T) {
	ceiling := int64(100)
	got, err := NormalizeBudget(nil, &ceiling)
	if err != nil || got == nil || *got != ceiling {
		t.Fatalf("nil budget should default to ceiling: got=%v err=%v", got, err)
	}
	requested := int64(101)
	if _, err := NormalizeBudget(&requested, &ceiling); err == nil {
		t.Fatal("budget above ceiling must be rejected")
	}
	zero := int64(0)
	if _, err := NormalizeBudget(&zero, &ceiling); err == nil {
		t.Fatal("non-positive budget must be rejected")
	}
}

func TestGoalStatusTerminalMatchesDurable(t *testing.T) {
	if !IsTerminal(StatusBudgetLimited) || !IsTerminal(StatusComplete) {
		t.Fatal("budget_limited and complete must be terminal")
	}
	if IsTerminal(StatusBlocked) || IsTerminal(StatusPaused) || IsTerminal(StatusUsageLimited) {
		t.Fatal("blocked, paused, and usage_limited are resumable/non-terminal states")
	}
}
