package agent

import "testing"

func TestRunStatusTransitions(t *testing.T) {
	run := &Run{}
	for _, status := range []string{RunStatusQueued, RunStatusRunning, RunStatusWaitingHuman, RunStatusResuming, RunStatusSucceeded} {
		if err := run.TransitionStatus(status); err != nil {
			t.Fatalf("transition to %s failed: %v", status, err)
		}
	}
	if err := run.TransitionStatus(RunStatusRunning); err == nil {
		t.Fatal("terminal run must not become running again")
	}
}
