package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/domain/skill"
	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/pkg/tokencounter"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/toolruntime"

	"agentcanvas/internal/infrastructure/llm"
)

func (n runtimeCore) toolIDsFromPacks(ctx context.Context, ownerID int64, packIDs []int64) []int64 {
	ids := make([]int64, 0)
	for _, packID := range packIDs {
		if packID <= 0 {
			continue
		}
		toolIDs, err := n.ToolPacks.ListToolIDs(ctx, ownerID, packID)
		if err != nil {
			continue
		}
		ids = append(ids, toolIDs...)
	}
	return ids
}

func mergeInt64IDs(values ...[]int64) []int64 {
	seen := map[int64]bool{}
	merged := make([]int64, 0)
	for _, list := range values {
		for _, id := range list {
			if id <= 0 || seen[id] {
				continue
			}
			seen[id] = true
			merged = append(merged, id)
		}
	}
	return merged
}

func agentMode(mode string) string {
	normalized, err := conversation.NormalizeMode(mode)
	if err != nil {
		return conversation.ModeDefault
	}
	return normalized
}

func (n runtimeCore) workspaceCodingContext(ctx context.Context, rc *RunContext) *runtimeagent.ContextBlock {
	if rc == nil || rc.Workspace == nil {
		return nil
	}
	workspace := rc.Workspace
	statusText := "unavailable"
	if n.Git != nil && workspace.GitEnabled {
		if branch, head, dirty, unpushed, err := n.Git.RuntimeStatus(ctx, workspace.WorkspacePath); err == nil {
			if workspace.Kind == "worktree" && workspace.BaseSHA != "" && head != "" && head != workspace.BaseSHA {
				unpushed = true
			}
			statusText = fmt.Sprintf("branch=%s head=%s dirty=%t unpushed=%t", branch, head, dirty, unpushed)
		} else {
			statusText = "unavailable (status check failed)"
		}
	}
	content := fmt.Sprintf(`Active coding workspace (resolved by the server; never replace it with a model-provided path):
- absolute workspace path: %s
- repository root: %s
- workspace kind: %s
- branch: %s
- base SHA: %s
- Git status: %s

Workspace safety rules:
- Resolve every relative file path from the active workspace path.
- Do not read or expose secrets, credentials, .env files, or private keys.
- Do not modify .git or AgentCanvas internal state directories.
- Before editing, read the current file and confirm its contents.
- Do not commit, push, merge, reset, or delete branches unless the user explicitly requests it and the approval gate is satisfied.`, workspace.WorkspacePath, workspace.RepositoryRoot, workspace.Kind, workspace.BranchName, workspace.BaseSHA, statusText)
	return &runtimeagent.ContextBlock{Name: "coding_workspace", Role: "system", Content: content, Pinned: true}
}

func agentStepRecord(step runtimeagent.RunStep) AgentStepRecord {
	return AgentStepRecord{
		StepIndex:     step.Index,
		StepType:      step.Type,
		Role:          step.Role,
		Content:       step.Content,
		ToolCallID:    step.ToolCallID,
		ToolName:      step.ToolName,
		ArgumentsJSON: step.ArgumentsJSON,
		OutputJSON:    step.OutputJSON,
		Compressed:    step.Compressed,
		ErrorMessage:  step.Error,
		TokenCount:    step.TokenCount,
		LatencyMS:     step.LatencyMS,
		ProviderID:    step.ProviderID,
		Model:         step.Model,
	}
}

