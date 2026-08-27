package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain"
)

const (
	TypeProfile  = "profile"
	TypeEpisodic = "episodic"
	TypeTask     = "task"
	TypeArchival = "archival"
)

const (
	TierShortTerm = "short_term"
	TierLongTerm  = "long_term"
)

const (
	ScopeUser         = "user"
	ScopeAgent        = "agent"
	ScopeConversation = "conversation"
	ScopeProject      = "project"

	StatusActive     = "active"
	StatusSuperseded = "superseded"
	StatusRevoked    = "revoked"
)

const (
	WriteActionCreate   = "create"
	WriteActionUpdate   = "update"
	WriteActionNoop     = "noop"
	WriteActionConflict = "conflict"
)

type Memory struct {
	domain.SoftDeleteModel
	ConflictWithID       *int64          `json:"conflict_with_id,omitempty" gorm:"column:conflict_with_id"`
	HasConflict          bool            `json:"has_conflict" gorm:"column:has_conflict"`
	SourceConversationID *int64          `json:"source_conversation_id,omitempty" gorm:"column:source_conversation_id"`
	SourceProjectID      *int64          `json:"source_project_id,omitempty" gorm:"column:source_project_id"`
	ScopeType            string          `json:"scope_type" gorm:"column:scope_type;default:user"`
	ScopeID              int64           `json:"scope_id" gorm:"column:scope_id"`
	Status               string          `json:"status" gorm:"column:status;default:active"`
	SupersedesID         *int64          `json:"supersedes_id,omitempty" gorm:"column:supersedes_id"`
	MemoryType           string          `json:"memory_type" gorm:"column:memory_type"`
	RetentionTier        string          `json:"retention_tier" gorm:"column:retention_tier;default:long_term"`
	Title                string          `json:"title" gorm:"column:title"`
	Content              string          `json:"content" gorm:"column:content"`
	Importance           float64         `json:"importance" gorm:"column:importance"`
	UsageCount           int             `json:"usage_count" gorm:"column:usage_count;default:0"`
	PromotionCount       int             `json:"promotion_count" gorm:"column:promotion_count;default:0"`
	Source               string          `json:"source" gorm:"column:source"`
	DeduplicationKey     *string         `json:"deduplication_key,omitempty" gorm:"column:deduplication_key"`
	MetadataJSON         json.RawMessage `json:"metadata_json" gorm:"column:metadata_json"`
	LastUsedAt           *time.Time      `json:"last_used_at" gorm:"column:last_used_at"`
	LastDecayAt          *time.Time      `json:"last_decay_at" gorm:"column:last_decay_at"`
	ExpiresAt            *time.Time      `json:"expires_at" gorm:"column:expires_at"`
}

const (
	ArtifactKindHandbook = "handbook"
	ArtifactKindSummary  = "summary"
	ArtifactKindRawInput = "raw_input"
	ArtifactKindRollout  = "rollout_summary"
	ArtifactKindAdHoc    = "ad_hoc"
)

var ArtifactKinds = []string{ArtifactKindHandbook, ArtifactKindSummary, ArtifactKindRawInput, ArtifactKindRollout, ArtifactKindAdHoc}

type MemoryArtifact struct {
	domain.BaseModel
	Kind           string          `json:"kind" gorm:"column:kind"`
	Version        int             `json:"version" gorm:"column:version"`
	Content        string          `json:"content" gorm:"column:content"`
	Source         string          `json:"source" gorm:"column:source"`
	SourceRefsJSON json.RawMessage `json:"source_refs_json" gorm:"column:source_refs_json"`
	Checksum       string          `json:"checksum" gorm:"column:checksum"`
	ProtectedAt    *time.Time      `json:"protected_at,omitempty" gorm:"column:protected_at"`
	ConsolidatedAt *time.Time      `json:"consolidated_at,omitempty" gorm:"column:consolidated_at"`
}

