 internal/domain/memory/repository.go              | 25 ---------
 internal/runtime/agentruntime/agent_runtime.go    |  5 --
 internal/runtime/agentruntime/dependencies.go     |  3 +-
 internal/runtime/agentruntime/runtime.go          |  1 -
 internal/runtime/toolruntime/memory_tools.go      | 62 -----------------------
 internal/runtime/toolruntime/memory_tools_test.go | 55 --------------------
 6 files changed, 1 insertion(+), 150 deletions(-)

diff --git a/internal/domain/memory/repository.go b/internal/domain/memory/repository.go
index eb9f2d2..1d1e8f6 100644
--- a/internal/domain/memory/repository.go
+++ b/internal/domain/memory/repository.go
@@ -1,34 +1,9 @@
 package memory
 
 import "context"
 
-type CandidateRequest struct {
-	OwnerID              int64
-	AgentID              int64
-	ConversationID       int64
-	ProjectID            int64
-	SourceConversationID int64
-	SourceProjectID      int64
-	RunID                int64
-	SourceID             string
-	MemoryID             int64
-	MemoryType           string
-	Title                string
-	Content              string
-	Action               string
-	Importance           float64
-	Evidence             []string
-	Source               string
-	ScopeType            string
-	ScopeID              int64
-}
-
-type CandidateWriter interface {
-	Suggest(ctx context.Context, request CandidateRequest) (int64, error)
-}
-
 type Commander interface {
 	Execute(ctx context.Context, request WriteRequest) (WriteResult, error)
 	Revoke(ctx context.Context, ownerID, memoryID int64, reason string) error
 	Supersede(ctx context.Context, ownerID, memoryID, replacementID int64, reason string) error
 }
diff --git a/internal/runtime/agentruntime/agent_runtime.go b/internal/runtime/agentruntime/agent_runtime.go
index d41e69f..2578a83 100644
--- a/internal/runtime/agentruntime/agent_runtime.go
+++ b/internal/runtime/agentruntime/agent_runtime.go
@@ -5,11 +5,10 @@ import (
 	"encoding/json"
 	"fmt"
 	"strings"
 
 	"agentcanvas/internal/domain/conversation"
-	"agentcanvas/internal/domain/memory"
 	runtimeagent "agentcanvas/internal/runtime/agent"
 	"agentcanvas/internal/runtime/harness/rules"
 	"agentcanvas/internal/runtime/toolruntime"
 )
 
