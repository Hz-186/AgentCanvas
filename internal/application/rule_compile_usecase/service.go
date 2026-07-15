package rule_compile_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/domain/workflow"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/observability"
	"agentcanvas/internal/runtime/harness/rules"

	"gorm.io/gorm"
)

const CompilerPromptVersion = "rule-graph-v1"

type Service struct {
	ruleSets  workflow.RuleSetRepository
	providers providerdomain.Repository
	secrets   *cryptoinfra.SecretBox
	llm       llm.ToolCallingClient
}

func NewService(ruleSets workflow.RuleSetRepository, providers providerdomain.Repository, secrets *cryptoinfra.SecretBox, client llm.ToolCallingClient) *Service {
	return &Service{ruleSets: ruleSets, providers: providers, secrets: secrets, llm: client}
}

func (s *Service) ProcessNext(ctx context.Context, workerID string) (bool, error) {
	job, err := s.ruleSets.ClaimNextCompileJob(ctx, workerID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		if err == workflow.ErrRuleCompileStale {
			observability.RuleSystemMetrics.RecordCompileStale()
			return true, nil
		}
		return false, err
	}
	return true, s.processClaimed(ctx, job)
}

func (s *Service) ProcessByID(ctx context.Context, jobID int64, workerID string) error {
	job, err := s.ruleSets.ClaimCompileJob(ctx, jobID, workerID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		if err == workflow.ErrRuleCompileStale {
			observability.RuleSystemMetrics.RecordCompileStale()
			return nil
		}
		return err
	}
	return s.processClaimed(ctx, job)
}

func (s *Service) processClaimed(ctx context.Context, job *workflow.RuleCompileJob) error {
	observability.RuleSystemMetrics.RecordCompileStarted()
	err := s.compile(ctx, job)
	if err == nil {
		observability.RuleSystemMetrics.RecordCompileCompleted()
		return nil
	}
	if err == workflow.ErrRuleCompileStale {
		observability.RuleSystemMetrics.RecordCompileStale()
		return nil
	}
	var retryAt *time.Time
	if job.Attempts < 3 {
		next := time.Now().UTC().Add(time.Duration(1<<max(job.Attempts-1, 0)) * time.Second)
		retryAt = &next
	}
	observability.RuleSystemMetrics.RecordCompileFailure(err, retryAt != nil)
	if failErr := s.ruleSets.FailCompilation(ctx, job, err, retryAt); failErr != nil {
		return fmt.Errorf("compile failed: %v; persist failure: %w", err, failErr)
	}
	return err
}

func (s *Service) compile(ctx context.Context, job *workflow.RuleCompileJob) error {
	set, err := s.ruleSets.FindByID(ctx, job.OwnerID, job.WorkflowID, job.RuleSetID)
	if err != nil {
		return err
	}
	items, err := rulesFromRows(set.Nodes, set.Edges, false)
	if err != nil {
		return err
	}
	suggestions := []workflow.RuleEdge{}
	if len(items) > 1 {
		suggestions, err = s.extractDependencies(ctx, job, items)
		if err != nil {
			return err
		}
		suggestions = excludeExistingDependencies(suggestions, set.Edges)
	}
	edges := make([]rules.DependencyEdge, 0, len(suggestions))
	for _, edge := range suggestions {
		edges = append(edges, rules.DependencyEdge{
			RuleID: edge.RuleID, DependsOn: edge.DependsOnRuleID,
			Source: "llm", Confidence: edge.Confidence, Reason: edge.Reason, Decision: "accepted",
		})
	}
	compiled, err := rules.CompileRuleSet(items, rules.CompileOptions{
		RuleSetID: set.ID, Version: strconv.Itoa(set.VersionNo), Edges: edges,
	})
	if err != nil {
		return fmt.Errorf("deterministic DAG validation failed: %w", err)
	}
	snapshot, err := json.Marshal(compiled)
	if err != nil {
		return err
	}
	nodes, err := compiledRows(compiled)
	if err != nil {
		return err
	}
	nextStatus := workflow.RuleSetStatusReady
	if len(suggestions) > 0 {
		nextStatus = workflow.RuleSetStatusReviewRequired
	}
	if err := s.ruleSets.CompleteCompilation(ctx, job, nodes, suggestions, snapshot, compiled.CompiledHash, compiled.TokenEstimatorVersion, nextStatus); err != nil {
		return err
	}
	if nextStatus == workflow.RuleSetStatusReady {
		ready, err := s.ruleSets.FindByID(ctx, job.OwnerID, job.WorkflowID, job.RuleSetID)
		if err != nil {
			return err
		}
		if err := s.ruleSets.PublishCompiled(ctx, ready, nodes, ready.Edges, snapshot, compiled.CompiledHash, compiled.TokenEstimatorVersion, job.OwnerID); err != nil {
			return err
		}
		observability.RuleSystemMetrics.RecordPublished()
	}
	return nil
}

