package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/audit"
	"agentcanvas/internal/domain/skill"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type SkillLoadTool struct {
	Repository      skill.Repository
	Audits          audit.Repository
	AllowedSkillIDs []int64
	SkillRoot       string
	MaxContentBytes int
}

type SkillSearchTool struct {
	// Retriever is the skill subsystem's query reader. When configured, skill
	// queries route there (exactly once per query); the in-memory Skills list
	// remains the fallback for old wiring without a retriever.
	Retriever skill.Retriever
	// Skills is the in-memory fallback search surface (attached skills only).
	Skills []skill.Skill
	// AllowedSkillIDs restricts retriever results to the attached skills.
	// A nil slice disables the restriction (legacy wiring).
	AllowedSkillIDs []int64
	Audits          audit.Repository
	Limit           int
}

func (SkillLoadTool) Name() string { return "load_skill" }

func (SkillLoadTool) Description() string {
	return "Load the full SKILL.md instructions for one of the skills explicitly attached to this agent. Use this only when the task matches the skill metadata and the complete instructions are needed."
}

func (SkillLoadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"skill_id":{"type":"integer"}},"required":["skill_id"],"additionalProperties":false}`)
}

func (t SkillLoadTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead, MaxOutputBytes: t.MaxContentBytes}
}

func (t SkillLoadTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Repository == nil {
		return nil, fmt.Errorf("skill repository is not configured")
	}
	var args struct {
		SkillID int64 `json:"skill_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	if args.SkillID <= 0 || !containsAllowedSkillID(t.AllowedSkillIDs, args.SkillID) {
		return &ToolResult{ContentText: "skill_id is not allowed", IsError: true}, fmt.Errorf("%w: skill_id is not allowed", agenterrors.ErrInvalidInput)
	}
	item, err := t.Repository.FindByID(ctx, rc.OwnerID, args.SkillID)
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	if !item.Enabled || item.DeletedAt != nil {
		return &ToolResult{ContentText: "skill is not active", IsError: true}, fmt.Errorf("%w: skill is not active", agenterrors.ErrInvalidInput)
	}
	content, err := loadSkillContentFromItem(t.SkillRoot, item)
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	if t.MaxContentBytes > 0 && len(content) > t.MaxContentBytes {
		content = content[:t.MaxContentBytes]
	}
	t.audit(rc.OwnerID, "skill.load", strconv.FormatInt(item.ID, 10), map[string]any{"skill_id": item.ID, "name": item.Name})
	return ResultFromValue(map[string]any{
		"id":               item.ID,
		"name":             item.Name,
		"description":      item.Description,
		"content_markdown": content,
		"checksum":         item.Checksum,
	})
}

func (SkillSearchTool) Name() string { return "skill_search" }

func (SkillSearchTool) Description() string {
	return "Search the agent's attached skills by goal, name, description, and tags. Use this before load_skill when many skills are attached."
}

func (SkillSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"goal":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":5}},"required":["goal"],"additionalProperties":false}`)
}

func (SkillSearchTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead}
}

