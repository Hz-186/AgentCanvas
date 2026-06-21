package tool

import "context"

type DefinitionRepository interface {
	Create(ctx context.Context, item *Definition) error
	Update(ctx context.Context, item *Definition) error
	FindByID(ctx context.Context, ownerID, id int64) (*Definition, error)
	List(ctx context.Context, ownerID int64, limit, offset int) ([]Definition, error)
	SoftDelete(ctx context.Context, ownerID, id int64) error
}

type InvocationRepository interface {
	Create(ctx context.Context, item *Invocation) error
	ListByRun(ctx context.Context, ownerID, runID int64) ([]Invocation, error)
}
