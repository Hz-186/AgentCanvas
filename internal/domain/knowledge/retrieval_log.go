package knowledge

import "time"

type RetrievalLog struct {
	ID               int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID          int64     `json:"owner_id" gorm:"column:owner_id"`
	KBIDsJSON        string    `json:"kb_ids" gorm:"column:kb_ids"`
	QueryText        string    `json:"query_text" gorm:"column:query_text"`
	RetrievalBackend string    `json:"retrieval_backend" gorm:"column:retrieval_backend"`
	RetrievalMode    string    `json:"retrieval_mode" gorm:"column:retrieval_mode"`
	TopK             int       `json:"top_k" gorm:"column:top_k"`
	ResultCount      int       `json:"result_count" gorm:"column:result_count"`
	LatencyMS        int       `json:"latency_ms" gorm:"column:latency_ms"`
	ResultsJSON      string    `json:"results_json" gorm:"column:results_json"`
	CreatedAt        time.Time `json:"created_at" gorm:"column:created_at"`
}

func (RetrievalLog) TableName() string { return "retrieval_logs" }
