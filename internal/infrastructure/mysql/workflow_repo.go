package mysql

import (
	"context"
	"time"

	"agentcanvas/internal/domain/workflow"

	"gorm.io/gorm"
)

type WorkflowRepository struct{ db *gorm.DB }

func NewWorkflowRepository(db *gorm.DB) *WorkflowRepository { return &WorkflowRepository{db: db} }

func (r *WorkflowRepository) Create(ctx context.Context, item *workflow.Workflow) error {
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	return newBaseRepository[workflow.Workflow](r.db).create(ctx, item)
}

func (r *WorkflowRepository) ListByOwner(ctx context.Context, ownerID int64) ([]workflow.Workflow, error) {
	return newBaseRepository[workflow.Workflow](r.db).listActiveByOwner(ctx, ownerID, "id DESC")
}

func (r *WorkflowRepository) FindByID(ctx context.Context, ownerID, id int64) (*workflow.Workflow, error) {
	return newBaseRepository[workflow.Workflow](r.db).findActiveByID(ctx, ownerID, id)
}

func (r *WorkflowRepository) Update(ctx context.Context, item *workflow.Workflow) error {
	item.UpdatedAt = time.Now().UTC()
	return newBaseRepository[workflow.Workflow](r.db).save(ctx, item)
}

func (r *WorkflowRepository) SoftDelete(ctx context.Context, ownerID, id int64) error {
	return newBaseRepository[workflow.Workflow](r.db).softDelete(ctx, &workflow.Workflow{}, ownerID, id)
}
