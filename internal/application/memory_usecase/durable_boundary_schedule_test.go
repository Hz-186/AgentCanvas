package memory_usecase

import (
	"context"
	"fmt"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	queueinfra "agentcanvas/internal/infrastructure/queue"
)

// These tests pin the session-level debounce scheduling that replaced the
// per-boundary durable jobs: at most one pending row per conversation,
// in-place refresh, exactly one successor for a running job, and queue
// wakeups published only when a new row is created.

const (
	scheduleTestOwner        = int64(7)
	scheduleTestConversation = int64(3)
)

var scheduleTestIdle = 30 * time.Minute

// recordingDurableQueue captures every published job so tests can assert the
// exactly-once-on-create wakeup contract.
type recordingDurableQueue struct {
	published []queueinfra.Job
}

func (q *recordingDurableQueue) Publish(_ context.Context, job queueinfra.Job) error {
	q.published = append(q.published, job)
	return nil
}

func (q *recordingDurableQueue) Claim(context.Context, queueinfra.ClaimOptions) ([]queueinfra.Job, error) {
	return nil, nil
}

func (q *recordingDurableQueue) Ack(context.Context, string) error             { return nil }
func (q *recordingDurableQueue) Nack(context.Context, string, time.Time) error { return nil }

var _ queueinfra.JobQueue = (*recordingDurableQueue)(nil)

// scheduleTestRig wires the real production trigger over the fake repository
// and a recording queue.
type scheduleTestRig struct {
	jobs     *fakeExtractionRepo
	queue    *recordingDurableQueue
	messages *fakeDreamMessages
	trigger  func(context.Context, int64, int64, int)
}