func (n runtimeCore) loadTools(ctx context.Context, ownerID int64, cfg agentRuntimeConfig, provider *LoadedProvider, workspace ...*toolruntime.WorkspaceContext) ([]toolruntime.RuntimeTool, error) {
	tools := make([]toolruntime.RuntimeTool, 0, len(cfg.ToolIDs)+4)
	tools = append(tools, toolruntime.HumanApprovalTool{})
	if cfg.RequestUserInputEnabled {
		tools = append(tools, toolruntime.RequestUserInputTool{})
	}
	if cfg.GoalToolsEnabled && n.Goals != nil {
		tools = append(tools, toolruntime.GetGoalTool{}, toolruntime.CreateGoalTool{}, toolruntime.UpdateGoalTool{})
	}
	if !n.DisableUpdatePlan {
		tools = append(tools, toolruntime.UpdatePlanTool{})
	}
	var workspaceContext *toolruntime.WorkspaceContext
	if len(workspace) > 0 {
		workspaceContext = workspace[0]
	}
	if workspaceContext != nil {
		workspaceTools := make([]toolruntime.RuntimeTool, 0, 14)
		readChars := n.FileReadMaxChars
		if readChars <= 0 {
			readChars = 100000
		}
		outputBytes := n.MaxOutputBytes
		if outputBytes <= 0 {
			outputBytes = 256 * 1024
		}
		if cfg.MaxToolOutputBytes > 0 && cfg.MaxToolOutputBytes < outputBytes {
			outputBytes = cfg.MaxToolOutputBytes
		}
		workspaceTools = append(workspaceTools,
			toolruntime.FileTool{Kind: "read_file", MaxReadChars: readChars, MaxOutputBytes: outputBytes},
			toolruntime.FileTool{Kind: "search_files", MaxReadChars: readChars, MaxOutputBytes: outputBytes},
			toolruntime.FileTool{Kind: "list_files", MaxReadChars: readChars, MaxOutputBytes: outputBytes},
		)
		if workspaceContext.FileWriteEnabled {
			workspaceTools = append(workspaceTools,
				toolruntime.FileTool{Kind: "write_file", MaxReadChars: readChars, MaxOutputBytes: outputBytes},
				toolruntime.FileTool{Kind: "patch_file", MaxReadChars: readChars, MaxOutputBytes: outputBytes},
				toolruntime.FileTool{Kind: "move_file", MaxReadChars: readChars, MaxOutputBytes: outputBytes},
				toolruntime.FileTool{Kind: "delete_file", MaxReadChars: readChars, MaxOutputBytes: outputBytes},
			)
		}
		if workspaceContext.ExecEnabled {
			workspaceTools = append(workspaceTools, toolruntime.WorkspaceExecTool{MaxOutputBytes: outputBytes, Timeout: n.WorkspaceTimeout})
		}
		if workspaceContext.GitEnabled && n.Git != nil {
			for _, name := range []string{"git_status", "git_diff", "git_log", "git_branch", "git_worktree", "git_commit"} {
				workspaceTools = append(workspaceTools, toolruntime.GitTool{Kind: name, Git: n.Git})
			}
		}
		for _, workspaceTool := range workspaceTools {
			if n.Audits != nil && toolruntime.MetadataOf(workspaceTool).SideEffect != toolruntime.SideEffectRead {
				workspaceTool = toolruntime.AuditedTool{Tool: workspaceTool, Audits: n.Audits}
			}
			tools = append(tools, workspaceTool)
		}
	}
	loadedSkills, err := n.loadSkillDefinitions(ctx, ownerID, cfg.SkillIDs)
	if err != nil {
		return nil, err
	}
	if len(loadedSkills) > 0 && n.Skills != nil {
		tools = append(tools, toolruntime.SkillLoadTool{
			Repository:      n.Skills,
			Audits:          n.Audits,
			AllowedSkillIDs: skillIDsFromItems(loadedSkills),
			SkillRoot:       n.SkillRoot,
			MaxContentBytes: cfg.MaxToolOutputBytes,
		})
		if strings.TrimSpace(cfg.SkillLoadingMode) == "search" || len(loadedSkills) > 10 {
			tools = append(tools, toolruntime.SkillSearchTool{
				Retriever:       n.SkillRetriever,
				Skills:          loadedSkills,
				AllowedSkillIDs: skillIDsFromItems(loadedSkills),
				Audits:          n.Audits,
				Limit:           3,
			})
		}
	}
	if len(cfg.KnowledgeBaseIDs) > 0 {
		if n.Retriever == nil {
			return nil, fmt.Errorf("agent runtime retriever is not configured")
		}
		tools = append(tools, toolruntime.KnowledgeSearchTool{
			Retriever:        n.Retriever,
			KnowledgeBaseIDs: cfg.KnowledgeBaseIDs,
			DefaultK:         cfg.KnowledgeTopK,
			Mode:             retrieval.Mode(cfg.KnowledgeMode),
		})
	}
	if cfg.AllowSubagents {
		if n.SubagentDispatcher == nil {
			return nil, fmt.Errorf("agent runtime subagent dispatcher is not configured")
		}
		tools = append(tools, toolruntime.SubagentTool{Dispatcher: n.SubagentDispatcher, Default: toolruntime.DefaultSubagentConfig{
			ProviderID: cfg.ProviderID, Model: cfg.Model, AllowedToolIDs: append([]int64(nil), cfg.ToolIDs...), AllowedSkillIDs: append([]int64(nil), cfg.SkillIDs...), AllowedKnowledgeIDs: append([]int64(nil), cfg.KnowledgeBaseIDs...), AllowedMCPServerIDs: append([]int64(nil), cfg.MCPServerIDs...), MaxIterations: cfg.MaxIterations, MaxToolCalls: cfg.MaxToolCalls, MaxExecutionTimeMS: cfg.MaxExecutionTimeMS, MaxParallelChildren: cfg.MaxParallelSubAgents, MaxDepth: cfg.MaxSubagentDepth, RequireApprovalForRisk: append([]string(nil), cfg.RequireApprovalForRisk...), MaxToolTimeoutMS: cfg.MaxToolTimeoutMS, MaxToolOutputBytes: cfg.MaxToolOutputBytes, AllowedHosts: append([]string(nil), cfg.AllowedHosts...), CodeExecutionEnabled: cfg.CodeExecutionEnabled,
		}})
	}
	if len(cfg.MCPServerIDs) > 0 {
		if n.MCPServers == nil {
			return nil, fmt.Errorf("agent runtime MCP server repository is not configured")
		}
		loaded, err := n.loadMCPTools(ctx, ownerID, cfg.MCPServerIDs)
		if err != nil {
			return nil, err
		}
		tools = append(tools, loaded...)
	}
	if cfg.CodeExecutionEnabled {
		if n.Sandbox == nil {
			return nil, fmt.Errorf("agent runtime sandbox runner is not configured")
		}
		tools = append(tools, toolruntime.PythonSandboxTool{Runner: n.Sandbox})
	}
	if cfg.MemoryEnabled {
		// read_memory is keyword-only: the unified context index selects and
		// SQL hydrates. The retired durable file reader is no longer an Agent
		// read path and skills are never searched here.
		if n.Memories == nil || n.ContextIndex == nil {
			return nil, fmt.Errorf("agent runtime unified context index is not configured: SQL memory repository and keyword index are required")
		}
		tools = append(tools, toolruntime.MemoryReadTool{
			Memories:     n.Memories,
			RecallLogs:   n.MemoryRecallLogs,
			ContextIndex: n.ContextIndex,
			TokenBudget:  cfg.MemoryPolicy.TokenBudget,
		})
		if n.SessionSearch != nil {
			tools = append(tools, toolruntime.SessionSearchTool{Index: n.SessionSearch})
		}
	}
	return appendLoadedTools(ctx, n, ownerID, cfg, tools)
}

