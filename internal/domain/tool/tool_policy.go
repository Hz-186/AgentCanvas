package tool

import (
	"encoding/json"
	"time"
	"gorm.io/gorm"
)

type ToolPolicy struct {
	ID                     int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID                int64           `json:"owner_id" gorm:"column:owner_id"`
	Name                   string          `json:"name" gorm:"column:name"`
	RequireApprovalForRisk json.RawMessage `json:"require_approval_for_risk,omitempty" gorm:"column:require_approval_for_risk"`
	MaxTimeoutMS           int             `json:"max_timeout_ms" gorm:"column:max_timeout_ms"`
	MaxOutputBytes         int             `json:"max_output_bytes" gorm:"column:max_output_bytes"`
	AllowedHosts           json.RawMessage `json:"allowed_hosts,omitempty" gorm:"column:allowed_hosts"`
	CredentialScope        string          `json:"credential_scope,omitempty" gorm:"column:credential_scope"`
	CreatedAt              time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt              time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (ToolPolicy) TableName() string { return "tool_policies" }

func (p *ToolPolicy) RequireApprovalForRiskSlice() []string {
	return decodeStringSlice(p.RequireApprovalForRisk)
}

func (p *ToolPolicy) AllowedHostsSlice() []string {
	return decodeStringSlice(p.AllowedHosts)
}

func (p *ToolPolicy) BeforeCreate(tx *gorm.DB) error {
	return p.normalize()
}

func (p *ToolPolicy) BeforeUpdate(tx *gorm.DB) error {
	return p.normalize()
}

func (p *ToolPolicy) normalize() error {
	p.RequireApprovalForRisk = normalizeField(p.RequireApprovalForRisk)
	p.AllowedHosts = normalizeField(p.AllowedHosts)
	return nil
}

func decodeStringSlice(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var slice []string
	if err := json.Unmarshal(raw, &slice); err != nil {
		return nil
	}
	return slice
}

func normalizeField(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("[]")
	}
	return raw
}
