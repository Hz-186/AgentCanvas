package agentruntime

import (
	"context"
	"testing"

	runtimeagent "agentcanvas/internal/runtime/agent"
)

// These tests pin the explicit durable-scheduling stop-reason whitelist: only
// root runs that exhausted budget or answered finally schedule extraction.

// allStopReasons lists every StopReason constant exactly once. Extending the
// enum MUST extend this list and force a whitelist decision.
func allStopReasons() []string {
	return []string{
		runtimeagent.StopReasonFinalAnswer,
		runtimeagent.StopReasonMaxIterations,
		runtimeagent.StopReasonMaxToolCalls,
		runtimeagent.StopReasonTimeout,
		runtimeagent.StopReasonCancelled,
		runtimeagent.StopReasonPaused,
		runtimeagent.StopReasonWaitingHuman,
		runtimeagent.StopReasonLLMError,
		runtimeagent.StopReasonToolNameNotFound,
		runtimeagent.StopReasonReflectionFailed,
		runtimeagent.StopReasonContextOverflow,
		runtimeagent.StopReasonClarification,
	}
}

var whitelistedStopReasons = map[string]bool{
	runtimeagent.StopReasonFinalAnswer:   true,
	runtimeagent.StopReasonMaxIterations: true,
	runtimeagent.StopReasonMaxToolCalls:  true,
	runtimeagent.StopReasonTimeout:       true,
}

func newWhitelistTestCore(calls *int) runtimeCore {
	return runtimeCore{corePolicies: corePolicies{
		OnExtractTrigger: func(_ context.Context, ownerID, conversationID int64, roundNumber int) {
			if ownerID != 1 || conversationID != 7 || roundNumber != 3 {
				panic("unexpected extraction trigger arguments")
			}
			*calls++
		},
	}}
}

func rootRunContext() *RunContext {
	conversationID := int64(7)
	return &RunContext{OwnerID: 1, ConversationID: &conversationID}
}

func TestDurableTriggerWhitelist(t *testing.T) {
	t.Run("shouldScheduleOnlyForWhitelistedStopReasons", func(t *testing.T) {
		reasons := allStopReasons()
		if len(reasons) != 12 {
			t.Fatalf("stop reason constants = %d, want all 12 covered by this test", len(reasons))
		}
		for _, reason := range reasons {
			calls := 0
			core := newWhitelistTestCore(&calls)

			core.checkExtractionTrigger(context.Background(), rootRunContext(), &runtimeagent.RunResult{StopReason: reason}, 3, true)

			want := 0
			if whitelistedStopReasons[reason] {
				want = 1
			}
			if calls != want {
				t.Errorf("stop reason %q scheduled %d call(s), want %d", reason, calls, want)
			}
		}
		if len(whitelistedStopReasons) != 4 {
			t.Fatalf("whitelist size = %d, want exactly the 4 budget/answer reasons", len(whitelistedStopReasons))
		}
	})

	t.Run("shouldKeepSubagentAndGateExclusions", func(t *testing.T) {
		parentRunID := int64(42)
		cases := []struct {
			name    string
			rc      *RunContext
			enabled bool
		}{
			{name: "subagent by parent run", rc: &RunContext{OwnerID: 1, ConversationID: rootRunContext().ConversationID, ParentRunID: &parentRunID}, enabled: true},
			{name: "subagent by delegation depth", rc: &RunContext{OwnerID: 1, ConversationID: rootRunContext().ConversationID, DelegationDepth: 2}, enabled: true},
			{name: "memory disabled", rc: rootRunContext(), enabled: false},
			{name: "missing conversation", rc: &RunContext{OwnerID: 1}, enabled: true},
		}
		for _, test := range cases {
			calls := 0
			core := newWhitelistTestCore(&calls)

			// A whitelisted stop reason must still not schedule when any
			// exclusion applies.
			core.checkExtractionTrigger(context.Background(), test.rc, &runtimeagent.RunResult{StopReason: runtimeagent.StopReasonMaxIterations}, 3, test.enabled)

			if calls != 0 {
				t.Errorf("%s: extraction trigger calls = %d, want 0", test.name, calls)
			}
		}
	})
}
