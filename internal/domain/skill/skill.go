package skill

import (
	"encoding/json"
	"time"

	"agentcanvas/internal/domain"
)

const (
	TypeInstruction = "instruction"
	TypeBundle      = "bundle"
)

const (
	SourceInline    = "inline"
	SourceLocalPath = "local_path"
)

const (
	StatusDisabled = domain.StatusDisabled
	StatusActive   = domain.StatusActive
)

type Skill struct {
	ID                  int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID             int64           `json:"owner_id" gorm:"column:owner_id"`
	Name                string          `json:"name" gorm:"column:name"`
	Description         string          `json:"description" gorm:"column:description"`
	SkillType           string          `json:"skill_type" gorm:"column:skill_type"`
	SourceType          string          `json:"source_type" gorm:"column:source_type"`
	EntryFile           string          `json:"entry_file" gorm:"column:entry_file"`
	ContentMD           string          `json:"content_md,omitempty" gorm:"column:content_md"`
	BundlePath          string          `json:"bundle_path,omitempty" gorm:"column:bundle_path"`
	TagsJSON            json.RawMessage `json:"tags_json,omitempty" gorm:"column:tags_json"`
	Status              int             `json:"status" gorm:"column:status"`
	Version             int             `json:"version" gorm:"column:version"`
	Checksum            string          `json:"checksum" gorm:"column:checksum"`
	LastValidatedAt     *time.Time      `json:"last_validated_at,omitempty" gorm:"column:last_validated_at"`
	LastValidationError string          `json:"last_validation_error,omitempty" gorm:"column:last_validation_error"`
	CreatedAt           time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt           time.Time       `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt           *time.Time      `json:"deleted_at,omitempty" gorm:"column:deleted_at"`
}

func (Skill) TableName() string { return "skills" }

func (s *Skill) Tags() []string {
	if len(s.TagsJSON) == 0 {
		return nil
	}
	var tags []string
	if err := json.Unmarshal(s.TagsJSON, &tags); err != nil {
		return nil
	}
	return tags
}