func appendLoadedTools(ctx context.Context, n runtimeCore, ownerID int64, cfg agentRuntimeConfig, tools []toolruntime.RuntimeTool) ([]toolruntime.RuntimeTool, error) {
	if len(cfg.ToolIDs) > 0 {
		if n.Tools == nil {
			return nil, fmt.Errorf("agent runtime tool registry is not configured")
		}
		loaded, err := n.Tools.LoadForAgent(ctx, ownerID, cfg.ToolIDs)
		if err != nil {
			return nil, err
		}
		tools = append(tools, loaded...)
	}
	return tools, nil
}

func (n runtimeCore) loadSkillDefinitions(ctx context.Context, ownerID int64, ids []int64) ([]skill.Skill, error) {
	if n.Skills == nil || len(ids) == 0 {
		return nil, nil
	}
	items, err := n.Skills.ListByIDs(ctx, ownerID, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]skill.Skill, len(items))
	for _, item := range items {
		if !item.Enabled || item.DeletedAt != nil {
			continue
		}
		byID[item.ID] = item
	}
	ordered := make([]skill.Skill, 0, len(ids))
	for _, id := range ids {
		if item, ok := byID[id]; ok {
			ordered = append(ordered, item)
		}
	}
	return ordered, nil
}

func (n runtimeCore) buildSkillContextBlocks(ctx context.Context, ownerID int64, cfg agentRuntimeConfig, provider *LoadedProvider, task string) []runtimeagent.ContextBlock {
	items, err := n.loadSkillDefinitions(ctx, ownerID, cfg.SkillIDs)
	if err != nil || len(items) == 0 {
		return nil
	}
	candidates := make([]semanticCandidate, 0, len(items))
	for index, item := range items {
		candidates = append(candidates, semanticCandidate{Index: index, ID: fmt.Sprint(item.ID), Text: item.Name + "\n" + item.Description})
	}
	if scores := n.semanticCandidateScores(ctx, provider, task, candidates); len(scores) > 0 {
		sort.SliceStable(items, func(i, j int) bool { return scores[fmt.Sprint(items[i].ID)] > scores[fmt.Sprint(items[j].ID)] })
	}
	mode := strings.TrimSpace(cfg.SkillLoadingMode)
	if mode == "" {
		mode = "metadata_only"
	}
	lines := make([]string, 0, len(items)*4+1)
	lines = append(lines, "Available skills:")
	for index, item := range items {
		if index >= 20 {
			break
		}
		description := strings.TrimSpace(item.Description)
		maxDescription := 500
		if mode == "search" || len(items) > 10 {
			maxDescription = 160
		}
		if len(description) > maxDescription {
			description = truncateString(description, maxDescription)
		}
		lines = append(lines,
			fmt.Sprintf("- name: %s", item.Name),
			fmt.Sprintf("  id: %d", item.ID),
			fmt.Sprintf("  description: %s", description),
		)
		if mode == "search" || len(items) > 10 {
			lines = append(lines, "  guidance: use skill_search to shortlist candidates, then load_skill for full instructions.")
		} else {
			lines = append(lines, fmt.Sprintf("  load: use load_skill with skill_id=%d when the task matches this skill.", item.ID))
		}
	}
	return []runtimeagent.ContextBlock{{
		Name:    "skills_metadata",
		Role:    "system",
		Content: strings.Join(lines, "\n"),
		Pinned:  false,
	}}
}

