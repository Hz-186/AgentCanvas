package memory_usecase

import (
	"context"
	"testing"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
)

// These tests pin the Phase-2 consolidation input contract for Task 7:
// gatherConsolidationInputs maps BOTH durable evidence sources (ad_hoc and
// extraction) into projection inputs with the correct source kinds, and a
// job that completed with the no_output outcome must never trigger the
// owner-scoped consolidation pass.

// countingConsolidationSourceReader wraps the fake source repository and
// counts ListBySources calls so a test can prove consolidation never even
// started gathering evidence.
type countingConsolidationSourceReader struct {
	inner *fakeMemorySourceRepo
	calls int
}

func (r *countingConsolidationSourceReader) ListBySources(ctx context.Context, ownerID int64, sources []string, limit int) ([]memory.Memory, error) {
	r.calls++
	return r.inner.ListBySources(ctx, ownerID, sources, limit)
}

func TestConsolidationInput(t *testing.T) {
	t.Run("shouldIncludeExtractionSourceMemories", func(t *testing.T) {
		conversationID := scheduleTestConversation
		sources := &countingConsolidationSourceReader{inner: newFakeMemorySourceRepo(
			consolidationSourceMemory(11, "extraction", "extracted lesson eleven", nil, projectionTestNow()),
			consolidationSourceMemory(12, "extraction", "extracted lesson twelve", &conversationID, projectionTestNow()),
			consolidationSourceMemory(21, "ad_hoc", "ad hoc note twenty-one", &conversationID, projectionTestNow()),
			consolidationSourceMemory(22, "ad_hoc", "ad hoc note twenty-two", nil, projectionTestNow()),
		)}
		worker := NewDurableMemoryWorker(nil, &fakeDreamMessages{}, &fakeExtractionRepo{}, nil,
			DurableMemoryConfig{Enabled: true, Model: "test-extraction-model"}, "test-worker",
			WithConsolidationSources(sources))

		inputs, err := worker.gatherConsolidationInputs(context.Background(), scheduleTestOwner)
		if err != nil {
			t.Fatalf("gatherConsolidationInputs: %v", err)
		}
		if sources.calls != 1 {
			t.Fatalf("source reads = %d, want exactly one", sources.calls)
		}
		if len(inputs) != 4 {
			t.Fatalf("inputs = %+v, want all four evidence rows", inputs)
		}
		want := []ProjectionInput{
			{SourceRef: ProjectionSourceRef{SourceID: 11, Kind: ConsolidationSourceRollout, ConversationID: 0}, RawMemory: "extracted lesson eleven"},
			{SourceRef: ProjectionSourceRef{SourceID: 12, Kind: ConsolidationSourceRollout, ConversationID: scheduleTestConversation}, RawMemory: "extracted lesson twelve"},
			{SourceRef: ProjectionSourceRef{SourceID: 21, Kind: ConsolidationSourceAdHoc, ConversationID: scheduleTestConversation}, RawMemory: "ad hoc note twenty-one"},
			{SourceRef: ProjectionSourceRef{SourceID: 22, Kind: ConsolidationSourceAdHoc, ConversationID: 0}, RawMemory: "ad hoc note twenty-two"},
		}
		for index, input := range inputs {
			if input.SourceRef != want[index].SourceRef {
				t.Fatalf("input %d source ref = %+v, want %+v", index, input.SourceRef, want[index].SourceRef)
			}
			if input.RawMemory != want[index].RawMemory {
				t.Fatalf("input %d raw memory = %q, want %q", index, input.RawMemory, want[index].RawMemory)
			}
			if input.SourceAt.IsZero() {
				t.Fatalf("input %d carries no SourceAt, want the memory row timestamp", index)
			}
		}
	})

	t.Run("shouldNotEnqueueConsolidationOnNoOutput", func(t *testing.T) {
		// A completed no_output job carries no new evidence: the owner-scoped
		// consolidation pass must not run at all. Holding the fallback Phase-2
		// lock turns ANY consolidation attempt into a hard error, and the
		// counting reader proves evidence gathering never starts.
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 1)}}
		jobsRepo.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{extractionMessageRow(1, "consolidation evidence")}}
		chat := &scriptedMergeChatClient{respond: func(int, llm.ChatRequest) (string, error) {
			return `{"candidates":[]}`, nil
		}}
		sources := &countingConsolidationSourceReader{inner: newFakeMemorySourceRepo(
			consolidationSourceMemory(41, "extraction", "pending extracted evidence", nil, projectionTestNow()),
		)}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		artifacts := newFakeArtifactRepo()
		projection := NewConsolidationProjection(artifacts)
		projection.Now = projectionTestNow
		worker := NewDurableMemoryWorker(chat, messages, jobsRepo, nil,
			DurableMemoryConfig{Enabled: true, Provider: mergeTestProvider, Model: "test-extraction-model"}, "test-worker",
			WithConsolidationProjection(projection),
			WithConsolidationSources(sources),
			WithExtractionWrites(pipeline))

		durablePhase2FallbackLock.Lock()
		defer durablePhase2FallbackLock.Unlock()
		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}
		job := jobsRepo.jobs[1]
		if job.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("job status = %q, want completed", job.Status)
		}
		result := decodeDurableResult(t, job.ResultJSON)
		if result.Outcome != durableExtractionOutcomeNoOutput {
			t.Fatalf("outcome = %q, want %q", result.Outcome, durableExtractionOutcomeNoOutput)
		}
		if job.Phase2AttemptCount != 0 || job.ErrorMessage != "" {
			t.Fatalf("phase2 bookkeeping moved for a no_output job: attempts=%d error=%q", job.Phase2AttemptCount, job.ErrorMessage)
		}
		if sources.calls != 0 {
			t.Fatalf("no_output job gathered consolidation inputs %d time(s), want zero", sources.calls)
		}
		if artifacts.count() != 0 {
			t.Fatalf("no_output job wrote %d artifact row(s), want zero", artifacts.count())
		}
		if len(listWriteJobs(t, writeJobs)) != 0 {
			t.Fatal("no_output job enqueued write jobs, want zero")
		}
	})
}
