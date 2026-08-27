package memory_usecase

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
)

// These tests pin the multi-chunk merge pass (design Decisions 6, 7 and 8):
// a job whose evidence chunks into N>1 parts runs exactly ONE merge model
// call over all per-chunk candidates and per-chunk summaries on the SAME
// extraction model; single-chunk jobs skip the merge entirely; a merge
// failure keeps every per-chunk candidate intact and retries through the
// LINEAR Phase-1 channel with zero new extraction calls; the merged output
// flows through the unchanged deterministic gate before write wiring.

// mergeTestProvider is a non-zero provider config so the tests can prove the
// merge pass reuses the extraction model configuration verbatim (no separate
// merge configuration exists).
var mergeTestProvider = llm.ChatProviderConfig{ProviderType: "openai", BaseURL: "https://merge.invalid", APIKey: "merge-key"}

// scriptedMergeChatClient answers each Chat call from a per-call script and
// records every request AND provider config so tests can count model calls,
// inspect prompts and verify the merge call rides the extraction model setup.
type scriptedMergeChatClient struct {
	mu        sync.Mutex
	respond   func(call int, req llm.ChatRequest) (string, error)
	requests  []llm.ChatRequest
	providers []llm.ChatProviderConfig
}

func (c *scriptedMergeChatClient) Chat(_ context.Context, provider llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	call := len(c.requests)
	c.requests = append(c.requests, req)
	c.providers = append(c.providers, provider)
	content, err := c.respond(call, req)
	if err != nil {
		return nil, err
	}
	return &llm.ChatResponse{Content: content}, nil
}

func (*scriptedMergeChatClient) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return nil
}

func (c *scriptedMergeChatClient) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func (c *scriptedMergeChatClient) prompt(call int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests[call].Messages[0].Content
}

func (c *scriptedMergeChatClient) model(call int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests[call].Model
}

func (c *scriptedMergeChatClient) provider(call int) llm.ChatProviderConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.providers[call]
}

func newMergePassWorker(chat llm.ChatClient, jobs memory.ExtractionJobRepository, messages DreamMessageRepository, pipeline *MemoryWritePipeline) *DurableMemoryWorker {
	return NewDurableMemoryWorker(chat, messages, jobs, nil,
		DurableMemoryConfig{Enabled: true, Provider: mergeTestProvider, Model: "test-extraction-model"},
		"test-worker", WithExtractionWrites(pipeline))
}

// mergeSecretMessageRow renders into a text unit whose content starts with a
// raw secret followed by filler bytes; the renderer must redact the secret
// before the evidence reaches any prompt, including the merge prompt.
func mergeSecretMessageRow(id int64, filler int) conversation.Message {
	return extractionMessageRow(id, `api_key = "abcd1234efgh" `+strings.Repeat("a", filler))
}

