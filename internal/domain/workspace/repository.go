package workspace

import (
	"context"
	"time"
)

type Repository interface {
	CreateWorkspace(context.Context, *Workspace) error
	ListWorkspaces(context.Context, int64) ([]Workspace, error)
	FindWorkspace(context.Context, int64, int64) (*Workspace, error)
	UpdateWorkspace(context.Context, *Workspace) error
	DeleteWorkspace(context.Context, int64, int64) error
	CreatePack(context.Context, *Pack) error
	ListPacks(context.Context, int64, int64) ([]Pack, error)
	FindPack(context.Context, int64, int64) (*Pack, error)
	UpdatePack(context.Context, *Pack) error
	DeletePack(context.Context, int64, int64) error
	AcquireRunLease(context.Context, *RunLease) (*RunLease, error)
	HeartbeatRunLease(context.Context, int64, string, time.Time) (bool, error)
	ReleaseRunLease(context.Context, int64, string) (bool, error)
	ListExpiredRunLeases(context.Context, time.Time, int) ([]RunLease, error)
}
