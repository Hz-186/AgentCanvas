package workflow

import "time"

type Team struct {
	ID                   int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID              int64     `json:"owner_id" gorm:"column:owner_id"`
	Name                 string    `json:"name" gorm:"column:name"`
	SupervisorWorkflowID int64     `json:"supervisor_workflow_id" gorm:"column:supervisor_workflow_id"`
	HandoffStrategy      string    `json:"handoff_strategy" gorm:"column:handoff_strategy"`
	MaxDepth             int       `json:"max_depth" gorm:"column:max_depth"`
	CreatedAt            time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt            time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (Team) TableName() string { return "workflow_teams" }

type TeamMember struct {
	ID         int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID    int64     `json:"owner_id" gorm:"column:owner_id"`
	TeamID     int64     `json:"team_id" gorm:"column:team_id"`
	WorkflowID int64     `json:"workflow_id" gorm:"column:workflow_id"`
	Role       string    `json:"role" gorm:"column:role"`
	CreatedAt  time.Time `json:"created_at" gorm:"column:created_at"`
}

func (TeamMember) TableName() string { return "workflow_team_members" }