func TestMergePass(t *testing.T) {
	t.Run("shouldMergeCandidatesAcrossChunksOnce", func(t *testing.T) {
		// One secret-bearing unit plus eight ~30000-byte units chunk into
		// [1-4], [3-8] and [7-9] (overlap), forcing three extraction calls
		// plus exactly one merge call.
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 9)}}
		jobsRepo.nextID = 1
		items := []conversation.Message{mergeSecretMessageRow(1, 29000)}
		for id := int64(2); id <= 9; id++ {
			items = append(items, sizedMessageRow(id, 30000, rune('a'+int(id)-1)))
		}
		messages := &fakeDreamMessages{items: items}

		duplicate := func(refs string) ExtractionCandidate {
			return ExtractionCandidate{Title: "User prefers concise replies", Content: "Keep replies short and direct.", Type: "preference", Confidence: 0.8, Importance: 0.6, EvidenceRefs: []string{refs}}
		}
		chunkZero := []ExtractionCandidate{duplicate("messages:1-4"), {Title: "Pin the base image", Content: "Pin the base image before deploying.", Type: "lesson", Confidence: 0.85, Importance: 0.65, EvidenceRefs: []string{"messages:2-2"}}}
		chunkOne := []ExtractionCandidate{duplicate("messages:5-6"), {Title: "Postmortem after incidents", Content: "Write a postmortem after every incident.", Type: "preference", Confidence: 0.75, Importance: 0.55, EvidenceRefs: []string{"messages:5-6"}}}
		chunkTwo := []ExtractionCandidate{duplicate("messages:9-9"), {Title: "Deploy timeout is 30s", Content: "The deploy timeout stays at 30 seconds.", Type: "fact", Confidence: 0.9, Importance: 0.7, EvidenceRefs: []string{"messages:9-9"}}}
		merged := []ExtractionCandidate{
			{Title: "User prefers concise replies", Content: "Keep replies short and direct.", Type: "preference", Confidence: 0.85, Importance: 0.6, EvidenceRefs: []string{"messages:1-9"}},
			chunkZero[1], chunkOne[1], chunkTwo[1],
		}
		chat := &scriptedMergeChatClient{respond: func(call int, _ llm.ChatRequest) (string, error) {
			switch call {
			case 0:
				return writeWiringCandidatesJSON(t, chunkZero), nil
			case 1:
				return writeWiringCandidatesJSON(t, chunkOne), nil
			case 2:
				return writeWiringCandidatesJSON(t, chunkTwo), nil
			default:
				return writeWiringCandidatesJSON(t, merged), nil
			}
		}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		worker := newMergePassWorker(chat, jobsRepo, messages, pipeline)

		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}
		if chat.calls() != 4 {
			t.Fatalf("model calls = %d, want 3 per-chunk extractions + exactly one merge", chat.calls())
		}
		// The merge prompt carries every chunk's candidates verbatim plus one
		// evidence digest per chunk, built from redacted evidence only.
		mergePrompt := chat.prompt(3)
		for _, marker := range []string{"[msg 1]", "[msg 3]", "[msg 7]"} {
			if !strings.Contains(mergePrompt, marker) {
				t.Fatalf("merge prompt lacks the chunk summary marker %q", marker)
			}
		}
		for _, run := range []string{strings.Repeat("a", 100), strings.Repeat("c", 100), strings.Repeat("g", 100)} {
			if !strings.Contains(mergePrompt, run) {
				t.Fatalf("merge prompt lacks a chunk summary evidence digest")
			}
		}
		if got := strings.Count(mergePrompt, "User prefers concise replies"); got != 3 {
			t.Fatalf("cross-chunk duplicate appears %d time(s) in the merge prompt, want once per source chunk", got)
		}
		for _, title := range []string{"Pin the base image", "Postmortem after incidents", "Deploy timeout is 30s"} {
			if !strings.Contains(mergePrompt, title) {
				t.Fatalf("merge prompt lacks candidate %q", title)
			}
		}
		if strings.Contains(mergePrompt, "abcd1234efgh") {
			t.Fatal("raw secret reached the merge prompt")
		}
		if !strings.Contains(mergePrompt, "[REDACTED]") {
			t.Fatal("merge prompt lacks the redaction placeholder for the secret-bearing chunk summary")
		}
		// Every call, extraction and merge alike, rides the SAME model config.
		for call := 0; call < chat.calls(); call++ {
			if got := chat.model(call); got != "test-extraction-model" {
				t.Fatalf("call %d model = %q, want the extraction model reused", call, got)
			}
			if got := chat.provider(call); got != mergeTestProvider {
				t.Fatalf("call %d provider = %+v, want the extraction provider config reused", call, got)
			}
		}
		job := jobsRepo.jobs[1]
		if job.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("job status = %q, want completed", job.Status)
		}
		result := decodeDurableResult(t, job.ResultJSON)
		if !reflect.DeepEqual(result.Merge, merged) {
			t.Fatalf("merge slot = %+v, want the merged candidate list %+v", result.Merge, merged)
		}
		if result.Outcome != durableExtractionOutcomeExtracted {
			t.Fatalf("outcome = %q, want %q", result.Outcome, durableExtractionOutcomeExtracted)
		}
		enqueued := listWriteJobs(t, writeJobs)
		if len(enqueued) != 4 {
			t.Fatalf("write jobs = %d, want one per merged candidate", len(enqueued))
		}
		for index := range enqueued {
			if want := extractionWriteKey(1, index); enqueued[index].IdempotencyKey != want {
				t.Fatalf("write job %d key = %q, want %q", index, enqueued[index].IdempotencyKey, want)
			}
		}
	})

	t.Run("shouldSkipMergeForSingleChunk", func(t *testing.T) {
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 1)}}
		jobsRepo.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{extractionMessageRow(1, "we pinned the base image after the glibc incident")}}
		candidates := []ExtractionCandidate{
			{Title: "Base image pin", Content: "Pin the base image before deploying.", Type: "lesson", Confidence: 0.85, Importance: 0.6, EvidenceRefs: []string{"messages:1"}},
			{Title: "Postmortem preference", Content: "The user asks for a postmortem after incidents.", Type: "preference", Confidence: 0.75, Importance: 0.55, EvidenceRefs: []string{"messages:1"}},
		}
		chat := &scriptedMergeChatClient{respond: func(int, llm.ChatRequest) (string, error) {
			return writeWiringCandidatesJSON(t, candidates), nil
		}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		worker := newMergePassWorker(chat, jobsRepo, messages, pipeline)

		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}
		if chat.calls() != 1 {
			t.Fatalf("model calls = %d, want the single extraction call and zero merge calls", chat.calls())
		}
		if prompt := chat.prompt(0); strings.Contains(prompt, "CHUNK SUMMARIES") {
			t.Fatal("single-chunk job was sent through the merge prompt")
		}
		result := decodeDurableResult(t, jobsRepo.jobs[1].ResultJSON)
		if result.Merge != nil {
			t.Fatalf("merge slot = %+v, want empty for a single-chunk job", result.Merge)
		}
		if len(result.Chunks[0]) != 2 {
			t.Fatalf("chunk candidates = %+v, want the two extracted candidates", result.Chunks[0])
		}
		if len(listWriteJobs(t, writeJobs)) != 2 {
			t.Fatalf("write jobs = %d, want the chunk candidates to go straight to the gate", len(listWriteJobs(t, writeJobs)))
		}
	})

	t.Run("shouldKeepChunkCandidatesOnMergeFailure", func(t *testing.T) {
		// Five 50000-byte units chunk into [1,2], [1,2,3,4] and [3,4,5]
		// (overlap), forcing three extraction calls and one merge call whose
		// first attempt fails.
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 5)}}
		jobsRepo.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{
			sizedMessageRow(1, 50000, 'a'),
			sizedMessageRow(2, 50000, 'b'),
			sizedMessageRow(3, 50000, 'c'),
			sizedMessageRow(4, 50000, 'd'),
			sizedMessageRow(5, 50000, 'e'),
		}}
		chunkCandidates := [][]ExtractionCandidate{
			{{Title: "Chunk zero lesson", Content: "lesson zero content", Type: "lesson", Confidence: 0.9, Importance: 0.7, EvidenceRefs: []string{"messages:1-2"}}},
			{{Title: "Chunk one lesson", Content: "lesson one content", Type: "lesson", Confidence: 0.85, Importance: 0.65, EvidenceRefs: []string{"messages:3-4"}}},
			{{Title: "Chunk two lesson", Content: "lesson two content", Type: "lesson", Confidence: 0.8, Importance: 0.6, EvidenceRefs: []string{"messages:5"}}},
		}
		merged := []ExtractionCandidate{{Title: "Merged lesson across chunks", Content: "The three chunks describe one lesson.", Type: "lesson", Confidence: 0.85, Importance: 0.7, EvidenceRefs: []string{"messages:1-5"}}}
		chat := &scriptedMergeChatClient{respond: func(call int, _ llm.ChatRequest) (string, error) {
			switch call {
			case 0, 1, 2:
				return writeWiringCandidatesJSON(t, chunkCandidates[call]), nil
			case 3:
				return "", errors.New("merge model overloaded")
			default:
				return writeWiringCandidatesJSON(t, merged), nil
			}
		}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		worker := newMergePassWorker(chat, jobsRepo, messages, pipeline)
		payload := DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}
		before := time.Now().UTC()

		err := worker.Handle(context.Background(), payload)
		if err == nil || !strings.Contains(err.Error(), "merge model overloaded") {
			t.Fatalf("handle error = %v, want the merge failure", err)
		}
		if chat.calls() != 4 {
			t.Fatalf("pass 1 model calls = %d, want 3 extractions + the failed merge", chat.calls())
		}
		job := jobsRepo.jobs[1]
		if job.Status != string(memory.ExtractionPending) {
			t.Fatalf("job status = %q, want back to pending for retry", job.Status)
		}
		// LINEAR Phase-1 channel: due_at = now + (AttemptCount+1) minutes,
		// never the phase2 exponential channel.
		if job.AttemptCount != 1 || job.DueAt == nil {
			t.Fatalf("attempt=%d due_at=%v, want attempt 1 with a backoff", job.AttemptCount, job.DueAt)
		}
		want := before.Add(time.Duration(job.AttemptCount+1) * time.Minute)
		if delta := job.DueAt.Sub(want); delta < -30*time.Second || delta > 90*time.Second {
			t.Fatalf("due_at = %s, want linear backoff %s (attempt+1 minutes)", job.DueAt.Format(time.RFC3339), want.Format(time.RFC3339))
		}
		if job.Phase2AttemptCount != 0 || strings.HasPrefix(job.ErrorMessage, "phase2:") {
			t.Fatalf("merge failure leaked into the phase2 channel: phase2=%d error=%q", job.Phase2AttemptCount, job.ErrorMessage)
		}
		partial := decodeDurableResult(t, job.ResultJSON)
		for index, wantCandidates := range chunkCandidates {
			if !reflect.DeepEqual(partial.Chunks[index], wantCandidates) {
				t.Fatalf("chunk %d candidates not preserved across the merge failure: %+v", index, partial.Chunks[index])
			}
		}
		if partial.Merge != nil {
			t.Fatalf("merge slot = %+v, want empty after the failed merge", partial.Merge)
		}
		if partial.Outcome != "" {
			t.Fatalf("failed merge persisted outcome %q, want empty so the retry re-enters", partial.Outcome)
		}

		// The job becomes due again and is reclaimed: the retry must re-send
		// ONLY the merge, never the completed chunks.
		job.DueAt = nil
		if err := worker.Handle(context.Background(), payload); err != nil {
			t.Fatalf("retry handle: %v", err)
		}
		if chat.calls() != 5 {
			t.Fatalf("total model calls = %d, want 5: the retry makes zero extraction calls and re-sends only the merge", chat.calls())
		}
		retryPrompt := chat.prompt(4)
		if !strings.Contains(retryPrompt, "CHUNK SUMMARIES") {
			t.Fatal("the retry's only model call is not a merge call")
		}
		for _, candidates := range chunkCandidates {
			if !strings.Contains(retryPrompt, candidates[0].Title) {
				t.Fatalf("retry merge prompt lacks chunk candidate %q", candidates[0].Title)
			}
		}
		final := jobsRepo.jobs[1]
		if final.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("final status = %q, want completed", final.Status)
		}
		result := decodeDurableResult(t, final.ResultJSON)
		if !reflect.DeepEqual(result.Merge, merged) {
			t.Fatalf("merge slot = %+v, want the merged list after the retry", result.Merge)
		}
		if result.Outcome != durableExtractionOutcomeExtracted {
			t.Fatalf("final outcome = %q, want %q", result.Outcome, durableExtractionOutcomeExtracted)
		}
		enqueued := listWriteJobs(t, writeJobs)
		if len(enqueued) != 1 || enqueued[0].IdempotencyKey != extractionWriteKey(1, 0) {
			t.Fatalf("write jobs = %+v, want the single merged candidate enqueued once", enqueued)
		}
	})

	t.Run("shouldReGateMergedOutput", func(t *testing.T) {
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 3)}}
		jobsRepo.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{
			sizedMessageRow(1, 50000, 'a'),
			sizedMessageRow(2, 50000, 'b'),
			sizedMessageRow(3, 50000, 'c'),
		}}
		perChunk := []ExtractionCandidate{{Title: "Shared lesson", Content: "lesson seen in both chunks", Type: "lesson", Confidence: 0.9, Importance: 0.7, EvidenceRefs: []string{"messages:1-3"}}}
		good := ExtractionCandidate{Title: "Verified lesson", Content: "verified by the merge pass", Type: "lesson", Confidence: 0.85, Importance: 0.6, EvidenceRefs: []string{"messages:1-2"}}
		low := ExtractionCandidate{Title: "Low confidence guess", Content: "the model was unsure", Type: "lesson", Confidence: 0.55, Importance: 0.6, EvidenceRefs: []string{"messages:3"}}
		chat := &scriptedMergeChatClient{respond: func(call int, _ llm.ChatRequest) (string, error) {
			if call < 2 {
				return writeWiringCandidatesJSON(t, perChunk), nil
			}
			return writeWiringCandidatesJSON(t, []ExtractionCandidate{good, low}), nil
		}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		worker := newMergePassWorker(chat, jobsRepo, messages, pipeline)

		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}
		if chat.calls() != 3 {
			t.Fatalf("model calls = %d, want 2 extractions + 1 merge", chat.calls())
		}
		enqueued := listWriteJobs(t, writeJobs)
		if len(enqueued) != 1 || enqueued[0].IdempotencyKey != extractionWriteKey(1, 0) {
			t.Fatalf("write jobs = %+v, want only the gate-accepted merged candidate", enqueued)
		}
		result := decodeDurableResult(t, jobsRepo.jobs[1].ResultJSON)
		if !reflect.DeepEqual(result.Merge, []ExtractionCandidate{good, low}) {
			t.Fatalf("merge slot = %+v, want the full merge output preserved", result.Merge)
		}
		if len(result.Rejections) != 1 || result.Rejections[0].Title != "Low confidence guess" || !strings.Contains(result.Rejections[0].Reason, "confidence") {
			t.Fatalf("rejections = %+v, want the low-confidence merged candidate rejected with a confidence reason", result.Rejections)
		}
		if result.Outcome != durableExtractionOutcomeExtracted {
			t.Fatalf("outcome = %q, want %q", result.Outcome, durableExtractionOutcomeExtracted)
		}
	})

	t.Run("shouldGateEmptyMergeOutputAsNoOutput", func(t *testing.T) {
		// A merge that legitimately returns zero candidates must be gated as
		// no_output: the gate consumes the (empty) merge slot and must NOT
		// fall back to the per-chunk candidates behind the merge's back.
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 3)}}
		jobsRepo.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{
			sizedMessageRow(1, 50000, 'a'),
			sizedMessageRow(2, 50000, 'b'),
			sizedMessageRow(3, 50000, 'c'),
		}}
		perChunk := []ExtractionCandidate{{Title: "Chunk candidate", Content: "would pass the gate if it bypassed the merge", Type: "lesson", Confidence: 0.9, Importance: 0.7, EvidenceRefs: []string{"messages:1-3"}}}
		chat := &scriptedMergeChatClient{respond: func(call int, _ llm.ChatRequest) (string, error) {
			if call < 2 {
				return writeWiringCandidatesJSON(t, perChunk), nil
			}
			return `{"candidates":[]}`, nil
		}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		worker := newMergePassWorker(chat, jobsRepo, messages, pipeline)

		// Holding the Phase-2 lock turns any consolidation attempt into an
		// error: the no_output job must never get there.
		durablePhase2FallbackLock.Lock()
		defer durablePhase2FallbackLock.Unlock()
		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}
		result := decodeDurableResult(t, jobsRepo.jobs[1].ResultJSON)
		if result.Merge == nil || len(result.Merge) != 0 {
			t.Fatalf("merge slot = %+v, want the empty merge output recorded", result.Merge)
		}
		if result.Outcome != durableExtractionOutcomeNoOutput {
			t.Fatalf("outcome = %q, want %q when the merge returns nothing", result.Outcome, durableExtractionOutcomeNoOutput)
		}
		if len(listWriteJobs(t, writeJobs)) != 0 {
			t.Fatal("empty merge output still enqueued write jobs")
		}
	})

	t.Run("shouldNotReRunMergeAfterEnqueueFailure", func(t *testing.T) {
		// The merge slot is persisted as soon as the merge lands, BEFORE the
		// write jobs are enqueued: an enqueue failure keeps the stored result
		// resumable and the retry re-gates the stored merge output with zero
		// new model calls.
		jobsRepo := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{55: seedExtractionJob(55, 3)}}
		jobsRepo.nextID = 55
		messages := &fakeDreamMessages{items: []conversation.Message{
			sizedMessageRow(1, 50000, 'a'),
			sizedMessageRow(2, 50000, 'b'),
			sizedMessageRow(3, 50000, 'c'),
		}}
		perChunk := []ExtractionCandidate{{Title: "Chunk candidate", Content: "extracted in both chunks", Type: "lesson", Confidence: 0.9, Importance: 0.7, EvidenceRefs: []string{"messages:1-3"}}}
		merged := []ExtractionCandidate{{Title: "Merged candidate", Content: "deduplicated by the merge pass", Type: "lesson", Confidence: 0.85, Importance: 0.65, EvidenceRefs: []string{"messages:1-3"}}}
		chat := &scriptedMergeChatClient{respond: func(call int, _ llm.ChatRequest) (string, error) {
			if call < 2 {
				return writeWiringCandidatesJSON(t, perChunk), nil
			}
			return writeWiringCandidatesJSON(t, merged), nil
		}}
		rows := newFakeMemoryRowRepo()
		pipeline, writeJobs := newWriteWiringPipeline(rows)
		worker := newMergePassWorker(chat, jobsRepo, messages, pipeline)
		payload := DurableMemoryPayload{JobID: 55, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}

		// Pass 1: the write-job store transiently rejects the enqueue.
		writeJobs.failCreate = errors.New("write job store unavailable")
		if err := worker.Handle(context.Background(), payload); err == nil {
			t.Fatal("transient enqueue failure must fail the extraction attempt")
		}
		writeJobs.failCreate = nil
		if chat.calls() != 3 {
			t.Fatalf("pass 1 model calls = %d, want 2 extractions + 1 merge", chat.calls())
		}
		partial := decodeDurableResult(t, jobsRepo.jobs[55].ResultJSON)
		if !reflect.DeepEqual(partial.Merge, merged) {
			t.Fatalf("merge slot = %+v, want the merge persisted before the enqueue so the retry can skip it", partial.Merge)
		}
		if partial.Outcome != "" {
			t.Fatalf("failed enqueue persisted outcome %q, want empty so the retry re-enters", partial.Outcome)
		}

		jobsRepo.jobs[55].DueAt = nil
		if err := worker.Handle(context.Background(), payload); err != nil {
			t.Fatalf("retry handle: %v", err)
		}
		if chat.calls() != 3 {
			t.Fatalf("total model calls = %d, want 3: the retry must re-gate the stored merge without any model call", chat.calls())
		}
		enqueued := listWriteJobs(t, writeJobs)
		if len(enqueued) != 1 || enqueued[0].IdempotencyKey != extractionWriteKey(55, 0) {
			t.Fatalf("write jobs = %+v, want the merged candidate enqueued exactly once", enqueued)
		}
		result := decodeDurableResult(t, jobsRepo.jobs[55].ResultJSON)
		if result.Outcome != durableExtractionOutcomeExtracted {
			t.Fatalf("final outcome = %q, want %q", result.Outcome, durableExtractionOutcomeExtracted)
		}
	})
}