func (MemoryArtifact) TableName() string { return "memory_artifacts" }
func (a MemoryArtifact) ValidKind() bool {
	for _, k := range ArtifactKinds {
		if a.Kind == k {
			return true
		}
	}
	return false
}

func (a MemoryArtifact) Validate() error {
	if a.OwnerID <= 0 {
		return fmt.Errorf("artifact owner is required")
	}
	if !a.ValidKind() {
		return fmt.Errorf("unsupported artifact kind %q", a.Kind)
	}
	if a.Version <= 0 {
		return fmt.Errorf("artifact version must be positive")
	}
	if strings.TrimSpace(a.Checksum) == "" {
		return fmt.Errorf("artifact checksum is required")
	}
	return nil
}

const (
	WriteJobStatusPending    = "pending"
	WriteJobStatusRunning    = "running"
	WriteJobStatusCompleted  = "completed"
	WriteJobStatusFailed     = "failed"
	WriteJobStatusDeadLetter = "dead_letter"
)

var WriteJobSources = []string{"extraction", "ad_hoc", "proposal", "consolidation", "reflection", "manual"}

// ValidSources is the canonical source vocabulary shared by memories and jobs.
var ValidSources = WriteJobSources

func ValidateSource(source string) error { return ValidateWriteJobSource(source) }

func ValidateWriteJobSource(source string) error {
	for _, allowed := range WriteJobSources {
		if source == allowed {
			return nil
		}
	}
	return fmt.Errorf("unsupported memory write source %q", source)
}

func CanonicalSource(source string) string {
	switch strings.TrimSpace(source) {
	case "extraction", "ad_hoc", "proposal", "consolidation", "reflection", "manual":
		return strings.TrimSpace(source)
	default:
		return "manual"
	}
}

type MemoryWriteJob struct {
	domain.BaseModel
	IdempotencyKey string          `json:"idempotency_key" gorm:"column:idempotency_key"`
	Source         string          `json:"source" gorm:"column:source"`
	PayloadJSON    json.RawMessage `json:"payload_json" gorm:"column:payload_json"`
	Status         string          `json:"status" gorm:"column:status"`
	AttemptCount   int             `json:"attempt_count" gorm:"column:attempt_count"`
	DueAt          *time.Time      `json:"due_at,omitempty" gorm:"column:due_at"`
	LockedBy       string          `json:"locked_by" gorm:"column:locked_by"`
	LockedAt       *time.Time      `json:"locked_at,omitempty" gorm:"column:locked_at"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty" gorm:"column:lease_expires_at"`
	ErrorMessage   string          `json:"error_message" gorm:"column:error_message"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty" gorm:"column:completed_at"`
}

func (MemoryWriteJob) TableName() string { return "memory_write_jobs" }
func (j MemoryWriteJob) Validate() error {
	if j.OwnerID <= 0 {
		return fmt.Errorf("job owner is required")
	}
	if err := ValidateWriteJobSource(j.Source); err != nil {
		return err
	}
	if strings.TrimSpace(j.IdempotencyKey) == "" {
		return fmt.Errorf("idempotency key is required")
	}
	return nil
}

// CanClaimAt is shared eligibility logic for leased workers. Repository claim
// queries must still lock and order eligible rows by due_at then id.
func (j MemoryWriteJob) CanClaimAt(now time.Time) bool {
	if j.Status == WriteJobStatusPending {
		return j.DueAt == nil || !j.DueAt.After(now)
	}
	return j.Status == WriteJobStatusRunning && j.LeaseExpiresAt != nil && !j.LeaseExpiresAt.After(now)
}

type MemoryArtifactRepository interface {
	Create(ctx context.Context, artifact *MemoryArtifact) error
	Latest(ctx context.Context, ownerID int64, kind string) (*MemoryArtifact, error)
}

