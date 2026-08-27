package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"
	runtimeagent "agentcanvas/internal/runtime/agent"
	runtimeevent "agentcanvas/internal/runtime/event"
)

// citationFakeRepository implements the citation usage surface on top of an
// embedded nil memory.Repository (the configuredMemoryRepository pattern) so
// it can be injected into runtimeCore.Memories while only the two methods the
// finalizer uses are real.
type citationFakeRepository struct {
	memory.Repository
	found    []memory.Memory
	markUsed [][]int64
}

func (r *citationFakeRepository) FindByIDs(_ context.Context, _ int64, _ []int64) ([]memory.Memory, error) {
	return r.found, nil
}

func (r *citationFakeRepository) MarkUsed(_ context.Context, _ int64, ids []int64) error {
	r.markUsed = append(r.markUsed, append([]int64(nil), ids...))
	return nil
}

// TestFinalizeCitationsStripsAndEmitsWarnings covers the runtime wiring: the
// final answer loses its citation block, the valid owner citation is bulk
// accounted once, and the foreign ID surfaces as one warning event.
func TestFinalizeCitationsStripsAndEmitsWarnings(t *testing.T) {
	repo := &citationFakeRepository{found: []memory.Memory{
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 101, OwnerID: 1}}},
	}}
	events := &capturedRuntimeEvents{}
	rc := &RunContext{OwnerID: 1, RunID: 7, Events: events}
	n := runtimeCore{coreRepositories: coreRepositories{Memories: repo}}
	raw := "Answer body.\n\n" +
		`<oai-mem-citation memory_id="101">adopted</oai-mem-citation>` + "\n" +
		`<oai-mem-citation memory_id="9"></oai-mem-citation>` + "\n"
	result := &runtimeagent.RunResult{FinalAnswer: raw,
		Steps: []runtimeagent.RunStep{
			{Type: runtimeagent.StepTypeLLMResponse, Content: raw},
			{Type: runtimeagent.StepTypeFinalAnswer, Content: raw},
		}}

	n.finalizeCitations(context.Background(), rc, result)

	if result.FinalAnswer != "Answer body." {
		t.Fatalf("final answer = %q, want citation block stripped", result.FinalAnswer)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps = %+v, want both steps preserved", result.Steps)
	}
	if result.Steps[0].Content != "Answer body." {
		t.Fatalf("llm_response step content = %q, want citation block stripped from emitted steps too", result.Steps[0].Content)
	}
	if result.Steps[1].Content != "Answer body." {
		t.Fatalf("final answer step content = %q, want stripped", result.Steps[1].Content)
	}
	if len(repo.markUsed) != 1 || len(repo.markUsed[0]) != 1 || repo.markUsed[0][0] != 101 {
		t.Fatalf("markUsed calls = %v, want one call with [101]", repo.markUsed)
	}
	if len(events.events) != 1 {
		t.Fatalf("emitted %d events, want one warning for foreign id 9: %#v", len(events.events), events.events)
	}
	event := events.events[0]
	if event.Type != runtimeevent.AgentStep || event.RunID != 7 {
		t.Fatalf("warning event = %#v, want agent_step for run 7", event)
	}
	if !strings.Contains(fmt.Sprintf("%v", event.Payload["error"]), "memory 9") {
		t.Fatalf("warning payload = %#v, want mention of memory 9", event.Payload)
	}
}

// TestFinalizeCitationsNoBlockLeavesAnswerUntouched covers the no-op path:
// text without citations stays byte-identical, no usage updates, no events.
func TestFinalizeCitationsNoBlockLeavesAnswerUntouched(t *testing.T) {
	repo := &citationFakeRepository{found: []memory.Memory{
		{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 101}}},
	}}
	events := &capturedRuntimeEvents{}
	rc := &RunContext{OwnerID: 1, RunID: 7, Events: events}
	n := runtimeCore{coreRepositories: coreRepositories{Memories: repo}}
	result := &runtimeagent.RunResult{FinalAnswer: "Plain answer, no citations."}

	n.finalizeCitations(context.Background(), rc, result)

	if result.FinalAnswer != "Plain answer, no citations." {
		t.Fatalf("final answer = %q, want unchanged", result.FinalAnswer)
	}
	if len(repo.markUsed) != 0 || len(events.events) != 0 {
		t.Fatalf("no citation present but markUsed=%v events=%d", repo.markUsed, len(events.events))
	}
}
