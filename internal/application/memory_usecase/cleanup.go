package memory_usecase

import (
	"context"
	"fmt"
)

// CleanupAction is a single destructive retirement step. It must be safe to
// run exactly once per deployment: the cleanup stage records completion in
// CleanupProgress so a rerun skips finished actions.
type CleanupAction struct {
	ID  string
	Run func(ctx context.Context) error
}

// CleanupProgress records which cleanup actions have completed. A persisted
// implementation survives process restarts; an in-memory one is enough for
// tests and one-shot cleanup.
type CleanupProgress interface {
	IsDone(ctx context.Context, id string) (bool, error)
	MarkDone(ctx context.Context, id string) error
}

// LegacyCleanup runs the destructive retirement stage. Both validations must
// pass before any action runs: if migration or ES backfill validation fails,
// zero destructive actions execute and the legacy sources stay intact so the
// whole flow remains rerunnable. Each action runs exactly once.
type LegacyCleanup struct {
	ValidateMigration  func(ctx context.Context) error
	ValidateESBackfill func(ctx context.Context) error
	Actions            []CleanupAction
	Progress           CleanupProgress
}

func (c *LegacyCleanup) Run(ctx context.Context) error {
	if c == nil || c.ValidateMigration == nil || c.ValidateESBackfill == nil || c.Progress == nil {
		return fmt.Errorf("legacy cleanup is not configured")
	}
	if err := c.ValidateMigration(ctx); err != nil {
		return err
	}
	if err := c.ValidateESBackfill(ctx); err != nil {
		return err
	}
	for _, action := range c.Actions {
		done, err := c.Progress.IsDone(ctx, action.ID)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		if err := action.Run(ctx); err != nil {
			return err
		}
		if err := c.Progress.MarkDone(ctx, action.ID); err != nil {
			return err
		}
	}
	return nil
}
