package memory_usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
)

// These tests pin Decision 10 on the consolidate side: the two
// truncated-text-dump fallback sites in the pipeline are gone. A missing
// model fails consolidation into the phase2 EXPONENTIAL backoff channel, and
// an empty model summary is an error instead of a truncated-text fallback.
// No artifact is written on either failure.

// noModelConsolidateWorker wires a worker whose Phase 1 is already terminal
// (stored chunked result) so Handle goes straight to consolidation.
func noModelConsolidateWorker(chat llm.ChatClient, jobsRepo *fakeExtractionRepo, messages DreamMessageRepository, pipeline *MemoryWritePipeline, artifacts *fakeArtifactRepo, sources *fakeMemorySourceRepo) *DurableMemoryWorker {
	projection := NewConsolidationProjection(artifacts)
	projection.Now = projectionTestNow
	return NewDurableMemoryWorker(chat, messages, jobsRepo, nil,
		DurableMemoryConfig{Enabled: true, Model: "test-extraction-model"},
		"test-worker",
		WithConsolidationProjection(projection),
		WithConsolidationSources(sources),
		WithExtractionWrites(pipeline),
	)
}

func terminalStoredExtractionJob(id, through int64) *memory.ExtractionJob {
	job := seedExtractionJob(id, through)
	job.ResultJSON = []byte(`{"chunks":{"0":[{"title":"Stored lesson","content":"stored content","type":"lesson","confidence":0.8,"importance":0.6,"evidence_refs":["messages:1"]}]},"merge":[],"outcome":"extracted","window_after":0,"window_through":1}`)
	return job
}

func TestNoModelConsolidate(t *testing.T) {
	t.Run("shouldFailWithoutModelDump", func(t *testing.T) {
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: terminalStoredExtractionJob(1, 1)}}
		jobsRepo.nextID = 1
		conversationID := scheduleTestConversation
		sources := newFakeMemorySourceRepo(
			consolidationSourceMemory(21, "ad_hoc", "remember the deploy freeze window", &conversationID, projectionTestNow()),
		)
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		artifacts := newFakeArtifactRepo()
		worker := noModelConsolidateWorker(nil, jobsRepo, &fakeDreamMessages{}, pipeline, artifacts, sources)
		before := time.Now().UTC()

		err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation})
		if err == nil || !strings.Contains(err.Error(), "model") {
			t.Fatalf("handle error = %v, want the missing-model consolidation failure", err)
		}

		job := jobsRepo.jobs[1]
		// Phase 1 stays completed; only the phase2 bookkeeping moves.
		if job.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("job status = %q, want phase 1 to stay completed", job.Status)
		}
		if job.Phase2AttemptCount != 1 {
			t.Fatalf("phase2 attempt count = %d, want 1", job.Phase2AttemptCount)
		}
		if !strings.HasPrefix(job.ErrorMessage, "phase2:") || !strings.Contains(job.ErrorMessage, "model") {
			t.Fatalf("error message = %q, want the phase2-prefixed missing-model error", job.ErrorMessage)
		}
		// PHASE2 EXPONENTIAL channel: due_at = now + 2^(attempt-1) minutes.
		// The tolerance window is deliberately narrower than the linear
		// channel's (attempt+1) minutes so a mixed-up channel fails.
		if job.DueAt == nil {
			t.Fatal("phase2 failure carries no due_at backoff")
		}
		want := before.Add(durablePhase2RetryDelay(job.Phase2AttemptCount))
		if delta := job.DueAt.Sub(want); delta < -15*time.Second || delta > 45*time.Second {
			t.Fatalf("due_at = %s, want phase2 exponential backoff %s", job.DueAt.Format(time.RFC3339), want.Format(time.RFC3339))
		}
		// The retired empty-summary fallback never triggers and no artifact is
		// written by the failed pass.
		if artifacts.count() != 0 {
			t.Fatalf("failed consolidation wrote %d artifact row(s), want zero", artifacts.count())
		}
		if len(listWriteJobs(t, writeJobs)) != 0 {
			t.Fatal("consolidation failure enqueued write jobs, want zero")
		}
	})

	t.Run("shouldFailOnEmptySummaryInsteadOfFallback", func(t *testing.T) {
		// Model configured but returning an empty summary: the retired
		// truncation fallback must not trigger; consolidation fails and no
		// artifact is written.
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 1)}}
		jobsRepo.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{extractionMessageRow(1, "consolidation evidence")}}
		conversationID := scheduleTestConversation
		sources := newFakeMemorySourceRepo(
			consolidationSourceMemory(21, "ad_hoc", "remember the deploy freeze window", &conversationID, projectionTestNow()),
		)
		chat := &scriptedExtractionChatClient{respond: func(call int, _ llm.ChatRequest) (string, error) {
			if call == 0 {
				return `{"candidates":[]}`, nil
			}
			return `{"memory":"# handbook","summary":""}`, nil
		}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		artifacts := newFakeArtifactRepo()
		worker := noModelConsolidateWorker(chat, jobsRepo, messages, pipeline, artifacts, sources)

		err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation})
		if err == nil || !strings.Contains(err.Error(), "summary") {
			t.Fatalf("handle error = %v, want the empty-summary consolidation failure", err)
		}
		job := jobsRepo.jobs[1]
		if job.Phase2AttemptCount != 1 || !strings.HasPrefix(job.ErrorMessage, "phase2:") {
			t.Fatalf("phase2 bookkeeping = %+v, want attempt 1 with a phase2-prefixed error", job)
		}
		if artifacts.count() != 0 {
			t.Fatalf("empty-summary consolidation wrote %d artifact row(s), want zero", artifacts.count())
		}
		if len(listWriteJobs(t, writeJobs)) != 0 {
			t.Fatal("empty-summary consolidation enqueued write jobs, want zero")
		}
	})
}
