package memory_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/skill"
)

// TerminalReflectionWriteAdapter is the extraction producer that replaced the
// retired reflection analyzer/queue. Terminal runs with inline reflection
// evidence enqueue an ordinary memory write job with source reflection.
type TerminalReflectionWriteAdapter struct {
	pipeline *MemoryWritePipeline
}

func NewTerminalReflectionWriteAdapter(pipeline *MemoryWritePipeline) *TerminalReflectionWriteAdapter {
	return &TerminalReflectionWriteAdapter{pipeline: pipeline}
}

func (a *TerminalReflectionWriteAdapter) EnqueueTerminalReflection(ctx context.Context, req memory.TerminalReflectionRequest) error {
	if a == nil || a.pipeline == nil {
		return fmt.Errorf("terminal reflection writer is not configured")
	}
	if req.OwnerID <= 0 || req.AgentID <= 0 || req.RunID <= 0 {
		return fmt.Errorf("terminal reflection owner, agent and run are required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return fmt.Errorf("terminal reflection content is required")
	}
	key := fmt.Sprintf("reflection:run:%d", req.RunID)
	metadata, err := json.Marshal(map[string]any{
		"evidence": req.EvidenceJSON,
	})
	if err != nil {
		return fmt.Errorf("encode terminal reflection metadata: %w", err)
	}
	return a.pipeline.Enqueue(ctx, WriteJobRequest{
		OwnerID:        req.OwnerID,
		Source:         "reflection",
		IdempotencyKey: key,
		Payload: memory.WriteRequest{
			OwnerID:      req.OwnerID,
			AgentID:      req.AgentID,
			RunID:        req.RunID,
			Source:       "reflection",
			MemoryType:   memory.TypeTask,
			ScopeType:    memory.ScopeAgent,
			ScopeID:      req.AgentID,
			Title:        "Run reflection",
			Content:      strings.TrimSpace(req.Content),
			MetadataJSON: metadata,
		},
	})
}

var _ memory.TerminalReflectionWriter = (*TerminalReflectionWriteAdapter)(nil)

// ProposalWriteJobAdapter routes approved reflection proposals into the
// unified memory write pipeline with source proposal. It replaced the retired
// reflection repository write in self-improvement.
type ProposalWriteJobAdapter struct {
	pipeline *MemoryWritePipeline
}

func NewProposalWriteJobAdapter(pipeline *MemoryWritePipeline) *ProposalWriteJobAdapter {
	return &ProposalWriteJobAdapter{pipeline: pipeline}
}

func (a *ProposalWriteJobAdapter) EnqueueProposalWriteJob(ctx context.Context, req memory.ProposalWriteJobRequest) error {
	if a == nil || a.pipeline == nil {
		return fmt.Errorf("proposal memory writer is not configured")
	}
	if req.OwnerID <= 0 || req.AgentID <= 0 || req.ProposalID <= 0 {
		return fmt.Errorf("proposal owner, agent and proposal are required")
	}
	key := fmt.Sprintf("proposal:%d", req.ProposalID)
	metadata, err := json.Marshal(map[string]any{
		"proposal_id": req.ProposalID,
		"confidence":  req.Confidence,
		"checksum":    req.Checksum,
		"evidence":    json.RawMessage(req.Evidence),
	})
	if err != nil {
		return fmt.Errorf("encode proposal memory metadata: %w", err)
	}
	return a.pipeline.Enqueue(ctx, WriteJobRequest{
		OwnerID:        req.OwnerID,
		Source:         "proposal",
		IdempotencyKey: key,
		Payload: memory.WriteRequest{
			OwnerID:      req.OwnerID,
			AgentID:      req.AgentID,
			RunID:        req.RunID,
			Source:       "proposal",
			MemoryType:   memory.TypeTask,
			ScopeType:    memory.ScopeAgent,
			ScopeID:      req.AgentID,
			Title:        strings.TrimSpace(req.Title),
			Content:      strings.TrimSpace(req.Content),
			MetadataJSON: metadata,
		},
	})
}

var _ memory.ProposalMemoryWriter = (*ProposalWriteJobAdapter)(nil)

// SkillRepositorySink hands legacy skill files to the skill subsystem. Skills
// are not memories: the importer never creates a memory artifact for them.
type SkillRepositorySink struct {
	skills skill.Repository
}

func NewSkillRepositorySink(skills skill.Repository) *SkillRepositorySink {
	return &SkillRepositorySink{skills: skills}
}

func (s *SkillRepositorySink) ImportSkill(ctx context.Context, ownerID int64, relPath, content string) error {
	if s == nil || s.skills == nil {
		return fmt.Errorf("skill import sink is not configured")
	}
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if !strings.HasPrefix(relPath, "skills/") {
		return fmt.Errorf("skill import path must be under skills/: %s", relPath)
	}
	parts := strings.Split(relPath, "/")
	name := strings.TrimSpace(parts[1])
	if name == "" {
		name = "imported-skill"
	}
	sum := sha256.Sum256([]byte(content))
	item := &skill.Skill{
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: ownerID}},
		Name:            name,
		Description:     "Imported from the legacy durable-memory skills directory",
		SkillType:       skill.TypeInstruction,
		SourceType:      skill.SourceInline,
		EntryFile:       "SKILL.md",
		ContentMarkdown: content,
		TagsJSON:        json.RawMessage(`["legacy-import"]`),
		Enabled:         skill.Disabled,
		Checksum:        hex.EncodeToString(sum[:]),
	}
	return s.skills.Create(ctx, item)
}

var _ SkillImportSink = (*SkillRepositorySink)(nil)
