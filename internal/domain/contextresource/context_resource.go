package contextresource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const (
	TypeReflection          = "reflection"
	TypeLongTermMemory      = "long_term_memory"
	TypeOptionalRule        = "optional_rule"
	TypeSkill               = "skill"
	TypeTool                = "tool"
	TypeConversationMessage = "conversation_message"

	OperationUpsert = "upsert"
	OperationDelete = "delete"

	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusDeadLetter = "dead_letter"
)

var AllResourceTypes = []string{
	TypeReflection,
	TypeLongTermMemory,
	TypeOptionalRule,
	TypeSkill,
	TypeTool,
	TypeConversationMessage,
}

var ErrLeaseLost = errors.New("context resource outbox lease lost")

// EmbeddingProfile identifies a compatible vector space. Vectors from distinct
// profile hashes must never share a collection.
type EmbeddingProfile struct {
	ProviderID int64
	Model      string
	Dimensions int
	Hash       string
}

type embeddingProfileContextKey struct{}

func WithEmbeddingProfile(ctx context.Context, profile EmbeddingProfile) context.Context {
	return context.WithValue(ctx, embeddingProfileContextKey{}, profile.Normalized())
}

func EmbeddingProfileFromContext(ctx context.Context) EmbeddingProfile {
	if ctx == nil {
		return EmbeddingProfile{}
	}
	profile, _ := ctx.Value(embeddingProfileContextKey{}).(EmbeddingProfile)
	return profile.Normalized()
}

func (p EmbeddingProfile) Normalized() EmbeddingProfile {
	p.Model = strings.TrimSpace(p.Model)
	if p.ProviderID > 0 || p.Model != "" || p.Dimensions > 0 {
		p.Hash = HashContent(strings.Join([]string{integer(p.ProviderID), p.Model, integer(int64(p.Dimensions))}, "\x1f"))
	} else {
		p.Hash = strings.TrimSpace(p.Hash)
	}
	return p
}

type OutboxItem struct {
	ID                   int64      `gorm:"primaryKey;column:id"`
	OwnerID              int64      `gorm:"column:owner_id"`
	WorkflowID           int64      `gorm:"column:workflow_id"`
	ResourceType         string     `gorm:"column:resource_type"`
	ResourceID           string     `gorm:"column:resource_id"`
	Operation            string     `gorm:"column:operation"`
	ContentHash          string     `gorm:"column:content_hash"`
	EmbeddingProviderID  int64      `gorm:"column:embedding_provider_id"`
	EmbeddingModel       string     `gorm:"column:embedding_model"`
	EmbeddingDimensions  int        `gorm:"column:embedding_dimensions"`
	EmbeddingProfileHash string     `gorm:"column:embedding_profile_hash"`
	Status               string     `gorm:"column:status"`
	AttemptCount         int        `gorm:"column:attempt_count"`
	MaxAttempts          int        `gorm:"column:max_attempts"`
	AvailableAt          time.Time  `gorm:"column:available_at"`
	LockedBy             string     `gorm:"column:locked_by"`
	LockedAt             *time.Time `gorm:"column:locked_at"`
	LeaseExpiresAt       *time.Time `gorm:"column:lease_expires_at"`
	LastError            string     `gorm:"column:last_error"`
	CompletedAt          *time.Time `gorm:"column:completed_at"`
	CreatedAt            time.Time  `gorm:"column:created_at"`
	UpdatedAt            time.Time  `gorm:"column:updated_at"`
}

func (OutboxItem) TableName() string { return "context_resource_index_outbox" }

type Document struct {
	OwnerID        int64
	WorkflowID     int64
	ResourceType   string
	ResourceID     string
	Content        string
	ContentHash    string
	ConversationID int64
	Metadata       map[string]any
}

type SearchRequest struct {
	OwnerID        int64
	WorkflowID     int64
	ConversationID int64
	ResourceTypes  []string
	Query          string
	Mode           string
	TopK           int
	Profile        EmbeddingProfile
}

type SearchResult struct {
	ResourceType string
	ResourceID   string
	Score        float64
	Metadata     map[string]any
}

type Repository interface {
	Claim(ctx context.Context, workerID string, limit int, lease time.Duration) ([]OutboxItem, error)
	Renew(ctx context.Context, id int64, workerID string, lease time.Duration) error
	Complete(ctx context.Context, id int64, workerID string, profile EmbeddingProfile) error
	Retry(ctx context.Context, id int64, workerID string, cause error, next time.Time) error
	LoadDocument(ctx context.Context, item OutboxItem) (*Document, error)
}

type Index interface {
	Upsert(ctx context.Context, document Document, profile EmbeddingProfile) (EmbeddingProfile, error)
	Delete(ctx context.Context, item OutboxItem) error
	Search(ctx context.Context, request SearchRequest) ([]SearchResult, error)
}

func HashContent(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}

func DocumentID(ownerID int64, resourceType, resourceID string) string {
	sum := sha256.Sum256([]byte(integer(ownerID) + "\x1f" + strings.TrimSpace(resourceType) + "\x1f" + strings.TrimSpace(resourceID)))
	return hex.EncodeToString(sum[:])
}

func integer(value int64) string {
	if value == 0 {
		return "0"
	}
	const digits = "0123456789"
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = digits[value%10]
		value /= 10
	}
	return string(buf[i:])
}