type semanticCandidate struct {
	Index int
	ID    string
	Text  string
}

func (n runtimeCore) semanticCandidateScores(ctx context.Context, provider *LoadedProvider, query string, candidates []semanticCandidate) map[string]float64 {
	if n.Embedder == nil || provider == nil || strings.TrimSpace(provider.EmbeddingModel) == "" || strings.TrimSpace(query) == "" || len(candidates) == 0 {
		return nil
	}
	if len(candidates) > 100 {
		candidates = candidates[:100]
	}
	inputs := make([]string, 0, len(candidates)+1)
	inputs = append(inputs, query)
	for _, item := range candidates {
		inputs = append(inputs, item.Text)
	}
	response, err := n.Embedder.Embed(ctx, provider.EmbeddingConfig, llm.EmbeddingRequest{Model: provider.EmbeddingModel, Input: inputs})
	if err != nil || response == nil || len(response.Embeddings) != len(inputs) || len(response.Embeddings[0]) == 0 {
		return nil
	}
	scores := make(map[string]float64, len(candidates))
	for index, item := range candidates {
		scores[item.ID] = cosineSimilarity(response.Embeddings[0], response.Embeddings[index+1])
	}
	return scores
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	dot, left, right := 0.0, 0.0, 0.0
	for index := range a {
		x, y := float64(a[index]), float64(b[index])
		dot += x * y
		left += x * x
		right += y * y
	}
	if left == 0 || right == 0 {
		return 0
	}
	return dot / (math.Sqrt(left) * math.Sqrt(right))
}

