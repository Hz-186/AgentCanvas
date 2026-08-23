package knowledge

import (
	"encoding/json"

	"agentcanvas/internal/domain"
)

type RetrievalLog struct {
	domain.ImmutableModel
	KnowledgeBaseIDs json.RawMessage `json:"knowledge_base_ids" gorm:"column:knowledge_base_ids"`
	QueryText        string          `json:"query_text" gorm:"column:query_text"`
	RetrievalBackend string          `json:"retrieval_backend" gorm:"column:retrieval_backend"`
	RetrievalMode    string          `json:"retrieval_mode" gorm:"column:retrieval_mode"`
	TopK             int             `json:"top_k" gorm:"column:top_k"`
	ResultCount      int             `json:"result_count" gorm:"column:result_count"`
	LatencyMS        int             `json:"latency_ms" gorm:"column:latency_ms"`
	ResultsJSON      json.RawMessage `json:"results_json" gorm:"column:results_json"`
}

func (RetrievalLog) TableName() string { return "retrieval_logs" }
