package memory_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
)

// These tests pin the chunked candidate-extraction contract (design Decisions
// 7, 8 and 10): structured candidates parsed field-by-field, malformed model
// output retryable on the LINEAR extract backoff channel, per-chunk candidates
// persisted incrementally into result_json so retries skip completed chunks,
// and secrets redacted before the prompt reaches the model.

// scriptedExtractionChatClient answers each Chat call from a per-call script
// and records every request so tests can count model calls and inspect prompts.
type scriptedExtractionChatClient struct {
	mu       sync.Mutex
	respond  func(call int, req llm.ChatRequest) (string, error)
	requests []llm.ChatRequest
}

func (c *scriptedExtractionChatClient) Chat(_ context.Context, _ llm.ChatProviderConfig, req llm.ChatRequest) (*llm.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	call := len(c.requests)
	c.requests = append(c.requests, req)
	content, err := c.respond(call, req)
	if err != nil {
		return nil, err
	}
	return &llm.ChatResponse{Content: content}, nil
}

func (c *scriptedExtractionChatClient) StreamChat(context.Context, llm.ChatProviderConfig, llm.ChatRequest, func(llm.StreamEvent) error) error {
	return nil
}

func (c *scriptedExtractionChatClient) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

func (c *scriptedExtractionChatClient) prompt(call int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests[call].Messages[0].Content
}

func newCandidateExtractionWorker(chat llm.ChatClient, jobs memory.ExtractionJobRepository, messages DreamMessageRepository) *DurableMemoryWorker {
	return NewDurableMemoryWorker(chat, messages, jobs, nil, DurableMemoryConfig{Enabled: true, Model: "test-extraction-model"}, "test-worker")
}

func seedExtractionJob(id, through int64) *memory.ExtractionJob {
	return &memory.ExtractionJob{
		BaseModel:        domain.BaseModel{ID: id, OwnerID: scheduleTestOwner},
		ConversationID:   scheduleTestConversation,
		IdempotencyKey:   fmt.Sprintf("durable:%d:%d:initial", scheduleTestOwner, scheduleTestConversation),
		TriggerReason:    "durable",
		ThroughMessageID: through,
		Status:           string(memory.ExtractionPending),
	}
}

func extractionMessageRow(id int64, content string) conversation.Message {
	return conversation.Message{
		ImmutableModel: domain.ImmutableModel{ID: id, OwnerID: scheduleTestOwner},
		ConversationID: scheduleTestConversation,
		Role:           conversation.RoleUser,
		Content:        content,
	}
}

// sizedMessageRow renders (through the evidence renderer) into a text unit of
// exactly `size` bytes, letting tests control the chunk layout precisely.
func sizedMessageRow(id int64, size int, fill rune) conversation.Message {
	unit := EvidenceUnit{Kind: EvidenceUnitText, MessageID: id, Role: conversation.RoleUser}
	base := evidenceUnitBytes(unit)
	return extractionMessageRow(id, strings.Repeat(string(fill), size-base))
}

func decodeResultJSONTopLevel(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("result_json is not a JSON object: %v (%s)", err, raw)
	}
	return top
}

func decodeDurableResult(t *testing.T, raw json.RawMessage) durableExtractionResult {
	t.Helper()
	var result durableExtractionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode result_json: %v (%s)", err, raw)
	}
	return result
}

