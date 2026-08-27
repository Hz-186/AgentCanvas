package mysql

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"

	"github.com/DATA-DOG/go-sqlmock"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func newMemorySchemaMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db, mock
}

// memoryWriteJobColumns lists the SELECT * projection of memory_write_jobs in
// schema field order; GORM scans rows by column name, not position.
func memoryWriteJobColumns() []string {
	return []string{
		"id", "owner_id", "created_at", "updated_at",
		"idempotency_key", "source", "payload_json", "status", "attempt_count",
		"due_at", "locked_by", "locked_at", "lease_expires_at",
		"error_message", "completed_at",
	}
}

func TestMemoryArtifactRepositoryShouldCreateVersionedArtifact(t *testing.T) {
	db, mock := newMemorySchemaMockDB(t)
	repo := NewMemoryArtifactRepository(db)
	ctx := context.Background()

	artifact := &memory.MemoryArtifact{
		BaseModel: domain.BaseModel{OwnerID: 1},
		Kind:      memory.ArtifactKindSummary,
		Version:   2,
		Content:   "summary content",
		Source:    "manual",
		Checksum:  "sha256",
	}
	// Exact owner/kind/version/checksum args; created_at/updated_at and
	// protected_at/consolidated_at are driver values set by the repository.
	// GORM wraps the single INSERT in a default transaction.
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `memory_artifacts`")).
		WithArgs(1, sqlmock.AnyArg(), sqlmock.AnyArg(), memory.ArtifactKindSummary, 2, "summary content", "manual", "sha256", nil, nil).
		WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectCommit()
	if err := repo.Create(ctx, artifact); err != nil {
		t.Fatalf("create valid artifact: %v", err)
	}
	if artifact.ID != 42 {
		t.Fatalf("artifact id = %d, want 42", artifact.ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}

	// Invalid artifact (missing checksum) must be rejected with
	// gorm.ErrInvalidData and zero database interaction.
	invalid := &memory.MemoryArtifact{
		BaseModel: domain.BaseModel{OwnerID: 1},
		Kind:      memory.ArtifactKindSummary,
		Version:   1,
	}
	if err := repo.Create(ctx, invalid); !errors.Is(err, gorm.ErrInvalidData) {
		t.Fatalf("missing checksum error = %v, want ErrInvalidData", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("invalid artifact touched the database: %v", err)
	}
}

func TestMemoryWriteJobRepositoryShouldRejectUnknownSource(t *testing.T) {
	db, mock := newMemorySchemaMockDB(t)
	repo := NewMemoryWriteJobRepository(db)
	ctx := context.Background()

	// Unknown source: rejected by Validate with zero database interaction.
	err := repo.Create(ctx, &memory.MemoryWriteJob{
		BaseModel:      domain.BaseModel{OwnerID: 1},
		IdempotencyKey: "key",
		Source:         "unknown",
	})
	if err == nil {
		t.Fatal("unknown write job source accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unknown source touched the database: %v", err)
	}

	// Manual source: exactly one INSERT (with the OnConflict clause emitted by
	// the repository) and the status defaulted to pending.
	job := &memory.MemoryWriteJob{
		BaseModel:      domain.BaseModel{OwnerID: 1},
		IdempotencyKey: "key",
		Source:         "manual",
	}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `memory_write_jobs`")).
		WithArgs(1, sqlmock.AnyArg(), sqlmock.AnyArg(), "key", "manual", memory.WriteJobStatusPending, 0, nil, "", nil, nil, "", nil).
		WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("create manual job: %v", err)
	}
	if job.ID != 7 || job.Status != memory.WriteJobStatusPending {
		t.Fatalf("created job = %+v, want id 7 status pending", job)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMemoryWriteJobRepositoryShouldHandleDuplicateIdempotencyKey(t *testing.T) {
	db, mock := newMemorySchemaMockDB(t)
	repo := NewMemoryWriteJobRepository(db)
	ctx := context.Background()

	job := &memory.MemoryWriteJob{
		BaseModel:      domain.BaseModel{OwnerID: 1},
		IdempotencyKey: "dup-key",
		Source:         "manual",
	}
	// Duplicate key: the ON DUPLICATE KEY UPDATE insert affects zero rows...
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `memory_write_jobs`")).
		WithArgs(1, sqlmock.AnyArg(), sqlmock.AnyArg(), "dup-key", "manual", memory.WriteJobStatusPending, 0, nil, "", nil, nil, "", nil).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	now := time.Now().UTC()
	// ...so the repository reads the existing row back.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `memory_write_jobs` WHERE owner_id = ? AND idempotency_key = ?")).
		WithArgs(1, "dup-key", 1).
		WillReturnRows(sqlmock.NewRows(memoryWriteJobColumns()).
			AddRow(42, 1, now, now, "dup-key", "manual", nil, memory.WriteJobStatusCompleted, 2, nil, "", nil, nil, nil, nil))
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("duplicate create: %v", err)
	}
	if job.ID != 42 || job.Status != memory.WriteJobStatusCompleted {
		t.Fatalf("existing job not returned: %+v", job)
	}
	// ExpectationsWereMet enforces exactly one INSERT was attempted.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMemoryWriteJobRepositoryShouldClaimLeaseInOrder(t *testing.T) {
	db, mock := newMemorySchemaMockDB(t)
	repo := NewMemoryWriteJobRepository(db)
	ctx := context.Background()
	now := time.Now().UTC()
	leaseUntil := now.Add(time.Minute)

	// Invalid worker/lease: ErrInvalidData with no SELECT/UPDATE issued. The
	// transaction begin/rollback are the only database calls.
	for _, tc := range []struct {
		name       string
		workerID   string
		leaseUntil time.Time
	}{
		{"empty worker", "", leaseUntil},
		{"lease not after now", "worker-1", now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock.ExpectBegin()
			mock.ExpectRollback()
			jobs, err := repo.ClaimPending(ctx, tc.workerID, now, tc.leaseUntil, 10)
			if !errors.Is(err, gorm.ErrInvalidData) {
				t.Fatalf("error = %v, want ErrInvalidData", err)
			}
			if jobs != nil {
				t.Fatalf("jobs = %+v, want nil", jobs)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet expectations: %v", err)
			}
		})
	}

	// Valid claim: deterministic ordering (due_at IS NOT NULL ASC, due_at ASC,
	// id ASC) with FOR UPDATE, one UPDATE per claimed row with the exact
	// workerID, and rows returned in claim order.
	dueAt := now.Add(2 * time.Hour)
	claimSelect := regexp.QuoteMeta(
		"SELECT * FROM `memory_write_jobs` WHERE (status = ? AND (due_at IS NULL OR due_at <= ?)) OR (status = ? AND lease_expires_at <= ?) ORDER BY due_at IS NOT NULL ASC, due_at ASC, id ASC LIMIT ?",
	) + " FOR UPDATE"
	mock.ExpectBegin()
	mock.ExpectQuery(claimSelect).
		WithArgs(memory.WriteJobStatusPending, now, memory.WriteJobStatusRunning, now, 10).
		WillReturnRows(sqlmock.NewRows(memoryWriteJobColumns()).
			AddRow(10, 1, now, now, "k1", "manual", nil, memory.WriteJobStatusPending, 0, nil, "", nil, nil, nil, nil).
			AddRow(20, 1, now, now, "k2", "manual", nil, memory.WriteJobStatusPending, 0, dueAt, "", nil, nil, nil, nil))
	// GORM sorts map update keys: attempt_count, lease_expires_at, locked_at,
	// locked_by, status, updated_at, then WHERE id.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `memory_write_jobs` SET")).
		WithArgs(1, sqlmock.AnyArg(), sqlmock.AnyArg(), "worker-1", memory.WriteJobStatusRunning, sqlmock.AnyArg(), 10).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `memory_write_jobs` SET")).
		WithArgs(1, sqlmock.AnyArg(), sqlmock.AnyArg(), "worker-1", memory.WriteJobStatusRunning, sqlmock.AnyArg(), 20).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	jobs, err := repo.ClaimPending(ctx, "worker-1", now, leaseUntil, 10)
	if err != nil {
		t.Fatalf("claim pending: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != 10 || jobs[1].ID != 20 {
		t.Fatalf("claimed jobs out of order: %+v", jobs)
	}
	for _, job := range jobs {
		if job.LockedBy != "worker-1" || job.Status != memory.WriteJobStatusRunning {
			t.Fatalf("job not claimed by worker-1: %+v", job)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMemoryWriteJobRepositoryUpdateValidatesBeforeSave(t *testing.T) {
	db, mock := newMemorySchemaMockDB(t)
	repo := NewMemoryWriteJobRepository(db)
	ctx := context.Background()

	// Missing idempotency key: rejected by Validate before any SQL.
	invalid := &memory.MemoryWriteJob{
		BaseModel: domain.BaseModel{ID: 5, OwnerID: 1},
		Source:    "manual",
	}
	if err := repo.Update(ctx, invalid); err == nil {
		t.Fatal("invalid job update accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("invalid job update touched the database: %v", err)
	}

	// Valid job: Save issues one UPDATE and refreshes UpdatedAt.
	valid := &memory.MemoryWriteJob{
		BaseModel:      domain.BaseModel{ID: 5, OwnerID: 1},
		IdempotencyKey: "key",
		Source:         "manual",
		Status:         memory.WriteJobStatusCompleted,
	}
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `memory_write_jobs` SET")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repo.Update(ctx, valid); err != nil {
		t.Fatalf("update valid job: %v", err)
	}
	if valid.UpdatedAt.IsZero() {
		t.Fatal("Update did not refresh UpdatedAt")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
