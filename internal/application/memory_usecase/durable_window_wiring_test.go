package memory_usecase

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
)

// These tests pin the durable window-read wiring (design Decision 2): the
// worker's window read prefers the archive-inclusive range reader exactly
// once per window, archived rows reach the evidence renderer, and
// repositories without the interface keep the historical active-only reads.

// archivingDreamMessages mirrors the production MySQL repository contract:
// the archive-inclusive reader returns archived rows, the active readers
// filter them out. Counters pin which path the worker actually took.
type archivingDreamMessages struct {
	items                   []conversation.Message
	archiveInclusiveCalls   int
	activeThroughCalls      int
	activeAfterThroughCalls int
}

func (f *archivingDreamMessages) isActive(item conversation.Message) bool {
	return item.ArchivedAt == nil
}

func (f *archivingDreamMessages) ListActiveByConversation(_ context.Context, _, _ int64) ([]conversation.Message, error) {
	items := make([]conversation.Message, 0, len(f.items))
	for _, item := range f.items {
		if f.isActive(item) {
			items = append(items, item)
		}
	}
	return items, nil
}

func (f *archivingDreamMessages) ListActiveThrough(_ context.Context, _, _, throughMessageID int64) ([]conversation.Message, error) {
	f.activeThroughCalls++
	items := make([]conversation.Message, 0, len(f.items))
	for _, item := range f.items {
		if f.isActive(item) && item.ID <= throughMessageID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (f *archivingDreamMessages) LatestActiveMessageID(_ context.Context, _, _ int64) (int64, error) {
	for i := len(f.items) - 1; i >= 0; i-- {
		if f.isActive(f.items[i]) {
			return f.items[i].ID, nil
		}
	}
	return 0, nil
}

func (f *archivingDreamMessages) ListActiveAfterThrough(_ context.Context, _, _, afterMessageID, throughMessageID int64) ([]conversation.Message, error) {
	f.activeAfterThroughCalls++
	items := make([]conversation.Message, 0, len(f.items))
	for _, item := range f.items {
		if f.isActive(item) && item.ID > afterMessageID && item.ID <= throughMessageID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (f *archivingDreamMessages) ListThroughIncludingArchived(_ context.Context, _, _, afterID, throughID int64) ([]conversation.Message, error) {
	f.archiveInclusiveCalls++
	items := make([]conversation.Message, 0, len(f.items))
	for _, item := range f.items {
		if item.ID > afterID && item.ID <= throughID {
			items = append(items, item)
		}
	}
	return items, nil
}

// activeOnlyDreamMessages implements only the base DreamMessageRepository
// contract: no archive-inclusive reader, no range reader. It pins the legacy
// ListActiveThrough fallback.
type activeOnlyDreamMessages struct {
	items                  []conversation.Message
	listActiveThroughCalls int
}

func (f *activeOnlyDreamMessages) ListActiveByConversation(_ context.Context, _, _ int64) ([]conversation.Message, error) {
	return append([]conversation.Message(nil), f.items...), nil
}

func (f *activeOnlyDreamMessages) ListActiveThrough(_ context.Context, _, _, throughMessageID int64) ([]conversation.Message, error) {
	f.listActiveThroughCalls++
	items := make([]conversation.Message, 0, len(f.items))
	for _, item := range f.items {
		if item.ID <= throughMessageID {
			items = append(items, item)
		}
	}
	return items, nil
}

func windowTestMessage(id int64, archivedAt *time.Time) conversation.Message {
	return conversation.Message{
		ImmutableModel: domain.ImmutableModel{ID: id, OwnerID: scheduleTestOwner},
		ConversationID: scheduleTestConversation,
		Role:           conversation.RoleUser,
		Content:        fmt.Sprintf("evidence message %d", id),
		ArchivedAt:     archivedAt,
	}
}

func TestDurableWindowWiring(t *testing.T) {
	t.Run("shouldReadArchivedRowsIntoRenderer", func(t *testing.T) {
		archivedAt := time.Now().UTC().Add(-time.Hour)
		items := make([]conversation.Message, 0, 9)
		for id := int64(1); id <= 9; id++ {
			var archived *time.Time
			if id >= 3 && id <= 7 {
				archived = &archivedAt
			}
			items = append(items, windowTestMessage(id, archived))
		}
		messages := &archivingDreamMessages{items: items}
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{1: seedExtractionJob(1, 9)}}
		jobs.nextID = 1
		chat := &scriptedExtractionChatClient{respond: func(int, llm.ChatRequest) (string, error) { return `{"candidates":[]}`, nil }}
		worker := newCandidateExtractionWorker(chat, jobs, messages)

		if err := worker.Handle(context.Background(), DurableMemoryPayload{JobID: 1, OwnerID: scheduleTestOwner, ConversationID: scheduleTestConversation}); err != nil {
			t.Fatalf("handle: %v", err)
		}
		if chat.calls() != 1 {
			t.Fatalf("model calls = %d, want one extraction call", chat.calls())
		}
		if messages.archiveInclusiveCalls != 1 {
			t.Fatalf("archive-inclusive reader calls = %d, want exactly one window read", messages.archiveInclusiveCalls)
		}
		// Archived rows 3..7 reached the evidence renderer: their rendered
		// units (message ids and payloads) appear in the extraction prompt.
		prompt := chat.prompt(0)
		for id := int64(3); id <= 7; id++ {
			if !strings.Contains(prompt, fmt.Sprintf("[msg %d]", id)) || !strings.Contains(prompt, fmt.Sprintf("evidence message %d", id)) {
				t.Fatalf("archived row %d did not reach the extraction prompt", id)
			}
		}
		job := jobs.jobs[1]
		if job.Status != string(memory.ExtractionCompleted) {
			t.Fatalf("job status = %q, want completed", job.Status)
		}
	})

	t.Run("shouldPreferArchiveInclusiveRangeReader", func(t *testing.T) {
		archivedAt := time.Now().UTC().Add(-time.Hour)
		items := make([]conversation.Message, 0, 9)
		for id := int64(1); id <= 9; id++ {
			var archived *time.Time
			if id >= 3 && id <= 7 {
				archived = &archivedAt
			}
			items = append(items, windowTestMessage(id, archived))
		}
		jobs := &fakeExtractionRepo{}
		worker := newCandidateExtractionWorker(nil, jobs, nil)

		// Repository WITH the archive-inclusive interface: the worker calls it
		// exactly once and never touches the active-only reads.
		withArchive := &archivingDreamMessages{items: items}
		worker.messages = withArchive
		window, err := worker.messagesThrough(context.Background(), scheduleTestOwner, scheduleTestConversation, 2, 9)
		if err != nil {
			t.Fatalf("messagesThrough: %v", err)
		}
		if withArchive.archiveInclusiveCalls != 1 {
			t.Fatalf("archive-inclusive calls = %d, want exactly one", withArchive.archiveInclusiveCalls)
		}
		if withArchive.activeThroughCalls != 0 || withArchive.activeAfterThroughCalls != 0 {
			t.Fatalf("active reads used despite archive-inclusive support: through=%d afterThrough=%d", withArchive.activeThroughCalls, withArchive.activeAfterThroughCalls)
		}
		ids := make([]int64, 0, len(window))
		for _, item := range window {
			ids = append(ids, item.ID)
		}
		if len(ids) != 7 || ids[0] != 3 || ids[len(ids)-1] != 9 {
			t.Fatalf("archive-inclusive window = %v, want ids 3..9 including archived rows", ids)
		}

		// Repository WITHOUT the interface: the legacy active read is
		// preserved unchanged.
		withoutArchive := &activeOnlyDreamMessages{items: items}
		worker.messages = withoutArchive
		fallback, err := worker.messagesThrough(context.Background(), scheduleTestOwner, scheduleTestConversation, 2, 9)
		if err != nil {
			t.Fatalf("messagesThrough fallback: %v", err)
		}
		if withoutArchive.listActiveThroughCalls != 1 {
			t.Fatalf("legacy ListActiveThrough calls = %d, want exactly one", withoutArchive.listActiveThroughCalls)
		}
		if len(fallback) == 0 || fallback[0].ID != 3 {
			t.Fatalf("legacy fallback window = %d rows, want the old after-filtered shape", len(fallback))
		}
	})
}
