package reflection

import "testing"

func TestCandidateScoreImportanceAndGate(t *testing.T) {
	s := CandidateScore{Severity: 1, EvidenceStrength: .9, Generalizability: .8, Confidence: .9, Novelty: .7}
	if got := s.Importance(); got != .89 {
		t.Fatalf("Importance() = %v", got)
	}
	if !s.ShouldPersist(KindErrorLesson, DefaultPolicy()) {
		t.Fatal("expected persistence")
	}
}

func TestImportantStrategyUsesHigherThreshold(t *testing.T) {
	s := CandidateScore{Severity: .5, EvidenceStrength: .7, Generalizability: .8, Confidence: .8, Novelty: .8}
	if s.ShouldPersist(KindImportantStrategy, DefaultPolicy()) {
		t.Fatal("weak success strategy persisted")
	}
}

func TestPolicyNormalize(t *testing.T) {
	p := (Policy{Enabled: true, RecallTopK: 99}).Normalize()
	if p.RecallTopK != 10 || p.RecallTokenBudget != 800 || p.RuntimeMode != RuntimeActive {
		t.Fatalf("%+v", p)
	}
}

func TestPolicyNormalizePreservesExplicitZeroInlineBudget(t *testing.T) {
	p := DefaultPolicy()
	p.MaxInlinePerRun = 0
	if got := p.Normalize().MaxInlinePerRun; got != 0 {
		t.Fatalf("explicit zero should disable inline reflection, got %d", got)
	}
}

func TestPolicyValidateRejectsInvalidRuntimeControls(t *testing.T) {
	p := DefaultPolicy()
	p.ReflectOnSuccess = "sometimes"
	if err := p.Validate(); err == nil {
		t.Fatal("expected invalid reflect_on_success to be rejected")
	}
	p = DefaultPolicy()
	p.MinConfidence = 1.1
	if err := p.Validate(); err == nil {
		t.Fatal("expected confidence outside 0..1 to be rejected")
	}
}