type MemoryWriteJobRepository interface {
	Create(ctx context.Context, job *MemoryWriteJob) error
	FindByIdempotencyKey(ctx context.Context, ownerID int64, key string) (*MemoryWriteJob, error)
	ClaimPending(ctx context.Context, workerID string, now time.Time, leaseUntil time.Time, limit int) ([]MemoryWriteJob, error)
	Update(ctx context.Context, job *MemoryWriteJob) error
}

func (Memory) TableName() string { return "memories" }

// ApplyV2Defaults fills only lifecycle and explicit scope defaults. Source
// identifiers are provenance fields and must never determine access scope.
func (m *Memory) ApplyV2Defaults() {
	if m == nil {
		return
	}
	if m.Status == "" {
		m.Status = StatusActive
	}
	if m.ScopeType == "" {
		m.ScopeType = ScopeUser
	}
	if m.ScopeID <= 0 {
		switch m.ScopeType {
		case ScopeUser:
			m.ScopeID = m.OwnerID
		}
	}
}

func ResolveScope(memoryType string, ownerID, agentID, projectID, conversationID int64, requestedType string, requestedID int64) (string, int64, error) {
	memoryType = strings.TrimSpace(memoryType)
	switch memoryType {
	case TypeProfile, TypeEpisodic, TypeTask, TypeArchival:
	default:
		return "", 0, fmt.Errorf("unsupported memory type %q", memoryType)
	}
	scopeType, scopeID := strings.TrimSpace(requestedType), requestedID
	if scopeType == "" {
		switch memoryType {
		case TypeProfile:
			scopeType, scopeID = ScopeUser, ownerID
		case TypeTask, TypeArchival:
			if projectID > 0 {
				scopeType, scopeID = ScopeProject, projectID
			} else if conversationID > 0 {
				scopeType, scopeID = ScopeConversation, conversationID
			} else {
				return "", 0, fmt.Errorf("memory type %q requires a project or conversation scope", memoryType)
			}
		case TypeEpisodic:
			if conversationID <= 0 {
				return "", 0, fmt.Errorf("memory type %q requires a conversation scope", memoryType)
			}
			scopeType, scopeID = ScopeConversation, conversationID
		default:
			return "", 0, fmt.Errorf("unsupported memory type %q", memoryType)
		}
	}
	if scopeType != ScopeUser && scopeType != ScopeAgent && scopeType != ScopeProject && scopeType != ScopeConversation {
		return "", 0, fmt.Errorf("unsupported memory scope %q", scopeType)
	}
	if scopeID <= 0 {
		switch scopeType {
		case ScopeUser:
			scopeID = ownerID
		case ScopeAgent:
			scopeID = agentID
		case ScopeProject:
			scopeID = projectID
		case ScopeConversation:
			scopeID = conversationID
		}
	}
	if scopeID <= 0 {
		return "", 0, fmt.Errorf("memory scope %q requires a positive scope id", scopeType)
	}
	return scopeType, scopeID, nil
}

func (m Memory) IsRecallable(now time.Time) bool {
	status := m.Status
	if status == "" {
		status = StatusActive
	}
	return status == StatusActive && m.DeletedAt == nil && !m.HasConflict &&
		(m.ExpiresAt == nil || m.ExpiresAt.After(now))
}

type RecallLog struct {
	domain.ImmutableModel
	AgentID        int64           `json:"agent_id" gorm:"column:agent_id"`
	ConversationID int64           `json:"conversation_id" gorm:"column:conversation_id"`
	RunID          int64           `json:"run_id" gorm:"column:run_id"`
	Query          string          `json:"query" gorm:"column:query"`
	CandidateJSON  json.RawMessage `json:"candidate_json" gorm:"column:candidate_json"`
	InjectedJSON   json.RawMessage `json:"injected_json" gorm:"column:injected_json"`
	TokenCost      int             `json:"token_cost" gorm:"column:token_cost"`
	Feedback       string          `json:"feedback" gorm:"column:feedback"`
}

func (RecallLog) TableName() string { return "memory_recall_logs" }
