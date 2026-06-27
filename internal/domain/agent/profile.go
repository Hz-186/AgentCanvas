package agent

import "time"

type Profile struct {
	ID                 int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID            int64      `json:"owner_id" gorm:"column:owner_id"`
	AgentID            int64      `json:"agent_id" gorm:"column:agent_id"`
	Role               string     `json:"role" gorm:"column:role"`
	Goal               string     `json:"goal" gorm:"column:goal"`
	Backstory          string     `json:"backstory" gorm:"column:backstory"`
	SystemPrompt       string     `json:"system_prompt" gorm:"column:system_prompt"`
	DefaultProviderID  *int64     `json:"default_provider_id" gorm:"column:default_provider_id"`
	DefaultModel       string     `json:"default_model" gorm:"column:default_model"`
	MaxIterations      int        `json:"max_iterations" gorm:"column:max_iterations"`
	MaxExecutionTimeMS int        `json:"max_execution_time_ms" gorm:"column:max_execution_time_ms"`
	MemoryEnabled      bool       `json:"memory_enabled" gorm:"column:memory_enabled"`
	PlanningEnabled    bool       `json:"planning_enabled" gorm:"column:planning_enabled"`
	AllowDelegation    bool       `json:"allow_delegation" gorm:"column:allow_delegation"`
	AllowCodeExecution bool       `json:"allow_code_execution" gorm:"column:allow_code_execution"`
	CreatedAt          time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt          time.Time  `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt          *time.Time `json:"-" gorm:"column:deleted_at"`
}

func (Profile) TableName() string { return "agent_profiles" }