func excludeExistingDependencies(suggestions, existing []workflow.RuleEdge) []workflow.RuleEdge {
	known := make(map[string]bool, len(existing))
	for _, edge := range existing {
		if edge.Decision == workflow.RuleEdgeDecisionRejected {
			continue
		}
		known[edge.RuleID+"\x00"+edge.DependsOnRuleID] = true
	}
	out := make([]workflow.RuleEdge, 0, len(suggestions))
	for _, edge := range suggestions {
		key := edge.RuleID + "\x00" + edge.DependsOnRuleID
		if known[key] {
			continue
		}
		known[key] = true
		out = append(out, edge)
	}
	return out
}

func (s *Service) extractDependencies(ctx context.Context, job *workflow.RuleCompileJob, items []rules.Rule) ([]workflow.RuleEdge, error) {
	if job.CompilerProviderID == nil || *job.CompilerProviderID <= 0 {
		return nil, fmt.Errorf("rule compiler provider is not configured")
	}
	if s.providers == nil || s.secrets == nil || s.llm == nil {
		return nil, fmt.Errorf("rule compiler dependencies are not configured")
	}
	provider, err := s.providers.FindByID(ctx, job.OwnerID, *job.CompilerProviderID)
	if err != nil {
		return nil, err
	}
	if provider.Status != providerdomain.StatusActive {
		return nil, fmt.Errorf("rule compiler provider is disabled")
	}
	apiKey, err := s.secrets.Decrypt(provider.EncryptedAPIKey)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(job.CompilerModel)
	if model == "" {
		model = strings.TrimSpace(provider.DefaultChatModel)
	}
	if model == "" {
		return nil, fmt.Errorf("rule compiler model is not configured")
	}
	payload := make([]map[string]string, 0, len(items))
	for _, item := range items {
		payload = append(payload, map[string]string{"id": item.ID, "content": item.Content})
	}
	sort.Slice(payload, func(i, j int) bool { return payload[i]["id"] < payload[j]["id"] })
	rulesJSON, _ := json.Marshal(payload)
	temperature := 0.0
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	response, err := s.llm.ChatWithTools(callCtx, llm.ChatProviderConfig{
		ProviderType: provider.ProviderType, BaseURL: provider.BaseURL, APIKey: apiKey,
	}, llm.ToolChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "Analyze only semantic prerequisite relationships between the supplied rules. Do not classify rule strength, safety, policy, or rewrite rule content. Submit zero or more candidate directed edges through submit_rule_graph."},
			{Role: "user", Content: "Rules:\n" + string(rulesJSON)},
		},
		Tools: []llm.ToolDefinition{{
			Type: "function",
			Function: llm.ToolFunctionDefinition{
				Name: "submit_rule_graph", Description: "Submit candidate rule prerequisite edges for human review.", Strict: true,
				Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"edges":{"type":"array","maxItems":200,"items":{"type":"object","additionalProperties":false,"properties":{"rule_id":{"type":"string"},"depends_on":{"type":"string"},"confidence":{"type":"number","minimum":0,"maximum":1},"reason":{"type":"string","maxLength":1000}},"required":["rule_id","depends_on","confidence","reason"]}}},"required":["edges"]}`),
			},
		}},
		ToolChoice:  map[string]any{"type": "function", "function": map[string]string{"name": "submit_rule_graph"}},
		Temperature: &temperature,
	})
	if err != nil {
		return nil, err
	}
	job.PromptTokens = response.Usage.PromptTokens
	job.CompletionTokens = response.Usage.CompletionTokens
	if len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].Name != "submit_rule_graph" {
		return nil, fmt.Errorf("rule compiler must return exactly one submit_rule_graph call")
	}
	return parseDependencySuggestions(response.Message.ToolCalls[0].Arguments, items)
}

func parseDependencySuggestions(raw json.RawMessage, items []rules.Rule) ([]workflow.RuleEdge, error) {
	if len(raw) > 256*1024 {
		return nil, fmt.Errorf("submit_rule_graph payload exceeds 262144 bytes")
	}
	var result struct {
		Edges []struct {
			RuleID     string  `json:"rule_id"`
			DependsOn  string  `json:"depends_on"`
			Confidence float64 `json:"confidence"`
			Reason     string  `json:"reason"`
		} `json:"edges"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid submit_rule_graph payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("invalid submit_rule_graph payload: trailing JSON content")
	}
	if len(result.Edges) > 200 {
		return nil, fmt.Errorf("rule compiler returned more than 200 edges")
	}
	known := make(map[string]bool, len(items))
	for _, item := range items {
		known[item.ID] = true
	}
	seen := map[string]bool{}
	edges := make([]workflow.RuleEdge, 0, len(result.Edges))
	for _, candidate := range result.Edges {
		candidate.RuleID = strings.TrimSpace(candidate.RuleID)
		candidate.DependsOn = strings.TrimSpace(candidate.DependsOn)
		candidate.Reason = strings.TrimSpace(candidate.Reason)
		if !known[candidate.RuleID] || !known[candidate.DependsOn] {
			return nil, fmt.Errorf("rule compiler referenced an unknown rule")
		}
		if candidate.RuleID == candidate.DependsOn {
			return nil, fmt.Errorf("rule compiler returned a self dependency for %s", candidate.RuleID)
		}
		if candidate.Confidence < 0 || candidate.Confidence > 1 || candidate.Reason == "" || len(candidate.Reason) > 1000 {
			return nil, fmt.Errorf("rule compiler returned an invalid confidence or reason")
		}
		key := candidate.RuleID + "\x00" + candidate.DependsOn
		if seen[key] {
			continue
		}
		seen[key] = true
		edges = append(edges, workflow.RuleEdge{
			RuleID: candidate.RuleID, DependsOnRuleID: candidate.DependsOn,
			Source: "llm", Confidence: candidate.Confidence, Reason: candidate.Reason,
			Decision: workflow.RuleEdgeDecisionPending,
		})
	}
	return edges, nil
}

