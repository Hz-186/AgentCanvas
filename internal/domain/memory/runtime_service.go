package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/contextresource"
)

type RuntimeService struct {
	Memories     Repository
	ContextIndex contextresource.Index
	AgentID      int64
	Profile      contextresource.EmbeddingProfile
	RecallLogs   RecallLogRepository
}

type ReadRequest struct {
	OwnerID        int64
	ConversationID *int64
	ProjectID      int64
	AgentID        int64
	RunID          int64
	MemoryTypes    []string
	Query          string
	Limit          int
	TokenBudget    int
	// SemanticOnly is retained for legacy callers. Keyword memory reads never
	// fall back to a "latest N" list regardless of this flag.
	SemanticOnly bool
	// AllowLegacyListFallback is reserved for maintenance/import callers. It
	// is intentionally false for all Agent-facing reads.
	AllowLegacyListFallback bool
}

type ReadResult struct {
	Memories      []Memory       `json:"memories"`
	MemoryContext string         `json:"memory_context"`
	Count         int            `json:"count"`
	Query         string         `json:"query,omitempty"`
	RecallDetails []RecallDetail `json:"recall_details,omitempty"`
}

type RecallDetail struct {
	MemoryID  int64   `json:"memory_id"`
	Source    string  `json:"source"`
	ScopeType string  `json:"scope_type"`
	ScopeID   int64   `json:"scope_id"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
	TokenCost int     `json:"token_cost"`
}

type WriteRequest struct {
	OwnerID              int64
	AgentID              int64
	ConversationID       int64
	ProjectID            int64
	SourceConversationID *int64
	SourceProjectID      *int64
	RunID                int64
	MemoryID             int64
	MemoryType           string
	Title                string
	Content              string
	Importance           float64
	Reason               string
	Source               string
	DeduplicationKey     *string
	MetadataJSON         json.RawMessage
	ScopeType            string
	ScopeID              int64
	Status               string
	SupersedesID         *int64
	// ConflictResolution is injected only after a user decision. Supported
	// values are keep_existing:<id>, replace:<id>, and keep_both.
	ConflictResolution string
}

func (s RuntimeService) Read(ctx context.Context, req ReadRequest) (ReadResult, error) {
	if s.Memories == nil {
		return ReadResult{}, fmt.Errorf("memory repository is not configured")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	query := strings.TrimSpace(req.Query)
	if query == "" && !req.AllowLegacyListFallback {
		return ReadResult{}, fmt.Errorf("semantic memory query is required")
	}
	if query != "" {
		return s.readKeywordMemory(ctx, req, query, limit)
	}
	var items []Memory
	var err error
	if scoped, ok := s.Memories.(ScopedReader); ok && req.ProjectID > 0 {
		projectID := req.ProjectID
		items, err = scoped.ListForReadScoped(ctx, req.OwnerID, req.AgentID, req.MemoryTypes, req.ConversationID, &projectID, limit)
	} else {
		items, err = s.Memories.ListForRead(ctx, req.OwnerID, req.MemoryTypes, req.ConversationID, limit)
	}
	if err != nil {
		return ReadResult{}, err
	}
	items = filterReadableMemories(items, req)
	return s.readResult(ctx, req, trimMemoriesToTokenBudget(items, req.TokenBudget), query, nil, "legacy_list")
}

// readKeywordMemory is the sole Agent-facing memory detail read path. It
// searches the unified context ES keyword index, ranks hits by ES _score
// descending with an ascending memory ID tie-break, and hydrates the survivors
// from SQL. The vector leg is never invoked and an index failure is returned
// to the caller instead of degrading into a full-table scan.
func (s RuntimeService) readKeywordMemory(ctx context.Context, req ReadRequest, query string, limit int) (ReadResult, error) {
	if s.ContextIndex == nil {
		return ReadResult{}, fmt.Errorf("unified keyword memory index is not configured")
	}
	conversationID := int64(0)
	if req.ConversationID != nil {
		conversationID = *req.ConversationID
	}
	agentID := req.AgentID
	if agentID <= 0 {
		agentID = s.AgentID
	}
	hits, err := s.ContextIndex.Search(ctx, contextresource.SearchRequest{
		OwnerID:        req.OwnerID,
		AgentID:        agentID,
		ProjectID:      req.ProjectID,
		ConversationID: conversationID,
		ResourceTypes:  []string{contextresource.TypeLongTermMemory},
		Query:          query,
		Mode:           "keyword",
		TopK:           limit * 2,
		Profile:        s.Profile,
	})
	if err != nil {
		return ReadResult{}, fmt.Errorf("keyword memory retrieval failed: %w", err)
	}
	ranked := rankKeywordMemoryHits(hits)
	scores := make(map[int64]float64, len(ranked))
	for _, hit := range ranked {
		scores[hit.id] = hit.score
	}
	items, err := s.hydrateKeywordMemories(ctx, req, ranked, limit)
	if err != nil {
		return ReadResult{}, fmt.Errorf("hydrate keyword memory hits: %w", err)
	}
	return s.readResult(ctx, req, items, query, scores, "unified_context_index")
}

const maxMemoryContentChars = 6000

type keywordMemoryHit struct {
	id    int64
	score float64
}

// rankKeywordMemoryHits converts context index results into memory IDs ranked
// by ES _score descending with a deterministic ascending memory ID tie-break.
func rankKeywordMemoryHits(hits []contextresource.SearchResult) []keywordMemoryHit {
	ranked := make([]keywordMemoryHit, 0, len(hits))
	for _, hit := range hits {
		id, err := strconv.ParseInt(strings.TrimSpace(hit.ResourceID), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		ranked = append(ranked, keywordMemoryHit{id: id, score: hit.Score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].id < ranked[j].id
	})
	return ranked
}

// hydrateKeywordMemories re-fetches ranked ES hits from SQL and rechecks owner,
// scope, lifecycle and deduplication. The ES score order is preserved no
// matter which order the SQL rows arrive in; foreign-owner hits and stale rows
// are omitted and per-entry content is truncated without padding missing rows.
func (s RuntimeService) hydrateKeywordMemories(ctx context.Context, req ReadRequest, ranked []keywordMemoryHit, limit int) ([]Memory, error) {
	ids := make([]int64, 0, len(ranked))
	for _, hit := range ranked {
		ids = append(ids, hit.id)
	}
	rows, err := s.Memories.FindByIDs(ctx, req.OwnerID, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Memory, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	now := time.Now()
	items := make([]Memory, 0, limit)
	seenIDs := map[int64]bool{}
	seenContent := map[string]bool{}
	seenSources := map[string]bool{}
	for _, hit := range ranked {
		if seenIDs[hit.id] {
			continue
		}
		item, ok := byID[hit.id]
		if !ok || item.OwnerID != req.OwnerID {
			continue
		}
		if !item.IsRecallable(now) || !matchesLevel(item.RetentionTier) || !matchesType(item.MemoryType, req.MemoryTypes) || !matchesScope(item, req) {
			continue
		}
		contentKey := normalizeMemoryText(item.Content)
		sourceKey := ""
		if item.DeduplicationKey != nil {
			sourceKey = strings.TrimSpace(*item.DeduplicationKey)
		}
		if seenContent[contentKey] || (sourceKey != "" && seenSources[sourceKey]) {
			continue
		}
		seenIDs[hit.id], seenContent[contentKey] = true, true
		if sourceKey != "" {
			seenSources[sourceKey] = true
		}
		item.Content = truncateMemoryContent(item.Content)
		items = append(items, item)
		if len(items) == limit {
			break
		}
	}
	return items, nil
}

// truncateMemoryContent enforces the 6000-character per-entry bound shared by
// the keyword memory read contract.
func truncateMemoryContent(content string) string {
	runes := []rune(content)
	if len(runes) <= maxMemoryContentChars {
		return content
	}
	return string(runes[:maxMemoryContentChars])
}

func normalizeMemoryText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func filterReadableMemories(items []Memory, req ReadRequest) []Memory {
	filtered := make([]Memory, 0, len(items))
	for _, item := range items {
		if item.IsRecallable(time.Now()) && matchesLevel(item.RetentionTier) && matchesType(item.MemoryType, req.MemoryTypes) && matchesScope(item, req) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// matchesLevel keeps deprecated Redis working memory out of semantic recall.
// Cross-run conversation continuity is provided by durable compaction snapshots.
func matchesLevel(level string) bool {
	return level == "" || level == TierShortTerm || level == TierLongTerm
}

func (s RuntimeService) readResult(ctx context.Context, request ReadRequest, items []Memory, query string, scores map[int64]float64, reason string) (ReadResult, error) {
	ids := make([]int64, 0, len(items))
	lines := make([]string, 0, len(items))
	details := make([]RecallDetail, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		lines = append(lines, item.Content)
		details = append(details, RecallDetail{MemoryID: item.ID, Source: item.Source, ScopeType: item.ScopeType, ScopeID: item.ScopeID,
			Score: scores[item.ID], Reason: reason, TokenCost: max(1, len([]rune(item.Content))/4)})
	}
	if err := s.Memories.MarkUsed(ctx, request.OwnerID, ids); err != nil {
		return ReadResult{}, fmt.Errorf("mark recalled memories used: %w", err)
	}
	result := ReadResult{Memories: items, MemoryContext: strings.Join(lines, "\n"), Count: len(items), Query: query, RecallDetails: details}
	if s.RecallLogs != nil {
		candidateJSON, err := json.Marshal(scores)
		if err != nil {
			return ReadResult{}, err
		}
		injectedJSON, err := json.Marshal(details)
		if err != nil {
			return ReadResult{}, err
		}
		conversationID := int64(0)
		if request.ConversationID != nil {
			conversationID = *request.ConversationID
		}
		tokens := 0
		for i := range details {
			tokens += details[i].TokenCost
		}
		if err := s.RecallLogs.Create(ctx, &RecallLog{ImmutableModel: domain.ImmutableModel{OwnerID: request.OwnerID}, AgentID: request.AgentID,
			ConversationID: conversationID, RunID: request.RunID, Query: query, CandidateJSON: candidateJSON,
			InjectedJSON: injectedJSON, TokenCost: tokens}); err != nil {
			return ReadResult{}, fmt.Errorf("record memory recall: %w", err)
		}
	}
	return result, nil
}

func trimMemoriesToTokenBudget(items []Memory, budget int) []Memory {
	if budget <= 0 {
		return items
	}
	result := make([]Memory, 0, len(items))
	used := 0
	for i := range items {
		cost := max(1, len([]rune(items[i].Title+" "+items[i].Content))/4)
		if used+cost > budget {
			break
		}
		used += cost
		result = append(result, items[i])
	}
	return result
}

func matchesType(value string, types []string) bool {
	if len(types) == 0 {
		return true
	}
	for _, memoryType := range types {
		if value == strings.TrimSpace(memoryType) {
			return true
		}
	}
	return false
}

func matchesScope(item Memory, request ReadRequest) bool {
	if item.ScopeType == "" {
		return false
	}
	switch item.ScopeType {
	case ScopeUser:
		return item.ScopeID == 0 || item.ScopeID == request.OwnerID
	case ScopeConversation:
		return request.ConversationID != nil && item.ScopeID == *request.ConversationID
	case ScopeAgent:
		return request.AgentID > 0 && item.ScopeID == request.AgentID
	case ScopeProject:
		return request.ProjectID > 0 && item.ScopeID == request.ProjectID
	default:
		return false
	}
}
