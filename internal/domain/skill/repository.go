package skill

import "context"

type Repository interface {
	Create(ctx context.Context, item *Skill) error
	Update(ctx context.Context, item *Skill) error
	FindByID(ctx context.Context, ownerID, id int64) (*Skill, error)
	List(ctx context.Context, ownerID int64, limit, offset int) ([]Skill, error)
	ListByIDs(ctx context.Context, ownerID int64, ids []int64) ([]Skill, error)
	SoftDelete(ctx context.Context, ownerID, id int64) error
}

// Retriever owns query-driven skill discovery. Skill queries route here,
// never through memory: skills are not memory artifacts and read_memory never
// searches them.
type Retriever interface {
	Search(ctx context.Context, ownerID int64, query string, limit int) ([]Skill, error)
}
