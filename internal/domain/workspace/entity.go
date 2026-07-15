package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusReleased = "released"
)

type Workspace struct {
	ID            int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID       int64      `json:"owner_id" gorm:"column:owner_id"`
	Name          string     `json:"name" gorm:"column:name"`
	RootPath      string     `json:"root_path" gorm:"column:root_path"`
	DefaultBranch string     `json:"default_branch" gorm:"column:default_branch"`
	Status        string     `json:"status" gorm:"column:status"`
	CreatedAt     time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt     time.Time  `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt     *time.Time `json:"-" gorm:"column:deleted_at"`
}

func (Workspace) TableName() string { return "workspaces" }

type Pack struct {
	ID                   int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID              int64           `json:"owner_id" gorm:"column:owner_id"`
	WorkspaceID          int64           `json:"workspace_id" gorm:"column:workspace_id"`
	Name                 string          `json:"name" gorm:"column:name"`
	AllowedPathsJSON     json.RawMessage `json:"-" gorm:"column:allowed_paths_json"`
	AllowedPaths         []string        `json:"allowed_paths" gorm:"-"`
	CommandAllowlistJSON json.RawMessage `json:"-" gorm:"column:command_allowlist_json"`
	CommandAllowlist     []string        `json:"command_allowlist" gorm:"-"`
	NetworkEnabled       bool            `json:"network_enabled" gorm:"column:network_enabled"`
	AllowedDomainsJSON   json.RawMessage `json:"-" gorm:"column:allowed_domains_json"`
	AllowedDomains       []string        `json:"allowed_domains" gorm:"-"`
	DockerImage          string          `json:"docker_image" gorm:"column:docker_image"`
	TimeoutSeconds       int             `json:"timeout_seconds" gorm:"column:timeout_seconds"`
	CPULimit             string          `json:"cpu_limit" gorm:"column:cpu_limit"`
	MemoryLimitMB        int             `json:"memory_limit_mb" gorm:"column:memory_limit_mb"`
	ProcessLimit         int             `json:"process_limit" gorm:"column:process_limit"`
	MaxOutputBytes       int             `json:"max_output_bytes" gorm:"column:max_output_bytes"`
	Checksum             string          `json:"checksum" gorm:"column:checksum"`
	Status               string          `json:"status" gorm:"column:status"`
	CreatedAt            time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt            time.Time       `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt            *time.Time      `json:"-" gorm:"column:deleted_at"`
}

func (Pack) TableName() string { return "workspace_packs" }

func (p *Pack) Normalize() {
	if len(p.AllowedPaths) == 0 {
		p.AllowedPaths = []string{"."}
	}
	if p.TimeoutSeconds <= 0 {
		p.TimeoutSeconds = 120
	}
	if p.CPULimit == "" {
		p.CPULimit = "2"
	}
	if p.MemoryLimitMB <= 0 {
		p.MemoryLimitMB = 2048
	}
	if p.ProcessLimit <= 0 {
		p.ProcessLimit = 128
	}
	if p.MaxOutputBytes <= 0 {
		p.MaxOutputBytes = 1 << 20
	}
	if p.Status == "" {
		p.Status = StatusActive
	}
	if p.DockerImage == "" {
		p.DockerImage = "agentcanvas/workspace:latest"
	}
}

func (p *Pack) Validate() error {
	p.Normalize()
	if p.WorkspaceID <= 0 || strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("workspace_id and name are required")
	}
	if p.NetworkEnabled {
		return fmt.Errorf("network-enabled workspace packs require an egress proxy and are not enabled by this runtime")
	}
	if p.TimeoutSeconds > 1800 || p.MemoryLimitMB > 16384 || p.ProcessLimit > 1024 || p.MaxOutputBytes > 16<<20 {
		return fmt.Errorf("workspace pack resource limit exceeds server maximum")
	}
	for _, value := range p.AllowedPaths {
		if strings.HasPrefix(strings.TrimSpace(value), "/") || strings.Contains(value, "..") {
			return fmt.Errorf("allowed_paths must be clean relative paths")
		}
	}
	for _, value := range p.CommandAllowlist {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, " /\\") {
			return fmt.Errorf("command allowlist entries must be executable basenames")
		}
	}
	return nil
}

func (p *Pack) Encode() error {
	if err := p.Validate(); err != nil {
		return err
	}
	p.AllowedPathsJSON, _ = json.Marshal(p.AllowedPaths)
	p.CommandAllowlistJSON, _ = json.Marshal(p.CommandAllowlist)
	p.AllowedDomainsJSON, _ = json.Marshal(p.AllowedDomains)
	snapshot, err := json.Marshal(map[string]any{"workspace_id": p.WorkspaceID, "allowed_paths": p.AllowedPaths, "commands": p.CommandAllowlist, "network": p.NetworkEnabled, "domains": p.AllowedDomains, "image": p.DockerImage, "timeout": p.TimeoutSeconds, "cpu": p.CPULimit, "memory": p.MemoryLimitMB, "processes": p.ProcessLimit, "output": p.MaxOutputBytes})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(snapshot)
	p.Checksum = hex.EncodeToString(sum[:])
	return nil
}

func (p *Pack) Decode() {
	_ = json.Unmarshal(p.AllowedPathsJSON, &p.AllowedPaths)
	_ = json.Unmarshal(p.CommandAllowlistJSON, &p.CommandAllowlist)
	_ = json.Unmarshal(p.AllowedDomainsJSON, &p.AllowedDomains)
	p.Normalize()
}

type RunLease struct {
	ID             int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID        int64     `json:"owner_id" gorm:"column:owner_id"`
	WorkspaceID    int64     `json:"workspace_id" gorm:"column:workspace_id"`
	RunID          int64     `json:"run_id" gorm:"column:run_id"`
	WorktreePath   string    `json:"worktree_path" gorm:"column:worktree_path"`
	LeaseToken     string    `json:"-" gorm:"column:lease_token"`
	LeaseExpiresAt time.Time `json:"lease_expires_at" gorm:"column:lease_expires_at"`
	Status         string    `json:"status" gorm:"column:status"`
	CreatedAt      time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt      time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (RunLease) TableName() string { return "workspace_run_leases" }