func rulesFromRows(nodes []workflow.RuleNode, edges []workflow.RuleEdge, includeLLM bool) ([]rules.Rule, error) {
	dependencies := map[string][]string{}
	for _, edge := range edges {
		if edge.Decision != workflow.RuleEdgeDecisionAccepted || (edge.Source == "llm" && !includeLLM) {
			continue
		}
		dependencies[edge.RuleID] = append(dependencies[edge.RuleID], edge.DependsOnRuleID)
	}
	items := make([]rules.Rule, 0, len(nodes))
	for _, node := range nodes {
		var activation rules.Activation
		if len(node.ActivationJSON) > 0 {
			if err := json.Unmarshal(node.ActivationJSON, &activation); err != nil {
				return nil, err
			}
		}
		var binding *rules.PolicyBinding
		if len(node.PolicyBindingJSON) > 0 && string(node.PolicyBindingJSON) != "null" {
			binding = &rules.PolicyBinding{}
			if err := json.Unmarshal(node.PolicyBindingJSON, binding); err != nil {
				return nil, err
			}
		}
		items = append(items, rules.Rule{
			ID: node.RuleID, Name: node.Name, Content: node.Content,
			Strength: rules.RuleStrength(node.Strength), Activation: activation,
			Priority: node.Priority, SafetyCritical: node.SafetyCritical,
			PolicyBinding: binding, ManualDependsOn: append([]string(nil), dependencies[node.RuleID]...),
		})
	}
	return items, nil
}

func compiledRows(compiled *rules.CompiledRuleSet) ([]workflow.RuleNode, error) {
	nodes := make([]workflow.RuleNode, 0, len(compiled.Rules))
	for _, item := range compiled.Rules {
		activation, err := json.Marshal(item.Rule.Activation)
		if err != nil {
			return nil, err
		}
		var binding json.RawMessage
		if item.Rule.PolicyBinding != nil {
			binding, err = json.Marshal(item.Rule.PolicyBinding)
			if err != nil {
				return nil, err
			}
		}
		nodes = append(nodes, workflow.RuleNode{
			RuleID: item.Rule.ID, Name: item.Rule.Name, Content: item.Rule.Content,
			Strength: string(item.Rule.Strength), ActivationJSON: activation,
			Priority: item.Rule.Priority, SafetyCritical: item.Rule.SafetyCritical,
			PolicyBindingJSON: binding, TokenCost: item.TokenCost,
			TopologicalOrder: item.TopologicalOrder, ContentHash: item.ContentHash,
		})
	}
	return nodes, nil
}