func (n runtimeCore) semanticShortlistTools(ctx context.Context, provider *LoadedProvider, task string, tools []toolruntime.RuntimeTool) []toolruntime.RuntimeTool {
	const maxSemanticTools = 20
	if len(tools) <= maxSemanticTools {
		return tools
	}
	candidates := make([]semanticCandidate, 0, len(tools))
	for index, item := range tools {
		candidates = append(candidates, semanticCandidate{Index: index, ID: item.Name(), Text: item.Name() + "\n" + item.Description()})
	}
	scores := n.semanticCandidateScores(ctx, provider, task, candidates)
	if len(scores) == 0 {
		return tools
	}
	sorted := append([]toolruntime.RuntimeTool(nil), tools...)
	sort.SliceStable(sorted, func(i, j int) bool {
		leftCore, rightCore := coreContextTool(sorted[i].Name()), coreContextTool(sorted[j].Name())
		if leftCore != rightCore {
			return leftCore
		}
		return scores[sorted[i].Name()] > scores[sorted[j].Name()]
	})
	return sorted[:maxSemanticTools]
}

func coreContextTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "search_knowledge", "read_memory", "skill_search", "load_skill", "request_approval", "resume_run":
		return true
	default:
		return false
	}
}

const (
	// automaticSummaryBlockName is the stable context-block ID of the injected
	// automatic summary; budget and compaction logic rely on its stability.
	automaticSummaryBlockName = "memory_summary"
	// automaticSummaryAdvisory marks the injected block as advisory and points
	// the model at read_memory for verified details. It appears exactly once.
	automaticSummaryAdvisory = "DURABLE MEMORY SUMMARY (advisory; verify details with read_memory when needed):"
	// automaticSummaryFreshnessNote warns that a consolidated summary may lag
	// behind recent memories and must be verified through read_memory. It
	// appears exactly once when the artifact carries memory IDs.
	automaticSummaryFreshnessNote = "may be stale; call read_memory to verify current details"
)

// automaticSummaryTokenBudget is the injected summary block's token bound.
const automaticSummaryTokenBudget = 1200

// buildAutomaticMemoryBlock is the single automatic durable-memory read. It
// injects the bounded advisory summary from the SQL summary artifact for
// top-level, non-delegated runs only; detailed memories stay available through
// the keyword read_memory tool. A summary artifact read failure yields no
// block and never falls back to legacy files.
func (n runtimeCore) buildAutomaticMemoryBlock(ctx context.Context, rc *RunContext, cfg agentRuntimeConfig) *runtimeagent.ContextBlock {
	if n.MemoryArtifacts == nil || rc == nil || rc.ParentRunID != nil || rc.DelegationDepth != 0 || !cfg.MemoryEnabled {
		return nil
	}
	policy, err := cfg.MemoryPolicy.Normalize()
	if err != nil {
		policy = memory.DefaultPolicy()
	}
	budget := policy.TokenBudget
	if budget <= 0 {
		budget = automaticSummaryTokenBudget
	}
	artifact, err := n.MemoryArtifacts.Latest(ctx, rc.OwnerID, memory.ArtifactKindSummary)
	if err != nil || artifact == nil {
		return nil
	}
	content := strings.TrimSpace(artifact.Content)
	if content == "" {
		return nil
	}
	memoryIDs := summarySourceRefIDs(artifact.SourceRefsJSON)
	footer := ""
	if len(memoryIDs) > 0 {
		footer = fmt.Sprintf("Memory IDs: %s\nThis summary (version %d) %s", strings.Join(memoryIDs, ", "), artifact.Version, automaticSummaryFreshnessNote)
	}
	overheadTokens := tokencounter.Count("", "", automaticSummaryAdvisory).Tokens + tokencounter.Count("", "", footer).Tokens + 2
	contentBudget := budget - overheadTokens
	if contentBudget < 1 {
		contentBudget = 1
	}
	content = truncateToTokenBudget(content, contentBudget)
	var builder strings.Builder
	builder.WriteString(automaticSummaryAdvisory)
	builder.WriteString("\n")
	builder.WriteString(content)
	if footer != "" {
		builder.WriteString("\n")
		builder.WriteString(footer)
	}
	return &runtimeagent.ContextBlock{
		Name:    automaticSummaryBlockName,
		Role:    conversation.RoleSystem,
		Content: builder.String(),
		Pinned:  false,
	}
}

