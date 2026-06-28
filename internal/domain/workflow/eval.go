package workflow

import (
	"encoding/json"
	"time"
)

const (
	EvalDatasetStatusActive   = 1
	EvalDatasetStatusArchived = 2
)

const (
	EvalRunStatusRunning   = "running"
	EvalRunStatusCompleted = "completed"
	EvalRunStatusFailed    = "failed"
)

type EvalDataset struct {
	ID          int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID     int64      `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID  int64      `json:"workflow_id" gorm:"column:workflow_id"`
	Name        string     `json:"name" gorm:"column:name"`
	Description string     `json:"description" gorm:"column:description"`
	Status      int        `json:"status" gorm:"column:status"`
	CreatedAt   time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt   *time.Time `json:"-" gorm:"column:deleted_at"`
}

func (EvalDataset) TableName() string { return "workflow_eval_datasets" }

type EvalCase struct {
	ID                int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID           int64           `json:"owner_id" gorm:"column:owner_id"`
	DatasetID         int64           `json:"dataset_id" gorm:"column:dataset_id"`
	Name              string          `json:"name" gorm:"column:name"`
	InputJSON         json.RawMessage `json:"input_json" gorm:"column:input_json"`
	ExpectedJSON      json.RawMessage `json:"expected_json" gorm:"column:expected_json"`
	TagsJSON          json.RawMessage `json:"tags_json" gorm:"column:tags_json"`
	RequiredToolsJSON json.RawMessage `json:"required_tools_json" gorm:"column:required_tools_json"`
	CreatedAt         time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt         time.Time       `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt         *time.Time      `json:"-" gorm:"column:deleted_at"`
}

func (EvalCase) TableName() string { return "workflow_eval_cases" }

type EvalRun struct {
	ID            int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID       int64           `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID    int64           `json:"workflow_id" gorm:"column:workflow_id"`
	DatasetID     int64           `json:"dataset_id" gorm:"column:dataset_id"`
	FlowVersionID int64           `json:"flow_version_id" gorm:"column:flow_version_id"`
	Status        string          `json:"status" gorm:"column:status"`
	TotalCases    int             `json:"total_cases" gorm:"column:total_cases"`
	PassedCases   int             `json:"passed_cases" gorm:"column:passed_cases"`
	FailedCases   int             `json:"failed_cases" gorm:"column:failed_cases"`
	SuccessRate   float64         `json:"success_rate" gorm:"column:success_rate"`
	SummaryJSON   json.RawMessage `json:"summary_json" gorm:"column:summary_json"`
	ErrorMessage  string          `json:"error_message" gorm:"column:error_message"`
	StartedAt     time.Time       `json:"started_at" gorm:"column:started_at"`
	FinishedAt    *time.Time      `json:"finished_at" gorm:"column:finished_at"`
	CreatedAt     time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt     time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (EvalRun) TableName() string { return "workflow_eval_runs" }

type EvalResult struct {
	ID            int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID       int64           `json:"owner_id" gorm:"column:owner_id"`
	EvalRunID     int64           `json:"eval_run_id" gorm:"column:eval_run_id"`
	EvalCaseID    int64           `json:"eval_case_id" gorm:"column:eval_case_id"`
	WorkflowRunID *int64          `json:"workflow_run_id" gorm:"column:workflow_run_id"`
	Status        string          `json:"status" gorm:"column:status"`
	Score         float64         `json:"score" gorm:"column:score"`
	Reason        string          `json:"reason" gorm:"column:reason"`
	OutputJSON    json.RawMessage `json:"output_json" gorm:"column:output_json"`
	MetricsJSON   json.RawMessage `json:"metrics_json" gorm:"column:metrics_json"`
	ErrorMessage  string          `json:"error_message" gorm:"column:error_message"`
	LatencyMS     int             `json:"latency_ms" gorm:"column:latency_ms"`
	CreatedAt     time.Time       `json:"created_at" gorm:"column:created_at"`
}

func (EvalResult) TableName() string { return "workflow_eval_results" }
