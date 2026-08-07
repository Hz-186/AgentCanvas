package workspace

import (
	"context"
	"time"
)

const (
	KindShared   = "shared"
	KindWorktree = "worktree"

	StatusCreating  = "creating"
	StatusReady     = "ready"
	StatusFailed    = "failed"
	StatusPreserved = "preserved"
	StatusCleaned   = "cleaned"
)

type Workspace struct {
	ID                int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID           int64      `json:"owner_id" gorm:"column:owner_id"`
	ProjectID         int64      `json:"project_id" gorm:"column:project_id"`
	RunID             int64      `json:"run_id" gorm:"column:run_id"`
	ParentWorkspaceID *int64     `json:"parent_workspace_id,omitempty" gorm:"column:parent_workspace_id"`
	Kind              string     `json:"kind" gorm:"column:kind"`
	RepositoryRoot    string     `json:"repository_root" gorm:"column:repository_root"`
	WorkspacePath     string     `json:"workspace_path" gorm:"column:workspace_path"`
	BranchName        string     `json:"branch_name" gorm:"column:branch_name"`
	BaseRef           string     `json:"base_ref" gorm:"column:base_ref"`
	BaseSHA           string     `json:"base_sha" gorm:"column:base_sha"`
	HeadSHA           string     `json:"head_sha" gorm:"column:head_sha"`
	Status            string     `json:"status" gorm:"column:status"`
	Dirty             bool       `json:"dirty" gorm:"column:dirty"`
	Unpushed          bool       `json:"unpushed" gorm:"column:unpushed"`
	Locked            bool       `json:"locked" gorm:"column:locked"`
	LockReason        string     `json:"lock_reason" gorm:"column:lock_reason"`
	CleanupReason     string     `json:"cleanup_reason" gorm:"column:cleanup_reason"`
	ErrorMessage      string     `json:"error_message" gorm:"column:error_message"`
	CreatedAt         time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt         time.Time  `json:"updated_at" gorm:"column:updated_at"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty" gorm:"column:last_checked_at"`
	CleanedAt         *time.Time `json:"cleaned_at,omitempty" gorm:"column:cleaned_at"`
}

func (Workspace) TableName() string { return "agent_workspaces" }

type Repository interface {
	Create(ctx context.Context, item *Workspace) error
	FindByID(ctx context.Context, ownerID, id int64) (*Workspace, error)
	FindByRunID(ctx context.Context, ownerID, runID int64) (*Workspace, error)
	ListByProject(ctx context.Context, ownerID, projectID int64) ([]Workspace, error)
	// ListRecoverable returns every recoverable workspace when limit is zero.
	ListRecoverable(ctx context.Context, limit int) ([]Workspace, error)
	Update(ctx context.Context, item *Workspace) error
	ListStale(ctx context.Context, before time.Time, limit int) ([]Workspace, error)
}

type Context struct {
	ID               int64  `json:"workspace_id"`
	ProjectID        int64  `json:"project_id"`
	RunID            int64  `json:"run_id"`
	Kind             string `json:"kind"`
	RepositoryRoot   string `json:"repository_root"`
	WorkspacePath    string `json:"workspace_path"`
	BranchName       string `json:"branch_name"`
	BaseSHA          string `json:"base_sha,omitempty"`
	HeadSHA          string `json:"head_sha,omitempty"`
	Dirty            bool   `json:"dirty"`
	Unpushed         bool   `json:"unpushed"`
	FileWriteEnabled bool   `json:"file_write_enabled"`
	GitEnabled       bool   `json:"git_enabled"`
	ExecEnabled      bool   `json:"exec_enabled"`
}