// summarySourceRefIDs extracts the stable memory IDs represented by a summary
// artifact's source references, sorted ascending without duplicates.
func summarySourceRefIDs(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var refs []struct {
		SourceID int64 `json:"source_id"`
	}
	if err := json.Unmarshal(raw, &refs); err != nil || len(refs) == 0 {
		return nil
	}
	seen := map[int64]bool{}
	ids := make([]int64, 0, len(refs))
	for _, ref := range refs {
		if ref.SourceID <= 0 || seen[ref.SourceID] {
			continue
		}
		seen[ref.SourceID] = true
		ids = append(ids, ref.SourceID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	formatted := make([]string, len(ids))
	for i, id := range ids {
		formatted[i] = strconv.FormatInt(id, 10)
	}
	return formatted
}

// truncateToTokenBudget returns the longest rune prefix of value whose
// conservative token count fits the budget.
func truncateToTokenBudget(value string, budget int) string {
	value = strings.TrimSpace(value)
	if budget <= 0 || tokencounter.Count("", "", value).Tokens <= budget {
		return value
	}
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if tokencounter.Count("", "", string(runes[:mid])).Tokens <= budget {
			low = mid
		} else {
			high = mid - 1
		}
	}
	if low == 0 {
		return ""
	}
	return string(runes[:low])
}

func projectIDFromRunContext(rc *RunContext) int64 {
	if rc == nil {
		return 0
	}
	if rc.ProjectID > 0 {
		return rc.ProjectID
	}
	if rc.Workspace == nil {
		return 0
	}
	return rc.Workspace.ProjectID
}

func skillIDsFromItems(items []skill.Skill) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func (n runtimeCore) loadMCPTools(ctx context.Context, ownerID int64, serverIDs []int64) ([]toolruntime.RuntimeTool, error) {
	loaded := make([]toolruntime.RuntimeTool, 0)
	for _, serverID := range serverIDs {
		if serverID <= 0 {
			continue
		}
		server, err := n.MCPServers.FindServerByID(ctx, ownerID, serverID)
		if err != nil {
			return nil, err
		}
		if !server.Enabled {
			continue
		}
		client := toolruntime.NewMCPClientFromServer(server)
		defs := cachedMCPToolDefs(ctx, n.MCPServers, ownerID, serverID)
		if len(defs) == 0 {
			var err error
			defs, err = client.Discover(ctx)
			if err != nil {
				return nil, err
			}
		}
		for _, def := range defs {
			loaded = append(loaded, toolruntime.NewMCPToolRuntime(def, client))
		}
	}
	return loaded, nil
}

func cachedMCPToolDefs(ctx context.Context, repo tool.MCPRepository, ownerID, serverID int64) []toolruntime.MCPToolDef {
	if repo == nil {
		return nil
	}
	cached, err := repo.ListToolCache(ctx, ownerID, serverID)
	if err != nil || len(cached) == 0 {
		return nil
	}
	defs := make([]toolruntime.MCPToolDef, 0, len(cached))
	for _, item := range cached {
		name := strings.TrimSpace(item.ToolName)
		if name == "" {
			continue
		}
		defs = append(defs, toolruntime.MCPToolDef{
			Name:        name,
			Description: item.Description,
			Parameters:  item.InputSchemaJSON,
		})
	}
	return defs
}
