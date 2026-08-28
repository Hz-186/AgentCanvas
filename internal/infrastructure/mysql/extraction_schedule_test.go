package mysql

import (
	"context"
	"database/sql/driver"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"

	"github.com/DATA-DOG/go-sqlmock"
	mysqldriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// These tests pin the MySQL side of the session-level debounce scheduling:
// the FOR UPDATE locking read is scoped to the conversation's latest durable
// row (never a table-wide lock), the pending refresh is a conditional UPDATE,
// the window-start lookup is a targeted conversation query, and a unique-key
// conflict falls back to re-reading the existing row. The behavioral branch
// coverage lives in memory_usecase (hand-written fakes); this file asserts
// the SQL shape and the transactional branch flow through sqlmock, plus a
// live-server integration subtest gated on AGENTCANVAS_TEST_MYSQL_DSN.

func extractionJobColumns() []string {
	return []string{
		"id", "owner_id", "created_at", "updated_at",
		"conversation_id", "project_id", "idempotency_key", "trigger_reason",
		"source_message_ids", "through_message_id", "status", "due_at",
		"attempt_count", "phase2_attempt_count", "locked_by", "locked_at",
		"lease_expires_at", "result_json", "error_message", "completed_at",
	}
}

func extractionJobRow(id, ownerID, conversationID int64, key, status string, through int64) []driver.Value {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	return []driver.Value{
		id, ownerID, now, now,
		conversationID, int64(0), key, "durable",
		nil, through, status, nil,
		0, 0, "", nil,
		nil, nil, "", nil,
	}
}

// duplicateEntryError is the MySQL 1062 the server returns when the
// (owner_id, idempotency_key) unique key rejects a rival successor insert.
func duplicateEntryError() error {
	return &mysqldriver.MySQLError{Number: 1062, Message: "Duplicate entry for key 'uq_memory_extraction_idempotency'"}
}

// lockingReadSQL is the conversation-scoped locking read; gorm renders the
// integer LIMIT as a bound argument and clause.Locking appends FOR UPDATE.
func lockingReadSQL() string {
	return regexp.QuoteMeta("SELECT * FROM `memory_extraction_jobs` WHERE owner_id = ? AND conversation_id = ? AND trigger_reason = ? ORDER BY id DESC LIMIT ?") + " FOR UPDATE"
}

func TestScheduleBoundaryRepo(t *testing.T) {
	ownerID := int64(1)
	conversationID := int64(2)
	dueAt := time.Date(2026, 8, 28, 6, 0, 0, 0, time.UTC)

	t.Run("shouldRefreshPendingRowInsideLockingTransaction", func(t *testing.T) {
		db, mock := newMemorySchemaMockDB(t)
		repo := NewExtractionJobRepository(db)

		mock.ExpectBegin()
		mock.ExpectQuery(lockingReadSQL()).
			WithArgs(ownerID, conversationID, "durable", 1).
			WillReturnRows(sqlmockRows(extractionJobRow(7, ownerID, conversationID, "durable:1:2:initial", "pending", 100)))
		mock.ExpectExec(regexp.QuoteMeta("UPDATE `memory_extraction_jobs` SET")).
			WithArgs(sqlmock.AnyArg(), int64(300), sqlmock.AnyArg(), int64(7), ownerID, "pending").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		job, created, err := repo.ScheduleDurableBoundary(context.Background(), ownerID, conversationID, 300, dueAt)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if created {
			t.Fatal("pending refresh must not report a created row")
		}
		if job == nil || job.ID != 7 || job.ThroughMessageID != 300 || job.DueAt == nil || !job.DueAt.Equal(dueAt) {
			t.Fatalf("refreshed job = %+v, want row 7 with through 300 and the new due_at", job)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("shouldCreateInitialRowWhenConversationEmpty", func(t *testing.T) {
		db, mock := newMemorySchemaMockDB(t)
		repo := NewExtractionJobRepository(db)

		mock.ExpectBegin()
		mock.ExpectQuery(lockingReadSQL()).
			WithArgs(ownerID, conversationID, "durable", 1).
			WillReturnRows(sqlmock.NewRows(extractionJobColumns()))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `memory_extraction_jobs`")).
			WillReturnResult(sqlmock.NewResult(3, 1))
		mock.ExpectCommit()

		job, created, err := repo.ScheduleDurableBoundary(context.Background(), ownerID, conversationID, 300, dueAt)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if !created {
			t.Fatal("initial row must be reported as created")
		}
		if job == nil || job.IdempotencyKey != "durable:1:2:initial" || job.Status != "pending" || job.ThroughMessageID != 300 {
			t.Fatalf("initial job = %+v, want key durable:1:2:initial pending through 300", job)
		}
		if job.DueAt == nil || !job.DueAt.Equal(dueAt) {
			t.Fatalf("initial job due_at = %v, want %s", job.DueAt, dueAt)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("shouldCreateSuccessorWhenLockingReadObservesRunning", func(t *testing.T) {
		db, mock := newMemorySchemaMockDB(t)
		repo := NewExtractionJobRepository(db)

		mock.ExpectBegin()
		mock.ExpectQuery(lockingReadSQL()).
			WithArgs(ownerID, conversationID, "durable", 1).
			WillReturnRows(sqlmockRows(extractionJobRow(9, ownerID, conversationID, "durable:1:2:initial", "running", 100)))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `memory_extraction_jobs`")).
			WillReturnResult(sqlmock.NewResult(12, 1))
		mock.ExpectCommit()

		job, created, err := repo.ScheduleDurableBoundary(context.Background(), ownerID, conversationID, 300, dueAt)
		if err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if !created || job == nil || job.IdempotencyKey != "durable:1:2:after-job:9" {
			t.Fatalf("successor = created=%v job=%+v, want created row keyed after-job:9", created, job)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("shouldFallbackToSuccessorWhenRefreshAffectsZeroRows", func(t *testing.T) {
		db, mock := newMemorySchemaMockDB(t)
		repo := NewExtractionJobRepository(db)

		// Defensive branch: an implementation whose locking read returned
		// pending can still lose the row before the conditional UPDATE lands;
		// zero affected rows must fall back to a successor, not an error.
		mock.ExpectBegin()
		mock.ExpectQuery(lockingReadSQL()).
			WithArgs(ownerID, conversationID, "durable", 1).
			WillReturnRows(sqlmockRows(extractionJobRow(7, ownerID, conversationID, "durable:1:2:initial", "pending", 100)))
		mock.ExpectExec(regexp.QuoteMeta("UPDATE `memory_extraction_jobs` SET")).
			WithArgs(sqlmock.AnyArg(), int64(300), sqlmock.AnyArg(), int64(7), ownerID, "pending").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `memory_extraction_jobs`")).
			WillReturnResult(sqlmock.NewResult(13, 1))
		mock.ExpectCommit()

		job, created, err := repo.ScheduleDurableBoundary(context.Background(), ownerID, conversationID, 300, dueAt)
		if err != nil {
			t.Fatalf("zero-row refresh must not error: %v", err)
		}
		if !created || job == nil || job.IdempotencyKey != "durable:1:2:after-job:7" {
			t.Fatalf("fallback successor = created=%v job=%+v, want created row keyed after-job:7", created, job)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("shouldRereadExistingRowOnUniqueKeyConflict", func(t *testing.T) {
		db, mock := newMemorySchemaMockDB(t)
		repo := NewExtractionJobRepository(db)

		mock.ExpectBegin()
		mock.ExpectQuery(lockingReadSQL()).
			WithArgs(ownerID, conversationID, "durable", 1).
			WillReturnRows(sqlmockRows(extractionJobRow(9, ownerID, conversationID, "durable:1:2:initial", "running", 100)))
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `memory_extraction_jobs`")).
			WillReturnError(duplicateEntryError())
		mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `memory_extraction_jobs` WHERE owner_id = ? AND idempotency_key = ?")).
			WithArgs(ownerID, "durable:1:2:after-job:9", 1).
			WillReturnRows(sqlmockRows(extractionJobRow(14, ownerID, conversationID, "durable:1:2:after-job:9", "pending", 300)))
		mock.ExpectCommit()

		job, created, err := repo.ScheduleDurableBoundary(context.Background(), ownerID, conversationID, 300, dueAt)
		if err != nil {
			t.Fatalf("unique-key conflict must resolve to the existing row: %v", err)
		}
		if created {
			t.Fatal("duplicate insert must not be reported as created")
		}
		if job == nil || job.ID != 14 {
			t.Fatalf("existing row = %+v, want the rival successor id 14", job)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("shouldScopeRefreshToPendingRows", func(t *testing.T) {
		db, mock := newMemorySchemaMockDB(t)
		repo := NewExtractionJobRepository(db)

		// GORM wraps the single conditional UPDATE in its default transaction.
		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta("UPDATE `memory_extraction_jobs` SET")).
			WithArgs(sqlmock.AnyArg(), int64(300), sqlmock.AnyArg(), int64(7), ownerID, "pending").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()

		refreshed, err := repo.RefreshPendingBoundary(context.Background(), ownerID, 7, 300, dueAt)
		if err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if refreshed {
			t.Fatal("zero affected rows must report no refresh")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("shouldReadWindowStartByConversationOnly", func(t *testing.T) {
		db, mock := newMemorySchemaMockDB(t)
		repo := NewExtractionJobRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta("SELECT `through_message_id` FROM `memory_extraction_jobs` WHERE owner_id = ? AND conversation_id = ? AND trigger_reason = ? AND status = ? ORDER BY id DESC LIMIT ?")).
			WithArgs(ownerID, conversationID, "durable", "completed", 1).
			WillReturnRows(sqlmock.NewRows([]string{"through_message_id"}).AddRow(500))

		through, err := repo.LatestCompletedDurableThrough(context.Background(), ownerID, conversationID)
		if err != nil {
			t.Fatalf("window start lookup: %v", err)
		}
		if through != 500 {
			t.Fatalf("window start = %d, want the latest completed through 500", through)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

func sqlmockRows(row ...[]driver.Value) *sqlmock.Rows {
	rows := sqlmock.NewRows(extractionJobColumns())
	for _, item := range row {
		rows.AddRow(item...)
	}
	return rows
}

func TestScheduleBoundaryIntegration(t *testing.T) {
	dsn := os.Getenv("AGENTCANVAS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set AGENTCANVAS_TEST_MYSQL_DSN to run MySQL integration tests")
	}
	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := NewExtractionJobRepository(db)
	ownerID := time.Now().UnixNano()
	conversationID := int64(1)
	cleanup := func() {
		_ = db.Exec("DELETE FROM memory_extraction_jobs WHERE owner_id = ?", ownerID).Error
	}
	cleanup()
	t.Cleanup(cleanup)

	seedJob := func(key, status string, through int64) *memory.ExtractionJob {
		job := &memory.ExtractionJob{
			BaseModel:        domain.BaseModel{OwnerID: ownerID},
			ConversationID:   conversationID,
			IdempotencyKey:   key,
			TriggerReason:    "durable",
			ThroughMessageID: through,
			Status:           status,
		}
		if err := repo.Create(ctx, job); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
		return job
	}

	durableRows := func() []memory.ExtractionJob {
		var rows []memory.ExtractionJob
		if err := db.Where("owner_id = ? AND conversation_id = ? AND trigger_reason = ?", ownerID, conversationID, "durable").Order("id ASC").Find(&rows).Error; err != nil {
			t.Fatalf("list rows: %v", err)
		}
		return rows
	}

	t.Run("shouldCreateInitialJobWhenConversationEmpty", func(t *testing.T) {
		conv := conversationID + 100
		due := time.Now().UTC().Add(30 * time.Minute)
		job, created, err := repo.ScheduleDurableBoundary(ctx, ownerID, conv, 300, due)
		if err != nil || !created {
			t.Fatalf("initial schedule: created=%v err=%v", created, err)
		}
		if job.IdempotencyKey != fmt.Sprintf("durable:%d:%d:initial", ownerID, conv) || job.Status != "pending" || job.ThroughMessageID != 300 {
			t.Fatalf("initial job = %+v", job)
		}
	})

	t.Run("shouldRefreshPendingRowInPlace", func(t *testing.T) {
		conv := conversationID + 200
		seed := seedJob(fmt.Sprintf("durable:%d:%d:initial", ownerID, conv), "pending", 100)
		due := time.Now().UTC().Add(30 * time.Minute)
		job, created, err := repo.ScheduleDurableBoundary(ctx, ownerID, conv, 300, due)
		if err != nil || created {
			t.Fatalf("refresh schedule: created=%v err=%v", created, err)
		}
		if job.ID != seed.ID || job.ThroughMessageID != 300 {
			t.Fatalf("refreshed job = %+v, want row %d through 300", job, seed.ID)
		}
		var count int64
		_ = db.Model(&memory.ExtractionJob{}).Where("owner_id = ? AND conversation_id = ?", ownerID, conv).Count(&count).Error
		if count != 1 {
			t.Fatalf("conversation rows = %d, want the single refreshed row", count)
		}
	})

	t.Run("shouldCreateSingleSuccessorForRunningJob", func(t *testing.T) {
		conv := conversationID + 300
		seed := seedJob(fmt.Sprintf("durable:%d:%d:initial", ownerID, conv), "running", 100)
		due := time.Now().UTC().Add(30 * time.Minute)
		for i := 0; i < 2; i++ {
			job, created, err := repo.ScheduleDurableBoundary(ctx, ownerID, conv, 300, due)
			if err != nil {
				t.Fatalf("schedule #%d: %v", i+1, err)
			}
			if i == 0 && !created {
				t.Fatal("first schedule must create the successor")
			}
			if i == 1 && created {
				t.Fatal("second schedule must refresh, not create")
			}
			if job == nil || job.IdempotencyKey != fmt.Sprintf("durable:%d:%d:after-job:%d", ownerID, conv, seed.ID) {
				t.Fatalf("schedule #%d job = %+v", i+1, job)
			}
		}
		rows := durableRows()
		if len(rows) != 2 {
			t.Fatalf("conversation rows = %d, want running row + exactly one successor", len(rows))
		}
	})

	t.Run("shouldCreateNewRowAfterTerminalJob", func(t *testing.T) {
		conv := conversationID + 400
		seed := seedJob(fmt.Sprintf("durable:%d:%d:initial", ownerID, conv), "completed", 100)
		due := time.Now().UTC().Add(30 * time.Minute)
		job, created, err := repo.ScheduleDurableBoundary(ctx, ownerID, conv, 300, due)
		if err != nil || !created {
			t.Fatalf("terminal schedule: created=%v err=%v", created, err)
		}
		if job.IdempotencyKey != fmt.Sprintf("durable:%d:%d:after-job:%d", ownerID, conv, seed.ID) {
			t.Fatalf("new row key = %q, want after-job:%d", job.IdempotencyKey, seed.ID)
		}
	})

	t.Run("shouldRecognizeLegacyFormatRowsByConversation", func(t *testing.T) {
		conv := conversationID + 500
		// Legacy rows carry the retired key format durable:<owner>:<conv>:<through>;
		// the conversation-scoped queries must find them without key parsing.
		legacy := seedJob(fmt.Sprintf("durable:%d:%d:400", ownerID, conv), "completed", 400)
		through, err := repo.LatestCompletedDurableThrough(ctx, ownerID, conv)
		if err != nil {
			t.Fatalf("window lookup over legacy row: %v", err)
		}
		if through != 400 {
			t.Fatalf("window start = %d, want the legacy through 400", through)
		}
		due := time.Now().UTC().Add(30 * time.Minute)
		job, created, err := repo.ScheduleDurableBoundary(ctx, ownerID, conv, 600, due)
		if err != nil || !created {
			t.Fatalf("schedule over legacy row: created=%v err=%v", created, err)
		}
		if job.IdempotencyKey != fmt.Sprintf("durable:%d:%d:after-job:%d", ownerID, conv, legacy.ID) {
			t.Fatalf("successor key = %q, want after-job:%d", job.IdempotencyKey, legacy.ID)
		}
	})
}