func (t SkillSearchTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	_ = ctx
	_ = rc
	var args struct {
		Goal  string `json:"goal"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	goal := strings.ToLower(strings.TrimSpace(args.Goal))
	if goal == "" {
		return &ToolResult{ContentText: "goal is required", IsError: true}, fmt.Errorf("%w: goal is required", agenterrors.ErrInvalidInput)
	}
	limit := args.Limit
	if limit <= 0 {
		limit = t.Limit
	}
	if limit <= 0 {
		limit = 3
	}
	if limit > 5 {
		limit = 5
	}
	matches, err := t.searchMatches(ctx, rc.OwnerID, goal, limit)
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	t.audit(rc.OwnerID, "skill.search", strconv.Itoa(len(matches)), map[string]any{"goal": args.Goal, "match_count": len(matches)})
	output := make([]map[string]any, 0, len(matches))
	for _, item := range matches {
		output = append(output, map[string]any{
			"skill_id":    item.ID,
			"name":        item.Name,
			"description": item.Description,
		})
	}
	return ResultFromValue(map[string]any{"matches": output})
}

// searchMatches routes the skill query to the skill subsystem retriever when
// one is configured; memory is never consulted for skills. The in-memory
// attached-skill scoring remains the fallback for legacy wiring.
func (t SkillSearchTool) searchMatches(ctx context.Context, ownerID int64, goal string, limit int) ([]skill.Skill, error) {
	if t.Retriever != nil {
		items, err := t.Retriever.Search(ctx, ownerID, goal, limit)
		if err != nil {
			return nil, err
		}
		return filterAllowedSkills(items, t.AllowedSkillIDs), nil
	}
	tokens := strings.Fields(goal)
	type skillMatch struct {
		Skill skill.Skill
		Score float64
	}
	matches := make([]skillMatch, 0, len(t.Skills))
	for _, item := range t.Skills {
		score := scoreSkill(item, tokens)
		if score <= 0 {
			continue
		}
		matches = append(matches, skillMatch{Skill: item, Score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Skill.ID < matches[j].Skill.ID
		}
		return matches[i].Score > matches[j].Score
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	skills := make([]skill.Skill, 0, len(matches))
	for _, item := range matches {
		skills = append(skills, item.Skill)
	}
	return skills, nil
}

// filterAllowedSkills restricts retriever results to the attached skill IDs.
// A nil allowed list disables the restriction (legacy wiring).
func filterAllowedSkills(items []skill.Skill, allowed []int64) []skill.Skill {
	if allowed == nil {
		return items
	}
	allowedSet := make(map[int64]bool, len(allowed))
	for _, id := range allowed {
		allowedSet[id] = true
	}
	filtered := make([]skill.Skill, 0, len(items))
	for _, item := range items {
		if allowedSet[item.ID] {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (t SkillLoadTool) audit(ownerID int64, action, resourceID string, detail map[string]any) {
	if t.Audits == nil || ownerID <= 0 {
		return
	}
	_ = t.Audits.Create(context.Background(), audit.NewLog(ownerID, ownerID, action, "skill", resourceID, detail, "", ""))
}

func (t SkillSearchTool) audit(ownerID int64, action, resourceID string, detail map[string]any) {
	if t.Audits == nil || ownerID <= 0 {
		return
	}
	_ = t.Audits.Create(context.Background(), audit.NewLog(ownerID, ownerID, action, "skill", resourceID, detail, "", ""))
}

func loadSkillContentFromItem(workspaceRoot string, item *skill.Skill) (string, error) {
	if item == nil {
		return "", agenterrors.ErrInvalidInput
	}
	if item.SourceType == skill.SourceInline {
		content := strings.TrimSpace(item.ContentMarkdown)
		if content == "" {
			return "", fmt.Errorf("%w: skill content is empty", agenterrors.ErrInvalidInput)
		}
		return content, nil
	}
	if item.SourceType != skill.SourceLocalPath {
		return "", fmt.Errorf("%w: unsupported source_type", agenterrors.ErrInvalidInput)
	}
	root, err := cleanAbsolutePath(workspaceRoot)
	if err != nil {
		return "", err
	}
	bundlePath, err := cleanAbsolutePath(item.BundlePath)
	if err != nil {
		return "", err
	}
	if bundlePath != root && !strings.HasPrefix(bundlePath, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: bundle_path must stay within the skill root", agenterrors.ErrInvalidInput)
	}
	entryFile := strings.TrimSpace(item.EntryFile)
	if entryFile == "" {
		entryFile = "SKILL.md"
	}
	fullPath, err := cleanAbsolutePath(filepath.Join(bundlePath, filepath.Clean(entryFile)))
	if err != nil {
		return "", err
	}
	if fullPath != bundlePath && !strings.HasPrefix(fullPath, bundlePath+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: entry_file escapes bundle_path", agenterrors.ErrInvalidInput)
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("%w: read skill entry file failed", agenterrors.ErrInvalidInput)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", fmt.Errorf("%w: skill content is empty", agenterrors.ErrInvalidInput)
	}
	return content, nil
}

func cleanAbsolutePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%w: path is required", agenterrors.ErrInvalidInput)
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("%w: invalid path", agenterrors.ErrInvalidInput)
	}
	return absPath, nil
}

func containsAllowedSkillID(ids []int64, target int64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func scoreSkill(item skill.Skill, tokens []string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	haystacks := []string{
		strings.ToLower(item.Name),
		strings.ToLower(item.Description),
		strings.ToLower(strings.Join(item.Tags(), " ")),
	}
	var score float64
	for _, token := range tokens {
		if token == "" {
			continue
		}
		if strings.Contains(haystacks[0], token) {
			score += 1
		}
		if strings.Contains(haystacks[1], token) {
			score += 0.7
		}
		if strings.Contains(haystacks[2], token) {
			score += 0.4
		}
	}
	return score
}