@@ -92,14 +91,10 @@ func (r *AgentRuntime) ConfigureSessionSearch(index conversation.MessageSearchIn
 
 func (r *AgentRuntime) ConfigureMemoryReader(reader MemoryBatchReader) {
 	r.core.MemoryReader = reader
 }
 
-func (r *AgentRuntime) ConfigureMemoryCandidates(candidates memory.CandidateWriter) {
-	r.core.MemoryCandidates = candidates
-}
-
 func New(deps Deps) (*AgentRuntime, error) {
 	if deps.ToolCalling == nil {
 		return nil, fmt.Errorf("tool calling client is required")
 	}
 	if deps.Sandbox == nil {
diff --git a/internal/runtime/agentruntime/dependencies.go b/internal/runtime/agentruntime/dependencies.go
index 1fb2d41..46312e7 100644
--- a/internal/runtime/agentruntime/dependencies.go
+++ b/internal/runtime/agentruntime/dependencies.go
@@ -62,11 +62,10 @@ type Repositories struct {
 	SessionSearch    conversation.MessageSearchIndex
 	Memories         memory.Repository
 	MemoryReader     MemoryBatchReader
 	MemoryWriteLogs  memory.WriteLogRepository
 	MemoryRecallLogs memory.RecallLogRepository
-	MemoryCandidates memory.CandidateWriter
 	MemoryRetriever  memory.SemanticRetriever
 	MemoryFiles      memory.DurableReader
 	AdHocNotes       memory.AdHocWriter
 	ToolPacks        tool.PackRepository
 	Skills           skill.Repository
@@ -121,11 +120,11 @@ func buildRuntimeCore(deps Deps) runtimeCore {
 	workspaceRoot, _ := os.Getwd()
 	return runtimeCore{
 		coreRepositories: coreRepositories{
 			Providers: deps.Providers, ToolPacks: deps.ToolPacks, Skills: deps.Skills, MCPServers: deps.MCPServers,
 			Retriever: deps.Retriever, MemoryRetriever: deps.MemoryRetriever, Memories: deps.Memories, MemoryReader: deps.MemoryReader,
-			MemoryLogs: deps.MemoryWriteLogs, MemoryRecallLogs: deps.MemoryRecallLogs, MemoryCandidates: deps.MemoryCandidates,
+			MemoryLogs: deps.MemoryWriteLogs, MemoryRecallLogs: deps.MemoryRecallLogs,
 			MemoryFiles: deps.MemoryFiles, AdHocNotes: deps.AdHocNotes,
 			MessageHistory: deps.MessageHistory, MessageWriter: deps.MessageWriter, Compactions: deps.Compactions, SessionSearch: deps.SessionSearch,
 			ContextIndex: deps.ContextIndex,
 		},
 		coreClients: coreClients{LLM: deps.ToolCalling, Embedder: deps.Embedder, Archival: deps.Archival},
diff --git a/internal/runtime/agentruntime/runtime.go b/internal/runtime/agentruntime/runtime.go
index 35ac50f..94289b1 100644
--- a/internal/runtime/agentruntime/runtime.go
+++ b/internal/runtime/agentruntime/runtime.go
@@ -35,11 +35,10 @@ type coreRepositories struct {
 	MemoryReader     MemoryBatchReader
 	MemoryRetriever  memory.SemanticRetriever
 	Memories         memory.Repository
 	MemoryLogs       memory.WriteLogRepository
 	MemoryRecallLogs memory.RecallLogRepository
-	MemoryCandidates memory.CandidateWriter
 	MemoryFiles      memory.DurableReader
 	AdHocNotes       memory.AdHocWriter
 	MessageHistory   MessageHistoryReader
 	MessageWriter    MessageWriter
 	Compactions      conversation.CompactionRepository
diff --git a/internal/runtime/toolruntime/memory_tools.go b/internal/runtime/toolruntime/memory_tools.go
index 5a15894..9749053 100644
--- a/internal/runtime/toolruntime/memory_tools.go
+++ b/internal/runtime/toolruntime/memory_tools.go
@@ -168,72 +168,10 @@ func (t MemoryReadTool) Execute(ctx context.Context, rc ToolRunContext, input js
 	return ResultFromValue(map[string]any{
 		"memories": result.Memories, "memory_context": result.MemoryContext, "count": result.Count, "query": result.Query, "recall_details": result.RecallDetails,
 	})
 }
 
-type MemoryWriteTool struct {
-	Memories   memory.Repository
-	Logs       memory.WriteLogRepository
-	Retriever  memory.SemanticRetriever
-	Archival   memory.ArchivalIndex
-	Candidates memory.CandidateWriter
-}
-
-type memoryWriteInput struct {
-	MemoryID           int64   `json:"memory_id"`
-	MemoryType         string  `json:"memory_type"`
-	Title              string  `json:"title"`
-	Content            string  `json:"content"`
-	Importance         float64 `json:"importance"`
-	Reason             string  `json:"reason"`
-	ConflictResolution string  `json:"conflict_resolution"`
-	Scope              string  `json:"scope"`
-}
-
-func (MemoryWriteTool) Name() string { return "write_memory" }
-
-func (MemoryWriteTool) Description() string {
-	return "Retired. Durable memory is written only by the asynchronous durable-memory consolidation pipeline."
-}
-
-func (MemoryWriteTool) Parameters() json.RawMessage {
-	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
-}
-
-func (MemoryWriteTool) Metadata() ToolMetadata {
-	return ToolMetadata{RiskLevel: RiskMedium, SideEffect: SideEffectWrite}
-}
-
-func (t MemoryWriteTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
-	if t.Candidates == nil {
-		return nil, fmt.Errorf("memory candidate service is not configured")
-	}
-	var parsed memoryWriteInput
-	if err := json.Unmarshal(input, &parsed); err != nil {
-		return &ToolResult{ContentText: err.Error(), IsError: true}, err
-	}
-	conversationID := int64(0)
-	if rc.ConversationID != nil {
-		conversationID = *rc.ConversationID
-	}
-	projectID := projectIDFromToolRunContext(rc)
-	action := "create"
-	if parsed.MemoryID > 0 {
-		action = "update"
-	}
-	proposalID, err := t.Candidates.Suggest(ctx, memory.CandidateRequest{OwnerID: rc.OwnerID, AgentID: rc.AgentID,
-		ConversationID: conversationID, ProjectID: projectID, SourceConversationID: conversationID, SourceProjectID: projectID, RunID: rc.RunID, ScopeType: strings.TrimSpace(parsed.Scope), SourceID: fmt.Sprintf("agent-tool:%d:%s", rc.RunID, strings.TrimSpace(parsed.Content)),
-		MemoryID: parsed.MemoryID, MemoryType: parsed.MemoryType, Title: parsed.Title, Content: parsed.Content,
-		Action: action, Importance: parsed.Importance, Evidence: []string{strings.TrimSpace(parsed.Reason)}, Source: "agent_tool"})
-	if err != nil {
-		return &ToolResult{ContentText: err.Error(), IsError: true}, err
-	}
-	return ResultFromValue(map[string]any{
-		"proposal_id": proposalID, "status": "pending", "action": "suggest", "content": strings.TrimSpace(parsed.Content),
-	})
-}
-
 func projectIDFromToolRunContext(rc ToolRunContext) int64 {
 	if rc.ProjectID > 0 {
 		return rc.ProjectID
 	}
 	if rc.Workspace != nil {
diff --git a/internal/runtime/toolruntime/memory_tools_test.go b/internal/runtime/toolruntime/memory_tools_test.go
index 22605cb..480dcf9 100644
--- a/internal/runtime/toolruntime/memory_tools_test.go
+++ b/internal/runtime/toolruntime/memory_tools_test.go
@@ -155,23 +155,10 @@ func (r *fakeMemoryRepo) UpdateDecayedImportance(ctx context.Context, ownerID in
 
 type fakeMemoryLogRepo struct {
 	items []memory.WriteLog
 }
 
-type fakeMemoryCandidateWriter struct {
-	request memory.CandidateRequest
-	id      int64
-}
-
-func (f *fakeMemoryCandidateWriter) Suggest(_ context.Context, request memory.CandidateRequest) (int64, error) {
-	f.request = request
-	if f.id == 0 {
-		f.id = 11
-	}
-	return f.id, nil
-}
-
 func (r *fakeMemoryLogRepo) Create(ctx context.Context, item *memory.WriteLog) error {
 	r.items = append(r.items, *item)
 	return nil
 }
 
@@ -220,35 +207,10 @@ func TestMemoryReadToolRequiresUnifiedVectorIndexByDefault(t *testing.T) {
 	if repo.readReq.limit != 0 {
 		t.Fatalf("memory list fallback must not be used: %+v", repo.readReq)
 	}
 }
 
-func TestMemoryWriteToolCreatesReviewCandidate(t *testing.T) {
-	candidates := &fakeMemoryCandidateWriter{}
-	tool := MemoryWriteTool{Candidates: candidates}
-	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, RunID: 2}, json.RawMessage(`{
-		"memory_type":"task",
-		"title":"Preference",
-		"content":"User prefers concise answers",
-		"importance":0.8,
-		"reason":"User stated preference"
-	}`))
-	if err != nil {
-		t.Fatal(err)
-	}
-	if candidates.request.Content != "User prefers concise answers" || candidates.request.RunID != 2 {
-		t.Fatalf("unexpected candidate: %+v", candidates.request)
-	}
-	var output map[string]any
-	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
-		t.Fatal(err)
-	}
-	if output["action"] != "suggest" || output["status"] != "pending" {
-		t.Fatalf("unexpected output: %+v", output)
-	}
-}
-
 type fakeSemanticRetriever struct {
 	ids []int64
 }
 
 func (r *fakeSemanticRetriever) Index(ctx context.Context, item memory.Memory) error {
@@ -319,22 +281,5 @@ func TestMemoryReadToolFallsBackWhenSearchFails(t *testing.T) {
 	if repo.readReq.limit != 3 {
 		t.Fatalf("expected fallback to ListForRead, got %+v", repo.readReq)
 	}
 }
 
-func TestMemoryWriteToolNeverSelfApprovesConflictingFact(t *testing.T) {
-	candidates := &fakeMemoryCandidateWriter{}
-	tool := MemoryWriteTool{Candidates: candidates}
-	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, RunID: 2}, json.RawMessage(`{"memory_type":"profile","title":"response style","content":"User prefers detailed answers"}`))
-	if err != nil || result == nil || result.Approval != nil || candidates.request.Content == "" {
-		t.Fatalf("expected pending proposal without direct memory approval, result=%+v candidate=%+v err=%v", result, candidates.request, err)
-	}
-}
-
-func TestMemoryWriteToolCarriesExplicitProjectScopeWithoutWorkspace(t *testing.T) {
-	candidates := &fakeMemoryCandidateWriter{}
-	tool := MemoryWriteTool{Candidates: candidates}
-	_, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 1, AgentID: 7, RunID: 2, ProjectID: 42}, json.RawMessage(`{"memory_type":"task","content":"project fact","scope":"project"}`))
-	if err != nil || candidates.request.ScopeType != memory.ScopeProject || candidates.request.SourceProjectID != 42 || candidates.request.AgentID != 7 {
-		t.Fatalf("project scope was not carried to pending candidate: request=%+v err=%v", candidates.request, err)
-	}
-}
