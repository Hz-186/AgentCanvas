package memory_usecase

import (
	"context"
	"testing"

	"agentcanvas/internal/domain/memory"
)

// This test pins the scope of the extraction dedup-key policy (design
// Decision 9): SQLMemoryWriter computes a content hash ONLY for
// source=extraction. ad_hoc, reflection and proposal keep the default
// semantics — DeduplicationKey equals the job idempotency key verbatim — and
// consolidation writes through the artifact projection, never through
// SQLMemoryWriter.

func TestDedupPolicyScope(t *testing.T) {
	t.Run("shouldNotAlterNonExtractionSources", func(t *testing.T) {
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)

		adHoc := NewAdHocWriteJobAdapter(pipeline)
		if _, err := adHoc.AppendAdHocNote(context.Background(), scheduleTestOwner, scheduleTestConversation, 42, "请记住这个偏好", "已记录"); err != nil {
			t.Fatalf("enqueue ad_hoc: %v", err)
		}
		reflection := NewTerminalReflectionWriteAdapter(pipeline)
		if err := reflection.EnqueueTerminalReflection(context.Background(), memory.TerminalReflectionRequest{
			OwnerID: scheduleTestOwner, AgentID: 5, RunID: 9, Content: "reflection lesson from the run",
		}); err != nil {
			t.Fatalf("enqueue reflection: %v", err)
		}
		proposal := NewProposalWriteJobAdapter(pipeline)
		if err := proposal.EnqueueProposalWriteJob(context.Background(), memory.ProposalWriteJobRequest{
			OwnerID: scheduleTestOwner, AgentID: 5, RunID: 9, ProposalID: 11, Title: "proposal", Content: "proposal content",
			Evidence: `[{"type":"run","id":9}]`,
		}); err != nil {
			t.Fatalf("enqueue proposal: %v", err)
		}

		for i := 0; i < 3; i++ {
			processed, err := pipeline.ProcessNext(context.Background())
			if !processed || err != nil {
				t.Fatalf("process write job %d: processed=%v err=%v", i, processed, err)
			}
		}

		if rows.rowCount() != 3 {
			t.Fatalf("memory rows = %d, want one per non-extraction job", rows.rowCount())
		}
		// Regression equality lock: every non-extraction source's
		// DeduplicationKey equals its job idempotency key verbatim.
		wantKeys := map[string]string{
			"ad_hoc":     "ad_hoc:42",
			"reflection": "reflection:run:9",
			"proposal":   "proposal:11",
		}
		for source, wantKey := range wantKeys {
			job, ok := writeJobs.jobByKey(scheduleTestOwner, wantKey)
			if !ok {
				t.Fatalf("write job %q missing", wantKey)
			}
			if job.Source != source || job.IdempotencyKey != wantKey {
				t.Fatalf("job = (source %q, key %q), want (%q, %q)", job.Source, job.IdempotencyKey, source, wantKey)
			}
			if !rows.hasDedupKey(scheduleTestOwner, wantKey, source) {
				t.Fatalf("source %s: no memory row whose DeduplicationKey equals the job idempotency key %q verbatim", source, wantKey)
			}
		}

		// Consolidation writes via the ConsolidationProjection artifact rows;
		// SQLMemoryWriter is never invoked and no write job is enqueued.
		artifacts := newFakeArtifactRepo()
		projection := NewConsolidationProjection(artifacts)
		projection.Now = projectionTestNow
		conversationID := scheduleTestConversation
		sources := newFakeMemorySourceRepo(
			consolidationSourceMemory(21, "ad_hoc", "consolidation input note", &conversationID, projectionTestNow()),
		)
		chat := &recordingDurableChatClient{content: `{"memory":"# Memory\n\nconsolidated","summary":"consolidated"}`}
		worker := NewDurableMemoryWorker(chat, &fakeDreamMessages{}, &fakeExtractionRepo{}, nil,
			DurableMemoryConfig{Enabled: true, Model: "test-extraction-model"}, "test-worker",
			WithConsolidationProjection(projection),
			WithConsolidationSources(sources),
		)
		if err := worker.consolidate(context.Background(), scheduleTestOwner); err != nil {
			t.Fatalf("consolidate: %v", err)
		}
		if artifacts.count() != 2 {
			t.Fatalf("consolidation artifacts = %d, want handbook+summary", artifacts.count())
		}
		if writeJobs.countJobs() != 3 {
			t.Fatalf("consolidation enqueued write jobs: %d row(s), want the unchanged 3", writeJobs.countJobs())
		}
		if rows.rowCount() != 3 || rows.createCalls() != 3 {
			t.Fatalf("consolidation touched SQLMemoryWriter: rows=%d creates=%d, want no new memory rows", rows.rowCount(), rows.createCalls())
		}
	})
}
