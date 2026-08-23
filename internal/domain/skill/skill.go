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
	Disabled = false
	Enabled  = true
)

type Skill struct {
	domain.SoftDeleteModel
	Name                string          `json:"name" gorm:"column:name"`
	Description         string          `json:"description" gorm:"column:description"`
	SkillType           string          `json:"skill_type" gorm:"column:skill_type"`
	SourceType          string          `json:"source_type" gorm:"column:source_type"`
	EntryFile           string          `json:"entry_file" gorm:"column:entry_file"`
	ContentMarkdown     string          `json:"content_markdown,omitempty" gorm:"column:content_markdown"`
	BundlePath          string          `json:"bundle_path,omitempty" gorm:"column:bundle_path"`
	TagsJSON            json.RawMessage `json:"tags_json,omitempty" gorm:"column:tags_json"`
	Enabled             bool            `json:"enabled" gorm:"column:enabled"`
	Checksum            string          `json:"checksum" gorm:"column:checksum"`
	LastValidatedAt     *time.Time      `json:"last_validated_at,omitempty" gorm:"column:last_validated_at"`
	LastValidationError string          `json:"last_validation_error,omitempty" gorm:"column:last_validation_error"`
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
