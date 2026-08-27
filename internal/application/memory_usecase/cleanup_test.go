package memory_usecase

import (
	"context"
	"fmt"
	"testing"
)

type fakeCleanupProgress struct {
	done map[string]bool
}

func (f *fakeCleanupProgress) IsDone(_ context.Context, id string) (bool, error) {
	if f.done == nil {
		return false, nil
	}
	return f.done[id], nil
}

func (f *fakeCleanupProgress) MarkDone(_ context.Context, id string) error {
	if f.done == nil {
		f.done = make(map[string]bool)
	}
	f.done[id] = true
	return nil
}

type countingAction struct {
	run    int
	record []string
}

func (c *countingAction) Run(_ context.Context) error {
	c.run++
	c.record = append(c.record, fmt.Sprintf("executed:%d", c.run))
	return nil
}

func TestLegacyCleanupShouldRemoveRetiredSurfacesAfterValidation(t *testing.T) {
	ctx := context.Background()
	migrationChecks := 0
	backfillChecks := 0
	fileDeletes := &countingAction{}
	reflectionDrops := &countingAction{}
	reflectionSurfaceRetire := &countingAction{}
	writeLogDrops := &countingAction{}
	progress := &fakeCleanupProgress{}
	cleanup := &LegacyCleanup{
		ValidateMigration: func(_ context.Context) error {
			migrationChecks++
			return nil
		},
		ValidateESBackfill: func(_ context.Context) error {
			backfillChecks++
			return nil
		},
		Actions: []CleanupAction{
			{ID: "delete_legacy_files", Run: fileDeletes.Run},
			{ID: "drop_agent_reflections", Run: reflectionDrops.Run},
			{ID: "retire_reflection_api_index_worker", Run: reflectionSurfaceRetire.Run},
			{ID: "drop_memory_write_logs", Run: writeLogDrops.Run},
		},
		Progress: progress,
	}

	if err := cleanup.Run(ctx); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if migrationChecks != 1 || backfillChecks != 1 {
		t.Fatalf("validation counts = migration %d backfill %d, want 1 each", migrationChecks, backfillChecks)
	}
	if fileDeletes.run != 1 || reflectionDrops.run != 1 || reflectionSurfaceRetire.run != 1 || writeLogDrops.run != 1 {
		t.Fatalf("action run counts = files %d reflections %d api/index/worker %d write_logs %d, want 1 each",
			fileDeletes.run, reflectionDrops.run, reflectionSurfaceRetire.run, writeLogDrops.run)
	}

	// Each cleanup action runs exactly once: a second run must skip all of them.
	if err := cleanup.Run(ctx); err != nil {
		t.Fatalf("second cleanup run failed: %v", err)
	}
	if fileDeletes.run != 1 || reflectionDrops.run != 1 || reflectionSurfaceRetire.run != 1 || writeLogDrops.run != 1 {
		t.Fatalf("second run re-executed actions: files %d reflections %d api/index/worker %d write_logs %d",
			fileDeletes.run, reflectionDrops.run, reflectionSurfaceRetire.run, writeLogDrops.run)
	}
}

func TestLegacyCleanupShouldKeepSourcesOnFailure(t *testing.T) {
	ctx := context.Background()
	backfillFailing := true
	fileDeletes := &countingAction{}
	reflectionDrops := &countingAction{}
	reflectionSurfaceRetire := &countingAction{}
	writeLogDrops := &countingAction{}
	progress := &fakeCleanupProgress{}
	cleanup := &LegacyCleanup{
		ValidateMigration: func(_ context.Context) error { return nil },
		ValidateESBackfill: func(_ context.Context) error {
			if backfillFailing {
				return fmt.Errorf("context keyword backfill is incomplete")
			}
			return nil
		},
		Actions: []CleanupAction{
			{ID: "delete_legacy_files", Run: fileDeletes.Run},
			{ID: "drop_agent_reflections", Run: reflectionDrops.Run},
			{ID: "retire_reflection_api_index_worker", Run: reflectionSurfaceRetire.Run},
			{ID: "drop_memory_write_logs", Run: writeLogDrops.Run},
		},
		Progress: progress,
	}

	// ES backfill failure: zero destructive cleanup, zero DROP/delete calls.
	err := cleanup.Run(ctx)
	if err == nil {
		t.Fatal("expected ES backfill failure to stop cleanup")
	}
	if fileDeletes.run != 0 || reflectionDrops.run != 0 || reflectionSurfaceRetire.run != 0 || writeLogDrops.run != 0 {
		t.Fatalf("destructive actions ran on backfill failure: files %d reflections %d api/index/worker %d write_logs %d",
			fileDeletes.run, reflectionDrops.run, reflectionSurfaceRetire.run, writeLogDrops.run)
	}
	for _, id := range []string{"delete_legacy_files", "drop_agent_reflections", "retire_reflection_api_index_worker", "drop_memory_write_logs"} {
		if progress.done[id] {
			t.Fatalf("cleanup action %s was recorded despite the backfill failure", id)
		}
	}

	// The state stays rerunnable: once the backfill passes, the same cleanup
	// instance completes every action exactly once.
	backfillFailing = false
	if err := cleanup.Run(ctx); err != nil {
		t.Fatalf("rerun after backfill recovery failed: %v", err)
	}
	if fileDeletes.run != 1 || reflectionDrops.run != 1 || reflectionSurfaceRetire.run != 1 || writeLogDrops.run != 1 {
		t.Fatalf("post-recovery action run counts = files %d reflections %d api/index/worker %d write_logs %d, want 1 each",
			fileDeletes.run, reflectionDrops.run, reflectionSurfaceRetire.run, writeLogDrops.run)
	}
}
