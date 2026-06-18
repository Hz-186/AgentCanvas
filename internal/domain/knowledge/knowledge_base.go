package knowledge

import "time"

const (
	RetrievalBackendElasticsearch = "elasticsearch"
	RetrievalModeKeyword          = "keyword"
	ChunkMethodFixedToken         = "fixed_token"
)

const (
	KnowledgeBaseStatusDisabled = iota
	KnowledgeBaseStatusActive
)

type KnowledgeBase struct {
	ID               int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID          int64      `json:"owner_id" gorm:"column:owner_id"`
	Name             string     `json:"name" gorm:"column:name"`
	Description      string     `json:"description" gorm:"column:description"`
	RetrievalBackend string     `json:"retrieval_backend" gorm:"column:retrieval_backend"`
	RetrievalMode    string     `json:"retrieval_mode" gorm:"column:retrieval_mode"`
	ChunkMethod      string     `json:"chunk_method" gorm:"column:chunk_method"`
	ChunkSize        int        `json:"chunk_size" gorm:"column:chunk_size"`
	ChunkOverlap     int        `json:"chunk_overlap" gorm:"column:chunk_overlap"`
	Status           int        `json:"status" gorm:"column:status"`
	DocumentCount    int        `json:"document_count" gorm:"column:document_count"`
	ChunkCount       int        `json:"chunk_count" gorm:"column:chunk_count"`
	CreatedAt        time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt        time.Time  `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt        *time.Time `json:"-" gorm:"column:deleted_at"`
}

func (KnowledgeBase) TableName() string { return "knowledge_bases" }