func newScheduleTestRig(jobs *fakeExtractionRepo, latestMessageID int64) *scheduleTestRig {
	items := make([]conversation.Message, 0, latestMessageID)
	for id := int64(1); id <= latestMessageID; id++ {
		items = append(items, conversation.Message{ImmutableModel: domain.ImmutableModel{ID: id, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation, Role: conversation.RoleUser, Content: fmt.Sprintf("m%d", id)})
	}
	messages := &fakeDreamMessages{items: items}
	queue := &recordingDurableQueue{}
	trigger := NewDurableMemoryTrigger(queue, nil,
		DurableMemoryConfig{Enabled: true, IdleTimeout: scheduleTestIdle},
		jobs, messages)
	return &scheduleTestRig{jobs: jobs, queue: queue, messages: messages, trigger: trigger}
}

func (rig *scheduleTestRig) fire(ctx context.Context) {
	rig.trigger(ctx, scheduleTestOwner, scheduleTestConversation, 1)
}

func (rig *scheduleTestRig) durableJobs() []*memory.ExtractionJob {
	rows := make([]*memory.ExtractionJob, 0, len(rig.jobs.jobs))
	for _, job := range rig.jobs.jobs {
		if job.OwnerID == scheduleTestOwner && job.ConversationID == scheduleTestConversation && job.TriggerReason == "durable" {
			rows = append(rows, job)
		}
	}
	return rows
}

func (rig *scheduleTestRig) jobByKey(t *testing.T, key string) *memory.ExtractionJob {
	t.Helper()
	for _, job := range rig.jobs.jobs {
		if job.OwnerID == scheduleTestOwner && job.IdempotencyKey == key {
			return job
		}
	}
	t.Fatalf("no job with idempotency key %q exists", key)
	return nil
}

func assertDueAtIsIdleAway(t *testing.T, dueAt *time.Time) {
	t.Helper()
	if dueAt == nil {
		t.Fatal("scheduled job must carry a due_at")
	}
	delta := time.Until(*dueAt) - scheduleTestIdle
	if delta < -2*time.Minute || delta > 2*time.Minute {
		t.Fatalf("due_at = %s, want now+%s (±2m)", dueAt.Format(time.RFC3339), scheduleTestIdle)
	}
}

func TestScheduleBoundary(t *testing.T) {
	t.Run("shouldCreateInitialJobWhenConversationEmpty", func(t *testing.T) {
		rig := newScheduleTestRig(&fakeExtractionRepo{}, 300)

		rig.fire(context.Background())

		rows := rig.durableJobs()
		if len(rows) != 1 {
			t.Fatalf("durable job rows = %d, want exactly one initial row", len(rows))
		}
		job := rows[0]
		if want := "durable:7:3:initial"; job.IdempotencyKey != want {
			t.Fatalf("idempotency key = %q, want %q", job.IdempotencyKey, want)
		}
		if job.Status != string(memory.ExtractionPending) {
			t.Fatalf("initial job status = %q, want pending", job.Status)
		}
		if job.ThroughMessageID != 300 {
			t.Fatalf("initial job through = %d, want the latest active message 300", job.ThroughMessageID)
		}
		assertDueAtIsIdleAway(t, job.DueAt)
		// Queue wakeup is published exactly once for the created row, due at
		// the row's due_at.
		if len(rig.queue.published) != 1 {
			t.Fatalf("queue publishes = %d, want exactly one for the created row", len(rig.queue.published))
		}
		published := rig.queue.published[0]
		if published.AvailableAt != *job.DueAt {
			t.Fatalf("published AvailableAt = %s, want row DueAt %s", published.AvailableAt, job.DueAt)
		}
		if published.Type != DurableMemoryJobType {
			t.Fatalf("published type = %q, want %q", published.Type, DurableMemoryJobType)
		}
		if published.Payload["job_id"] != job.ID || published.Payload["owner_id"] != scheduleTestOwner || published.Payload["conversation_id"] != scheduleTestConversation {
			t.Fatalf("published payload = %+v, want job/owner/conversation of the created row", published.Payload)
		}
	})

	t.Run("shouldRefreshPendingRowInPlace", func(t *testing.T) {
		earlierDue := time.Now().UTC().Add(5 * time.Minute)
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{7: {
			BaseModel: domain.BaseModel{ID: 7, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			IdempotencyKey: "durable:7:3:initial", TriggerReason: "durable",
			ThroughMessageID: 100, Status: string(memory.ExtractionPending), DueAt: &earlierDue,
		}}}
		jobs.nextID = 7
		rig := newScheduleTestRig(jobs, 300)

		rig.fire(context.Background())

		if got := len(rig.durableJobs()); got != 1 {
			t.Fatalf("durable job rows = %d, want the same single pending row", got)
		}
		job := rig.jobByKey(t, "durable:7:3:initial")
		if job.ID != 7 {
			t.Fatalf("refresh created a different row: id=%d", job.ID)
		}
		if job.ThroughMessageID != 300 {
			t.Fatalf("refreshed through = %d, want 300", job.ThroughMessageID)
		}
		assertDueAtIsIdleAway(t, job.DueAt)
		if job.Status != string(memory.ExtractionPending) {
			t.Fatalf("refreshed row status = %q, want pending", job.Status)
		}
		if len(rig.queue.published) != 0 {
			t.Fatalf("queue publishes on refresh path = %d, want zero", len(rig.queue.published))
		}
	})

	t.Run("shouldCreateSingleSuccessorForRunningJob", func(t *testing.T) {
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{9: {
			BaseModel: domain.BaseModel{ID: 9, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			IdempotencyKey: "durable:7:3:initial", TriggerReason: "durable",
			ThroughMessageID: 100, Status: string(memory.ExtractionRunning),
		}}}
		jobs.nextID = 9
		rig := newScheduleTestRig(jobs, 300)

		// Two consecutive schedule calls while the job runs: the first creates
		// the successor, the second refreshes that successor in place.
		rig.fire(context.Background())
		rig.fire(context.Background())

		rows := rig.durableJobs()
		if len(rows) != 2 {
			t.Fatalf("durable job rows = %d, want running row + exactly one successor", len(rows))
		}
		successor := rig.jobByKey(t, "durable:7:3:after-job:9")
		if successor.Status != string(memory.ExtractionPending) || successor.ThroughMessageID != 300 {
			t.Fatalf("successor row = status %q through %d, want pending through 300", successor.Status, successor.ThroughMessageID)
		}
		running := rig.jobByKey(t, "durable:7:3:initial")
		if running.ID != 9 || running.Status != string(memory.ExtractionRunning) || running.ThroughMessageID != 100 {
			t.Fatalf("running row mutated: id=%d status=%q through=%d", running.ID, running.Status, running.ThroughMessageID)
		}
		if len(rig.queue.published) != 1 {
			t.Fatalf("queue publishes = %d, want exactly one for the single successor", len(rig.queue.published))
		}
	})

	t.Run("shouldCreateNewRowAfterTerminalJob", func(t *testing.T) {
		completedAt := time.Now().UTC().Add(-time.Hour)
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{11: {
			BaseModel: domain.BaseModel{ID: 11, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			IdempotencyKey: "durable:7:3:initial", TriggerReason: "durable",
			ThroughMessageID: 100, Status: string(memory.ExtractionCompleted), CompletedAt: &completedAt,
		}}}
		jobs.nextID = 11
		rig := newScheduleTestRig(jobs, 300)

		rig.fire(context.Background())

		if got := len(rig.durableJobs()); got != 2 {
			t.Fatalf("durable job rows = %d, want terminal row + one new row", got)
		}
		successor := rig.jobByKey(t, "durable:7:3:after-job:11")
		if successor.Status != string(memory.ExtractionPending) || successor.ThroughMessageID != 300 {
			t.Fatalf("new row = status %q through %d, want pending through 300", successor.Status, successor.ThroughMessageID)
		}
		assertDueAtIsIdleAway(t, successor.DueAt)
		if len(rig.queue.published) != 1 {
			t.Fatalf("queue publishes = %d, want exactly one for the new row", len(rig.queue.published))
		}
	})

	t.Run("shouldFallbackToSuccessorOnRefreshRace", func(t *testing.T) {
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{7: {
			BaseModel: domain.BaseModel{ID: 7, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			IdempotencyKey: "durable:7:3:initial", TriggerReason: "durable",
			ThroughMessageID: 100, Status: string(memory.ExtractionPending),
		}}}
		jobs.nextID = 7
		// The scheduler's latest-row read observes pending; before the
		// conditional refresh lands, a worker claims the row (status flips to
		// running), so the refresh affects zero rows.
		jobs.onLatestDurableJob = func(r *fakeExtractionRepo) {
			r.jobs[7].Status = string(memory.ExtractionRunning)
		}
		rig := newScheduleTestRig(jobs, 300)

		rig.fire(context.Background())

		claimed := rig.jobByKey(t, "durable:7:3:initial")
		if claimed.Status != string(memory.ExtractionRunning) || claimed.ThroughMessageID != 100 {
			t.Fatalf("claimed row mutated: status=%q through=%d", claimed.Status, claimed.ThroughMessageID)
		}
		rows := rig.durableJobs()
		if len(rows) != 2 {
			t.Fatalf("durable job rows = %d, want claimed row + exactly one successor (no duplicates)", len(rows))
		}
		successor := rig.jobByKey(t, "durable:7:3:after-job:7")
		if successor.Status != string(memory.ExtractionPending) || successor.ThroughMessageID != 300 {
			t.Fatalf("successor row = status %q through %d, want pending through 300", successor.Status, successor.ThroughMessageID)
		}
		if len(rig.queue.published) != 1 {
			t.Fatalf("queue publishes = %d, want exactly one for the successor", len(rig.queue.published))
		}
	})

	t.Run("shouldDeduplicateConcurrentSuccessorViaUniqueKey", func(t *testing.T) {
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{9: {
			BaseModel: domain.BaseModel{ID: 9, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			IdempotencyKey: "durable:7:3:initial", TriggerReason: "durable",
			ThroughMessageID: 100, Status: string(memory.ExtractionRunning),
		}}}
		jobs.nextID = 9
		// A rival scheduler inserts the same successor right after our
		// latest-row read: our INSERT must hit the unique key and re-read the
		// existing row instead of duplicating it.
		jobs.onLatestDurableJob = func(r *fakeExtractionRepo) {
			due := time.Now().UTC().Add(scheduleTestIdle)
			_ = r.Create(context.Background(), &memory.ExtractionJob{
				BaseModel:        domain.BaseModel{OwnerID: scheduleTestOwner},
				ConversationID:   scheduleTestConversation,
				IdempotencyKey:   "durable:7:3:after-job:9",
				TriggerReason:    "durable",
				ThroughMessageID: 300,
				Status:           string(memory.ExtractionPending),
				DueAt:            &due,
			})
		}
		rig := newScheduleTestRig(jobs, 300)

		rig.fire(context.Background())

		matches := 0
		for _, job := range rig.jobs.jobs {
			if job.IdempotencyKey == "durable:7:3:after-job:9" {
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("rows with successor key = %d, want exactly one after dedup", matches)
		}
		if got := len(rig.durableJobs()); got != 2 {
			t.Fatalf("durable job rows = %d, want running row + the rival successor only", got)
		}
		if len(rig.queue.published) != 0 {
			t.Fatalf("queue publishes = %d, want zero when our insert lost the race", len(rig.queue.published))
		}
	})

	t.Run("shouldRecognizeLegacyFormatRowsByConversation", func(t *testing.T) {
		// Legacy rows carry the retired key format durable:<owner>:<conv>:<through>.
		// Conversation-scoped lookups must find them without any key parsing.
		legacyKey := fmt.Sprintf("durable:%d:%d:400", scheduleTestOwner, scheduleTestConversation)
		completedAt := time.Now().UTC().Add(-time.Hour)
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{4: {
			BaseModel: domain.BaseModel{ID: 4, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			IdempotencyKey: legacyKey, TriggerReason: "durable",
			ThroughMessageID: 400, Status: string(memory.ExtractionCompleted), CompletedAt: &completedAt,
		}}}
		jobs.nextID = 4
		rig := newScheduleTestRig(jobs, 600)

		rig.fire(context.Background())

		successor := rig.jobByKey(t, "durable:7:3:after-job:4")
		if successor.Status != string(memory.ExtractionPending) || successor.ThroughMessageID != 600 {
			t.Fatalf("successor after legacy row = status %q through %d, want pending through 600", successor.Status, successor.ThroughMessageID)
		}

		// The window-start lookup also recognizes the legacy completed row.
		worker := NewDurableMemoryWorker(nil, rig.messages, jobs, nil, DurableMemoryConfig{Enabled: true}, "test-worker")
		boundary := worker.previousBoundary(context.Background(), &memory.ExtractionJob{
			BaseModel: domain.BaseModel{ID: 99, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			TriggerReason: "durable", ThroughMessageID: 600, Status: string(memory.ExtractionRunning),
		})
		if boundary != 400 {
			t.Fatalf("window start after legacy row = %d, want the legacy through 400", boundary)
		}
	})
}

func TestBoundaryWindow(t *testing.T) {
	t.Run("shouldStartWindowAfterLatestCompletedDurableJob", func(t *testing.T) {
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{}}
		// The conversation's latest completed durable job: through=500.
		jobs.jobs[5] = &memory.ExtractionJob{
			BaseModel: domain.BaseModel{ID: 5, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			IdempotencyKey: "durable:7:3:initial", TriggerReason: "durable",
			ThroughMessageID: 500, Status: string(memory.ExtractionCompleted),
		}
		// 250 unrelated completed jobs: other conversations, newer IDs and
		// larger through values. A bounded owner-wide scan (or a MAX(through)
		// aggregate) would surface one of these instead of 500.
		for i := 0; i < 250; i++ {
			id := int64(6 + i)
			jobs.jobs[id] = &memory.ExtractionJob{
				BaseModel: domain.BaseModel{ID: id, OwnerID: scheduleTestOwner}, ConversationID: int64(1000 + i),
				IdempotencyKey: fmt.Sprintf("durable:7:%d:%d", 1000+i, 9000+i), TriggerReason: "durable",
				ThroughMessageID: 9000 + int64(i), Status: string(memory.ExtractionCompleted),
			}
		}
		jobs.nextID = 255
		worker := NewDurableMemoryWorker(nil, &fakeDreamMessages{}, jobs, nil, DurableMemoryConfig{Enabled: true}, "test-worker")
		current := &memory.ExtractionJob{
			BaseModel: domain.BaseModel{ID: 300, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			IdempotencyKey: "durable:7:3:after-job:5", TriggerReason: "durable",
			ThroughMessageID: 600, Status: string(memory.ExtractionRunning),
		}

		boundary := worker.previousBoundary(context.Background(), current)

		// Window start is the latest completed through: the new window covers
		// messages strictly after 500 regardless of the unrelated jobs.
		if boundary != 500 {
			t.Fatalf("window start = %d, want the conversation's latest completed through 500", boundary)
		}
		if jobs.listByStatusCalls != 0 {
			t.Fatalf("previousBoundary used the retired %d-row ListByStatus scan %d time(s)", 200, jobs.listByStatusCalls)
		}
	})

	t.Run("shouldKeepOutOfOrderShadowRule", func(t *testing.T) {
		jobs := &fakeExtractionRepo{jobs: map[int64]*memory.ExtractionJob{8: {
			BaseModel: domain.BaseModel{ID: 8, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			IdempotencyKey: "durable:7:3:initial", TriggerReason: "durable",
			ThroughMessageID: 700, Status: string(memory.ExtractionCompleted),
		}}}
		jobs.nextID = 8
		worker := NewDurableMemoryWorker(nil, &fakeDreamMessages{}, jobs, nil, DurableMemoryConfig{Enabled: true}, "test-worker")
		current := &memory.ExtractionJob{
			BaseModel: domain.BaseModel{ID: 9, OwnerID: scheduleTestOwner}, ConversationID: scheduleTestConversation,
			IdempotencyKey: "durable:7:3:after-job:8", TriggerReason: "durable",
			ThroughMessageID: 600, Status: string(memory.ExtractionRunning),
		}

		boundary := worker.previousBoundary(context.Background(), current)

		// Completion order is not message order: a completed boundary at or
		// beyond the current one shadows it into an empty window.
		if boundary < current.ThroughMessageID {
			t.Fatalf("shadow boundary = %d, want >= current through %d", boundary, current.ThroughMessageID)
		}
		if boundary != 700 {
			t.Fatalf("shadow boundary = %d, want the latest completed through 700", boundary)
		}
	})
}

func TestNewDurableMemoryTriggerIgnoresIdleConversations(t *testing.T) {
	jobs := &fakeExtractionRepo{}
	trigger := NewDurableMemoryTrigger(&recordingDurableQueue{}, nil,
		DurableMemoryConfig{Enabled: true, IdleTimeout: scheduleTestIdle},
		jobs, &fakeDreamMessages{})

	trigger(context.Background(), scheduleTestOwner, scheduleTestConversation, 1)

	if len(jobs.jobs) != 0 {
		t.Fatalf("conversation without messages scheduled %d job(s)", len(jobs.jobs))
	}
}