func TestCandidateExtraction(t *testing.T) {
	t.Run("shouldParseStructuredCandidatesFromModel", func(t *testing.T) {
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 2)}}
		jobs.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{
			extractionMessageRow(1, "we debugged the deploy pipeline together"),
			extractionMessageRow(2, "the fix was pinning the base image"),
		}}
		expected := []ExtractionCandidate{
			{Title: "Deploy pipeline base image pin", Content: "Pin the base image before deploying to avoid glibc drift.", Type: "lesson", Confidence: 0.85, Importance: 0.6, EvidenceRefs: []string{"messages:1-2"}},
			{Title: "User prefers postmortem summaries", Content: "The user asks for a postmortem after every incident.", Type: "preference", Confidence: 0.72, Importance: 0.55, EvidenceRefs: []string{"tool_call:call_42"}},
		}
		payload, err := json.Marshal(map[string]any{"candidates": expected})
		if err != nil {
			t.Fatal(err)
		}
		chat := &scriptedExtractionChatClient{respond: func(int, llm.ChatRequest) (string, error) { return string(payload), nil }}
		worker := newCandidateExtractionWorker(chat, jobs, messages)

		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}

		job := jobs.jobs[1]
		if job.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("job status = %q, want completed", job.Status)
		}
		if chat.calls() != 1 {
			t.Fatalf("model calls = %d, want exactly one for the single chunk", chat.calls())
		}
		if prompt := chat.prompt(0); !strings.Contains(prompt, "[msg 1]") || !strings.Contains(prompt, "[msg 2]") {
			t.Fatalf("prompt does not carry rendered evidence units: %q", prompt[:80])
		}
		// The result_json schema keys are stable: chunks/merge/outcome plus the
		// additive window markers (Fix round: resume index soundness).
		top := decodeResultJSONTopLevel(t, job.ResultJSON)
		for _, key := range []string{"chunks", "merge", "outcome", "window_after", "window_through"} {
			if _, ok := top[key]; !ok {
				t.Fatalf("result_json lacks the %q key: %s", key, job.ResultJSON)
			}
		}
		result := decodeDurableResult(t, job.ResultJSON)
		got, ok := result.Chunks[0]
		if !ok {
			t.Fatalf("result_json chunks missing index 0: %s", job.ResultJSON)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("parsed candidates = %+v, want field-by-field %+v", got, expected)
		}
		if result.Outcome != durableExtractionOutcomeExtracted {
			t.Fatalf("outcome = %q, want %q", result.Outcome, durableExtractionOutcomeExtracted)
		}
		if len(result.Merge) != 0 {
			t.Fatalf("merge slot = %+v, want empty before the merge pass exists", result.Merge)
		}
	})

	t.Run("shouldReturnRetryableErrorOnMalformedJSON", func(t *testing.T) {
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 1)}}
		jobs.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{extractionMessageRow(1, "some evidence")}}
		chat := &scriptedExtractionChatClient{respond: func(int, llm.ChatRequest) (string, error) { return `{invalid-json`, nil }}
		worker := newCandidateExtractionWorker(chat, jobs, messages)
		before := time.Now().UTC()

		err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation})
		if err == nil {
			t.Fatal("malformed candidate JSON must fail the extraction attempt")
		}

		job := jobs.jobs[1]
		if job.Status != string(memory.ExtractionPending) {
			t.Fatalf("job status = %q, want back to pending for retry", job.Status)
		}
		if job.AttemptCount != 1 {
			t.Fatalf("attempt count = %d, want 1 after the failed claim", job.AttemptCount)
		}
		// LINEAR extract backoff: due_at = now + (AttemptCount+1) minutes, not
		// the phase2 exponential channel.
		if job.DueAt == nil {
			t.Fatal("failed job carries no due_at backoff")
		}
		want := before.Add(time.Duration(job.AttemptCount+1) * time.Minute)
		if delta := job.DueAt.Sub(want); delta < -30*time.Second || delta > 90*time.Second {
			t.Fatalf("due_at = %s, want linear backoff %s (attempt+1 minutes)", job.DueAt.Format(time.RFC3339), want.Format(time.RFC3339))
		}
		if job.Phase2AttemptCount != 0 || strings.HasPrefix(job.ErrorMessage, "phase2:") {
			t.Fatalf("extract failure leaked into the phase2 channel: phase2Attempts=%d error=%q", job.Phase2AttemptCount, job.ErrorMessage)
		}
		if len(job.ResultJSON) != 0 {
			t.Fatalf("failed attempt persisted result_json: %s", job.ResultJSON)
		}
		// The failing call went through the evidence-chunk prompt, not the
		// retired raw-rollout prompt.
		if prompt := chat.prompt(0); !strings.Contains(prompt, "[msg 1]") {
			t.Fatalf("prompt does not carry rendered evidence units: %q", prompt[:80])
		}
	})

	t.Run("shouldPersistChunkCandidatesIncrementallyAndSkipOnRetry", func(t *testing.T) {
		// Three ~50000-byte units chunk into [u1,u2] and [u1,u2,u3] (overlap),
		// forcing exactly two model calls per full extraction pass.
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 3)}}
		jobs.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{
			sizedMessageRow(1, 50000, 'a'),
			sizedMessageRow(2, 50000, 'b'),
			sizedMessageRow(3, 50000, 'c'),
		}}
		chunkZeroJSON := `{"candidates":[{"title":"Chunk zero lesson","content":"lesson zero","type":"lesson","confidence":0.9,"importance":0.7,"evidence_refs":["messages:1-2"]}]}`
		chunkOneJSON := `{"candidates":[{"title":"Chunk one lesson","content":"lesson one","type":"lesson","confidence":0.8,"importance":0.6,"evidence_refs":["messages:3"]}]}`
		chat := &scriptedExtractionChatClient{respond: func(call int, _ llm.ChatRequest) (string, error) {
			switch call {
			case 0:
				return chunkZeroJSON, nil
			case 1:
				return "", errors.New("model overloaded")
			default:
				return chunkOneJSON, nil
			}
		}}
		worker := newCandidateExtractionWorker(chat, jobs, messages)
		payload := DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}

		if err := worker.Handle(context.Background(), payload); err == nil {
			t.Fatal("chunk 1 model failure must fail the extraction attempt")
		}
		if chat.calls() != 2 {
			t.Fatalf("first pass model calls = %d, want one per chunk", chat.calls())
		}
		failed := jobs.jobs[1]
		if failed.Status != string(memory.ExtractionPending) {
			t.Fatalf("failed job status = %q, want pending", failed.Status)
		}
		// result_json already holds chunk 0's candidates although chunk 1 failed.
		partial := decodeDurableResult(t, failed.ResultJSON)
		if _, ok := partial.Chunks[0]; !ok || len(partial.Chunks[0]) != 1 || partial.Chunks[0][0].Title != "Chunk zero lesson" {
			t.Fatalf("chunk 0 candidates not persisted before the failure: %s", failed.ResultJSON)
		}
		if _, ok := partial.Chunks[1]; ok {
			t.Fatalf("failed chunk 1 candidates present in result_json: %s", failed.ResultJSON)
		}
		if partial.Outcome != "" {
			t.Fatalf("partial result_json carries outcome %q, want empty until completion", partial.Outcome)
		}
		// The partial records the window it was chunked from: (0,3]. The retry
		// below keeps this exact window, so chunk 0 must stay skipped (this is
		// the over-discarding guard for the window-shrink fix).
		if partial.WindowAfter != 0 || partial.WindowThrough != 3 {
			t.Fatalf("partial window markers = (%d,%d], want (0,3]", partial.WindowAfter, partial.WindowThrough)
		}

		// The job becomes due again and is reclaimed.
		failed.DueAt = nil
		if err := worker.Handle(context.Background(), payload); err != nil {
			t.Fatalf("retry handle: %v", err)
		}

		if chat.calls() != 3 {
			t.Fatalf("total model calls = %d, want 3: chunk 0 must not be re-sent on retry", chat.calls())
		}
		retryPrompt := chat.prompt(2)
		if !strings.Contains(retryPrompt, strings.Repeat("c", 100)) {
			t.Fatalf("retry did not re-send chunk 1 evidence (message 3 missing)")
		}
		final := jobs.jobs[1]
		if final.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("final status = %q, want completed", final.Status)
		}
		result := decodeDurableResult(t, final.ResultJSON)
		if result.Outcome != durableExtractionOutcomeExtracted {
			t.Fatalf("final outcome = %q, want %q", result.Outcome, durableExtractionOutcomeExtracted)
		}
		if len(result.Chunks[0]) != 1 || result.Chunks[0][0].Title != "Chunk zero lesson" {
			t.Fatalf("chunk 0 candidates lost across the retry: %+v", result.Chunks[0])
		}
		if len(result.Chunks[1]) != 1 || result.Chunks[1][0].Title != "Chunk one lesson" {
			t.Fatalf("chunk 1 candidates missing after retry: %+v", result.Chunks[1])
		}
		if result.WindowAfter != 0 || result.WindowThrough != 3 {
			t.Fatalf("final window markers = (%d,%d], want the unchanged (0,3]", result.WindowAfter, result.WindowThrough)
		}
	})

	t.Run("shouldDiscardPartialChunksWhenWindowShrinksBetweenAttempts", func(t *testing.T) {
		// Pass 1 reads the wide window (0,100]: chunk 0 persists, chunk 1
		// fails. Before the retry, a SUCCESSOR job completes with through 80
		// (completion order is not message order), so previousBoundary moves
		// and the retry window shrinks to (80,100]. Chunk indices are
		// positional within the plan recomputed from the window: the stale
		// wide-window chunk-0 candidates must be discarded, never reused
		// against different evidence.
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 100)}}
		jobs.nextID = 2
		messages := &fakeDreamMessages{items: []conversation.Message{
			sizedMessageRow(10, 50000, 'a'),
			sizedMessageRow(20, 50000, 'b'),
			sizedMessageRow(100, 50000, 'c'),
		}}
		staleJSON := `{"candidates":[{"title":"Stale wide-window lesson","content":"covers messages 10-20","type":"lesson","confidence":0.9,"importance":0.7,"evidence_refs":["messages:10-20"]}]}`
		shrunkJSON := `{"candidates":[{"title":"Shrunken-window lesson","content":"covers message 100","type":"lesson","confidence":0.8,"importance":0.6,"evidence_refs":["messages:100"]}]}`
		chat := &scriptedExtractionChatClient{respond: func(call int, _ llm.ChatRequest) (string, error) {
			switch call {
			case 0:
				return staleJSON, nil
			case 1:
				return "", errors.New("model overloaded")
			default:
				return shrunkJSON, nil
			}
		}}
		worker := newCandidateExtractionWorker(chat, jobs, messages)
		payload := DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}

		if err := worker.Handle(context.Background(), payload); err == nil {
			t.Fatal("chunk 1 model failure must fail the extraction attempt")
		}
		if chat.calls() != 2 {
			t.Fatalf("pass 1 model calls = %d, want one per chunk", chat.calls())
		}
		failed := jobs.jobs[1]
		partial := decodeDurableResult(t, failed.ResultJSON)
		if len(partial.Chunks[0]) != 1 || partial.Chunks[0][0].Title != "Stale wide-window lesson" {
			t.Fatalf("pass 1 did not persist chunk 0: %s", failed.ResultJSON)
		}
		// The partial result records the window it was chunked from.
		if partial.WindowAfter != 0 || partial.WindowThrough != 100 {
			t.Fatalf("partial window markers = (%d,%d], want (0,100]", partial.WindowAfter, partial.WindowThrough)
		}

		// The successor completes BEFORE the retry: previousBoundary moves to
		// 80 and the retry window shrinks to (80,100].
		jobs.jobs[2] = &memory.ExtractionJob{
			BaseModel:        domain.BaseModel{ID: 2, OwnerID: scheduleTestOwner},
			ConversationID:   scheduleTestConversation,
			TriggerReason:    "durable",
			ThroughMessageID: 80,
			Status:           string(memory.ExtractionCompleted),
		}
		failed.DueAt = nil
		if err := worker.Handle(context.Background(), payload); err != nil {
			t.Fatalf("retry handle: %v", err)
		}

		// The shrunken window renders a single chunk; it must be extracted
		// fresh instead of skipped through the stale chunk-0 slot.
		if chat.calls() != 3 {
			t.Fatalf("total model calls = %d, want 3: the shrunken window's chunk must be re-extracted, not skipped via the stale index", chat.calls())
		}
		retryPrompt := chat.prompt(2)
		if !strings.Contains(retryPrompt, strings.Repeat("c", 100)) {
			t.Fatalf("retry prompt does not carry the shrunken window's evidence (message 100 missing)")
		}
		if strings.Contains(retryPrompt, strings.Repeat("a", 100)) {
			t.Fatalf("retry prompt re-read covered messages outside the (80,100] window")
		}
		final := jobs.jobs[1]
		if final.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("final status = %q, want completed", final.Status)
		}
		result := decodeDurableResult(t, final.ResultJSON)
		if result.Outcome != durableExtractionOutcomeExtracted {
			t.Fatalf("final outcome = %q, want %q", result.Outcome, durableExtractionOutcomeExtracted)
		}
		for index, candidates := range result.Chunks {
			for _, candidate := range candidates {
				if candidate.Title == "Stale wide-window lesson" {
					t.Fatalf("stale wide-window candidate survived the window shrink at chunk %d", index)
				}
			}
		}
		if len(result.Chunks[0]) != 1 || result.Chunks[0][0].Title != "Shrunken-window lesson" {
			t.Fatalf("shrunken window candidates = %+v, want the fresh chunk-0 extraction", result.Chunks[0])
		}
		if result.WindowAfter != 80 || result.WindowThrough != 100 {
			t.Fatalf("final window markers = (%d,%d], want the shrunken (80,100]", result.WindowAfter, result.WindowThrough)
		}
	})

	t.Run("shouldRejectCandidateMissingRequiredField", func(t *testing.T) {
		// ASSERT lock: a missing candidate field is a parse error, never a
		// silent zero.
		for _, field := range []string{"title", "content", "type", "confidence", "importance", "evidence_refs"} {
			payload := map[string]any{
				"title": "t", "content": "c", "type": "lesson",
				"confidence": 0.8, "importance": 0.6, "evidence_refs": []string{"messages:1-2"},
			}
			delete(payload, field)
			raw, err := json.Marshal(map[string]any{"candidates": []any{payload}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseExtractionCandidates(string(raw)); err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("missing %q parsed without a field-specific error: %v", field, err)
			}
		}
	})

	t.Run("shouldAcceptEmptyEvidenceRefsForTheGate", func(t *testing.T) {
		// A present-but-empty evidence_refs parses; rejecting it belongs to
		// the deterministic gate (Task 6), not to parsing.
		candidates, err := parseExtractionCandidates(`{"candidates":[{"title":"t","content":"c","type":"lesson","confidence":0.8,"importance":0.6,"evidence_refs":[]}]}`)
		if err != nil {
			t.Fatalf("present-but-empty evidence_refs must parse: %v", err)
		}
		if len(candidates) != 1 {
			t.Fatalf("candidates = %d, want 1", len(candidates))
		}
	})

	t.Run("shouldRejectResponseWithoutCandidatesArray", func(t *testing.T) {
		if _, err := parseExtractionCandidates(`{"memories":[]}`); err == nil {
			t.Fatal("response without a candidates array must fail parsing")
		}
	})

	t.Run("shouldRedactEvidenceBeforePrompt", func(t *testing.T) {
		const rawSecret = "abcd1234efgh5678"
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 1)}}
		jobs.nextID = 1
		messages := &fakeDreamMessages{items: []conversation.Message{
			extractionMessageRow(1, fmt.Sprintf(`the deploy script uses api_key = "%s" for releases`, rawSecret)),
		}}
		chat := &scriptedExtractionChatClient{respond: func(int, llm.ChatRequest) (string, error) { return `{"candidates":[]}`, nil }}
		worker := newCandidateExtractionWorker(chat, jobs, messages)

		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}

		prompt := chat.prompt(0)
		if strings.Contains(prompt, rawSecret) {
			t.Fatalf("raw secret reached the extraction model prompt")
		}
		if !strings.Contains(prompt, "[REDACTED]") {
			t.Fatalf("prompt lacks the redaction placeholder: %q", prompt)
		}
	})
}
