package project

import (
	"context"
	"time"
)

type Project struct {
	ID          int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID     int64           `json:"owner_id" gorm:"column:owner_id"`
	Slug        string          `json:"slug" gorm:"column:slug"`
	Name        string          `json:"name" gorm:"column:name"`
	Description string          `json:"description" gorm:"column:description"`
	Icon        string          `json:"icon" gorm:"column:icon"`
	Color       string          `json:"color" gorm:"column:color"`
	PrimaryPath string          `json:"primary_path" gorm:"column:primary_path"`
	Archived    bool            `json:"archived" gorm:"column:archived"`
	CreatedAt   time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time       `json:"updated_at" gorm:"column:updated_at"`
	Folders     []ProjectFolder `json:"folders,omitempty" gorm:"-"`
}

func (Project) TableName() string { return "projects" }

type ProjectFolder struct {
	ID        int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID   int64     `json:"owner_id" gorm:"column:owner_id"`
	ProjectID int64     `json:"project_id" gorm:"column:project_id"`
	Path      string    `json:"path" gorm:"column:path"`
	Label     string    `json:"label" gorm:"column:label"`
	IsPrimary bool      `json:"is_primary" gorm:"column:is_primary"`
	AddedAt   time.Time `json:"added_at" gorm:"column:added_at"`
}

func (ProjectFolder) TableName() string { return "project_folders" }

type Repository interface {
	Create(ctx context.Context, item *Project) error
	CreateWithPrimaryFolder(ctx context.Context, item *Project, folder *ProjectFolder) error
	ListByOwner(ctx context.Context, ownerID int64, includeArchived bool) ([]Project, error)
	FindByID(ctx context.Context, ownerID, id int64) (*Project, error)
	Update(ctx context.Context, item *Project) error
	Archive(ctx context.Context, ownerID, id int64) error
	ListFolders(ctx context.Context, ownerID, projectID int64) ([]ProjectFolder, error)
	AddFolder(ctx context.Context, item *ProjectFolder) error
	AddPrimaryFolder(ctx context.Context, item *ProjectFolder) error
	DeleteFolder(ctx context.Context, ownerID, projectID, folderID int64) error
	SetPrimaryFolder(ctx context.Context, ownerID, projectID, folderID int64) error
}
