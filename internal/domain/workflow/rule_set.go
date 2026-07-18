package workflow

import (
	"encoding/json"
	"time"
)

const (
	RuleSetStatusDraft      = "draft"
	RuleSetStatusPublished  = "published"
	RuleSetStatusSuperseded = "superseded"
)

type RuleSet struct {
	ID                    int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID               int64           `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID            int64           `json:"workflow_id" gorm:"column:workflow_id"`
	VersionNo             int             `json:"version_no" gorm:"column:version_no"`
	Status                string          `json:"status" gorm:"column:status"`
	Revision              int64           `json:"revision" gorm:"column:revision"`
	SourceHash            string          `json:"source_hash" gorm:"column:source_hash"`
	CompiledHash          string          `json:"compiled_hash" gorm:"column:compiled_hash"`
	CompiledSnapshotJSON  json.RawMessage `json:"compiled_snapshot_json,omitempty" gorm:"column:compiled_snapshot_json"`
	TokenEstimatorVersion string          `json:"token_estimator_version" gorm:"column:token_estimator_version"`
	RollbackOfRuleSetID   *int64          `json:"rollback_of_rule_set_id,omitempty" gorm:"column:rollback_of_rule_set_id"`
	PublishedBy           *int64          `json:"published_by,omitempty" gorm:"column:published_by"`
	PublishedAt           *time.Time      `json:"published_at,omitempty" gorm:"column:published_at"`
	CreatedAt             time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt             time.Time       `json:"updated_at" gorm:"column:updated_at"`
	Nodes                 []RuleNode      `json:"rules,omitempty" gorm:"-"`
}

func (RuleSet) TableName() string { return "workflow_rule_sets" }

type RuleNode struct {
	ID                int64           `json:"-" gorm:"primaryKey;column:id"`
	RuleSetID         int64           `json:"-" gorm:"column:rule_set_id"`
	RuleID            string          `json:"id" gorm:"column:rule_id"`
	Name              string          `json:"name" gorm:"column:name"`
	Content           string          `json:"content" gorm:"column:content"`
	Strength          string          `json:"strength" gorm:"column:strength"`
	ActivationJSON    json.RawMessage `json:"activation,omitempty" gorm:"column:activation_json"`
	TriggersJSON      json.RawMessage `json:"triggers,omitempty" gorm:"column:triggers_json"`
	Priority          int             `json:"priority" gorm:"column:priority"`
	SafetyCritical    bool            `json:"safety_critical" gorm:"column:safety_critical"`
	PolicyBindingJSON json.RawMessage `json:"policy_binding,omitempty" gorm:"column:policy_binding_json"`
	TokenCost         int             `json:"token_cost,omitempty" gorm:"column:token_cost"`
	ContentHash       string          `json:"content_hash,omitempty" gorm:"column:content_hash"`
	CreatedAt         time.Time       `json:"-" gorm:"column:created_at"`
	UpdatedAt         time.Time       `json:"-" gorm:"column:updated_at"`
}

func (RuleNode) TableName() string { return "workflow_rule_nodes" }
