package workflow_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"agentcanvas/internal/domain/flow"
	"agentcanvas/internal/domain/workflow"
	"agentcanvas/internal/infrastructure/llm"
	agenterrors "agentcanvas/internal/pkg/errors"
	runtimeagent "agentcanvas/internal/runtime/agent"
	"agentcanvas/internal/runtime/engine"
	"agentcanvas/internal/runtime/harness/rules"
	runtimenode "agentcanvas/internal/runtime/node"
	"agentcanvas/internal/runtime/toolruntime"
)

func (s *Service) ListApprovalRequests(ctx context.Context, ownerID int64, status string) ([]workflow.ApprovalRequest, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("%w: approval repository is not configured", agenterrors.ErrInvalidInput)
	}
	return s.approvals.ListApprovalRequests(ctx, ownerID, strings.TrimSpace(status))
}

func (s *Service) ApproveRequest(ctx context.Context, ownerID, approvalID int64, req DecideApprovalRequest) (*workflow.ApprovalRequest, error) {
	return s.decideApproval(ctx, ownerID, approvalID, workflow.ApprovalStatusApproved, req.Note)
}

func (s *Service) RejectRequest(ctx context.Context, ownerID, approvalID int64, req DecideApprovalRequest) (*workflow.ApprovalRequest, error) {
	return s.decideApproval(ctx, ownerID, approvalID, workflow.ApprovalStatusRejected, req.Note)
}

func (s *Service) ResumeRun(ctx context.Context, ownerID, runID int64) (*workflow.Run, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("%w: approval repository is not configured", agenterrors.ErrInvalidInput)
	}
	item, err := s.GetRun(ctx, ownerID, runID)
	if err != nil {
		return nil, err
	}
	if item.Status != workflow.RunStatusWaitingHuman && item.Status != workflow.RunStatusPaused {
		return nil, fmt.Errorf("%w: run is not waiting for resume", agenterrors.ErrInvalidInput)
	}
	checkpoint, err := s.approvals.FindLatestCheckpointByRun(ctx, ownerID, runID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	approval, err := s.approvals.FindPendingApprovalByRun(ctx, ownerID, runID)
	if err == nil && approval.Status == workflow.ApprovalStatusPending {
		return nil, fmt.Errorf("%w: approval request is still pending", agenterrors.ErrInvalidInput)
	}
	if len(checkpoint.MessagesJSON) == 0 {
		return nil, fmt.Errorf("%w: checkpoint messages are missing", agenterrors.ErrInvalidInput)
	}
	item.Status = workflow.RunStatusResuming
	if err := s.runs.Update(ctx, item); err != nil {
		return nil, err
	}
	if item.RunKind == workflow.RunKindAgent {
		if s.agentRunResumer == nil {
			return nil, fmt.Errorf("%w: independent agent resumer is not configured", agenterrors.ErrInvalidInput)
		}
		decision, decisionErr := s.latestApprovalDecision(ctx, ownerID, runID)
		if decisionErr != nil {
			return nil, decisionErr
		}
		return s.agentRunResumer.ResumeIndependentRun(ctx, item, checkpoint, decision)
	}
	return s.resumeRunFromCheckpoint(ctx, item, checkpoint)
}

func (s *Service) decideApproval(
	ctx context.Context, ownerID, approvalID int64, status, note string,
) (*workflow.ApprovalRequest, error) {
	if s.approvals == nil {
		return nil, fmt.Errorf("%w: approval repository is not configured", agenterrors.ErrInvalidInput)
	}
	item, err := s.approvals.FindApprovalRequestByID(ctx, ownerID, approvalID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if item.Status != workflow.ApprovalStatusPending {
		return nil, fmt.Errorf("%w: approval request is already decided", agenterrors.ErrInvalidInput)
	}
	now := time.Now().UTC()
	item.Status = status
	item.DecisionNote = strings.TrimSpace(note)
	item.DecidedAt = &now
	if err := s.approvals.UpdateApprovalRequest(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) persistRunCheckpointArtifacts(
	ctx context.Context, run *workflow.Run, output engine.NodeOutput, checkpointStatus string,
) error {
	if s.approvals == nil || run == nil || output == nil {
		return nil
	}
	approval, ok := output["approval"].(*runtimeagent.Approval)
	if !ok {
		if raw, exists := output["approval"]; exists {
			bytes, _ := json.Marshal(raw)
			var decoded runtimeagent.Approval
			if err := json.Unmarshal(bytes, &decoded); err == nil {
				approval = &decoded
			}
		}
	}
	checkpoint, ok := output["checkpoint"].(*runtimeagent.Checkpoint)
	if !ok {
		if raw, exists := output["checkpoint"]; exists {
			bytes, _ := json.Marshal(raw)
			var decoded runtimeagent.Checkpoint
			if err := json.Unmarshal(bytes, &decoded); err == nil {
				checkpoint = &decoded
			}
		}
	}
	if approval != nil {
		requestJSON, _ := json.Marshal(approval)
		item := &workflow.ApprovalRequest{
			OwnerID:     run.OwnerID,
			WorkflowID:  run.WorkflowID,
			RunID:       run.ID,
			NodeID:      checkpointNodeID(checkpoint),
			ToolCallID:  approval.ToolCallID,
			ToolName:    approval.ToolName,
			RiskLevel:   approval.RiskLevel,
			Reason:      approval.Reason,
			RequestJSON: requestJSON,
			Status:      workflow.ApprovalStatusPending,
		}
		if item.NodeID == "" {
			item.NodeID = "agent"
		}
		if err := s.approvals.CreateApprovalRequest(ctx, item); err != nil {
			return err
		}
	}
	if checkpoint != nil {
		messagesJSON, _ := json.Marshal(checkpoint.Messages)
		stepsJSON, _ := json.Marshal(output["steps"])
		pendingJSON, _ := json.Marshal(checkpoint.PendingToolCall)
		contextJSON, _ := json.Marshal(checkpointContextEnvelope{
			Context:  checkpoint.Context,
			Metadata: checkpoint.Metadata,
		})
		item := &workflow.WorkflowCheckpoint{
			OwnerID:             run.OwnerID,
			WorkflowID:          run.WorkflowID,
			RunID:               run.ID,
			NodeID:              checkpointNodeID(checkpoint),
			Status:              checkpointStatus,
			MessagesJSON:        messagesJSON,
			MessagesSummary:     checkpoint.MessagesSummary,
			StepsJSON:           stepsJSON,
			PendingToolCallJSON: pendingJSON,
			ContextJSON:         contextJSON,
			ToolRegistryHash:    checkpointToolRegistryHash(checkpoint),
			ToolPolicyHash:      checkpointToolPolicyHash(checkpoint),
		}
		if item.NodeID == "" {
			item.NodeID = "agent"
		}
		if err := s.approvals.CreateCheckpoint(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func checkpointNodeID(checkpoint *runtimeagent.Checkpoint) string {
	if checkpoint == nil || checkpoint.Metadata == nil {
		return ""
	}
	nodeID, _ := checkpoint.Metadata["node_id"].(string)
	return nodeID
}

func checkpointToolRegistryHash(checkpoint *runtimeagent.Checkpoint) string {
	if checkpoint == nil {
		return ""
	}
	if len(checkpoint.ToolNames) > 0 {
		return stableJSONHash(checkpoint.ToolNames)
	}
	if checkpoint.Metadata != nil {
		if hash, _ := checkpoint.Metadata["tool_registry_hash"].(string); hash != "" {
			return hash
		}
	}
	return stableJSONHash(checkpoint.ToolNames)
}

func checkpointToolPolicyHash(checkpoint *runtimeagent.Checkpoint) string {
	if checkpoint == nil {
		return ""
	}
	if checkpoint.Metadata != nil {
		if hash, _ := checkpoint.Metadata["tool_policy_hash"].(string); hash != "" && isZeroToolPolicy(checkpoint.ToolPolicy) {
			return hash
		}
	}
	return stableJSONHash(checkpoint.ToolPolicy)
}

func isZeroToolPolicy(policy runtimeagent.ToolPolicy) bool {
	return len(policy.RequireApprovalForRisk) == 0 && policy.MaxToolTimeoutMS == 0 && policy.MaxToolOutputBytes == 0 && len(policy.AllowedHosts) == 0
}

func (s *Service) resumeRunFromCheckpoint(ctx context.Context, run *workflow.Run, stored *workflow.WorkflowCheckpoint) (*workflow.Run, error) {
	if run == nil || stored == nil {
		return nil, agenterrors.ErrInvalidInput
	}
	decision, err := s.latestApprovalDecision(ctx, run.OwnerID, run.ID)
	if err != nil {
		return nil, err
	}
	checkpoint, err := decodeRuntimeCheckpoint(stored, decision)
	if err != nil {
		return nil, err
	}
	if checkpoint.PendingToolCall != nil && decision == nil {
		return nil, fmt.Errorf("%w: approval decision is missing", agenterrors.ErrInvalidInput)
	}
	var dsl *flow.DSL
	if run.RunKind == workflow.RunKindInlineAgent {
		var definition toolruntime.InlineAgentDefinition
		if err := json.Unmarshal(run.DefinitionJSON, &definition); err != nil {
			return nil, fmt.Errorf("%w: invalid inline agent definition", agenterrors.ErrInvalidInput)
		}
		hash := sha256.Sum256(run.DefinitionJSON)
		if run.DefinitionHash == "" || run.DefinitionHash != hex.EncodeToString(hash[:]) {
			return nil, fmt.Errorf("%w: inline agent definition hash mismatch", agenterrors.ErrForbidden)
		}
		dsl = inlineAgentDSL(definition, false)
	} else {
		version, err := s.GetWorkflowVersion(ctx, run.OwnerID, run.FlowVersionID)
		if err != nil {
			return nil, err
		}
		var parseErr error
		dsl, parseErr = flow.ParseDSL(version.DSLJSON)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: invalid dsl_json", agenterrors.ErrInvalidInput)
		}
	}
	nodeSpec, err := findCheckpointAgentNode(dsl, stored.NodeID)
	if err != nil {
		return nil, err
	}
	input := map[string]any{}
	if len(run.InputJSON) > 0 {
		_ = json.Unmarshal(run.InputJSON, &input)
	}
	callChain := []int64{run.WorkflowID}
	if len(run.CallChainJSON) > 0 {
		_ = json.Unmarshal(run.CallChainJSON, &callChain)
	}
	var pinnedRules *rules.CompiledRuleSet
	if run.RuleSetID != nil {
		pinnedRules, err = s.loadPinnedRuleSet(ctx, run.OwnerID, run.WorkflowID, *run.RuleSetID)
		if err != nil {
			return nil, err
		}
	}
	node := s.resumeAgentLoopNode()
	rc := &engine.RunContext{
		OwnerID:           run.OwnerID,
		WorkflowID:        run.WorkflowID,
		FlowVersionID:     run.FlowVersionID,
		RuleSetID:         ruleSetIDValue(run.RuleSetID),
		RuleSetVersion:    run.RuleSetVersion,
		CompiledRuleHash:  run.CompiledRuleHash,
		CompiledRules:     pinnedRules,
		RunID:             run.ID,
		ParentRunID:       run.ParentRunID,
		CallDepth:         run.CallDepth,
		WorkflowCallChain: append([]int64(nil), callChain...),
		ConversationID:    run.ConversationID,
		Input:             input,
		NodeInputs:        map[string]engine.NodeInput{},
		NodeOutputs:       map[string]engine.NodeOutput{},
		NodeErrors:        map[string]string{},
		NodeLatencies:     map[string]int{},
		ExecutedNodes:     map[string]bool{},
		CurrentNodeID:     nodeSpec.ID,
		CurrentNodeType:   nodeSpec.Type,
		AgentSteps:        s,
		Events: &eventEmitter{
			repo:    s.events,
			ownerID: run.OwnerID,
			runID:   run.ID,
		}}
	started := time.Now().UTC()
	execCtx, cancel := context.WithCancel(ctx)
	s.runCancels.Register(run.ID, cancel)
	defer func() {
		cancel()
		s.runCancels.Unregister(run.ID)
	}()
	output, execErr := node.Resume(
		execCtx, rc, engine.NodeInput(input), nodeSpec.Config,
		runtimenode.AgentResumeOptions{
			Checkpoint:    checkpoint,
			Approved:      decision != nil && decision.Status == workflow.ApprovalStatusApproved,
			RejectionNote: approvalDecisionNote(decision),
		})
	finished := time.Now().UTC()
	run.FinishedAt = &finished
	run.LatencyMS += int(finished.Sub(started).Milliseconds())
	if output != nil {
		run.OutputJSON, _ = json.Marshal(output)
		rc.NodeOutputs[nodeSpec.ID] = output
		rc.ExecutedNodes[nodeSpec.ID] = true
		rc.NodeLatencies[nodeSpec.ID] = int(finished.Sub(started).Milliseconds())
	}
	if errors.Is(execErr, context.Canceled) || execCtx.Err() == context.Canceled {
		run.Status = workflow.RunStatusCancelled
		run.ErrorMessage = context.Canceled.Error()
	} else if execErr != nil {
		run.Status = workflow.RunStatusFailed
		run.ErrorMessage = execErr.Error()
	} else if status := runStatusFromOutput(output); status != "" {
		run.Status = status
		run.ErrorMessage = ""
	} else {
		run.Status = workflow.RunStatusSucceeded
		run.ErrorMessage = ""
	}
	if run.Status == workflow.RunStatusWaitingHuman {
		if err := s.persistRunCheckpointArtifacts(ctx, run, output, workflow.RunStatusWaitingHuman); err != nil {
			return run, err
		}
	}
	if run.Status == workflow.RunStatusPaused {
		if err := s.persistRunCheckpointArtifacts(ctx, run, output, workflow.RunStatusPaused); err != nil {
			return run, err
		}
	}
	if err := s.runs.Update(ctx, run); err != nil {
		return run, err
	}
	_ = s.writeNodeLogs(ctx, run.OwnerID, run.ID, dsl, rc)
	return run, execErr
}

func (s *Service) resumeAgentLoopNode() runtimenode.AgentLoopNode {
	workspaceRoot, _ := os.Getwd()
	return runtimenode.AgentLoopNode{AgentNode: runtimenode.AgentNode{LLM: s.llm.(llm.ToolCallingClient), Providers: s, Tools: s.toolRegistry, ToolPacks: s.toolPacks, Skills: s.skills, Audits: s.audits, MCPServers: s.mcpServers, Retriever: s.retriever, MemoryRetriever: s.memoryRetriever, Memories: s.memories, MemoryLogs: s.memoryLogs, WorkingMemory: s.workingMemory, OnExtractTrigger: s.triggerMemoryExtraction, WorkflowCaller: s, InlineAgentCaller: s, Profiles: s, RuleSets: s, Reflections: s.reflections, MessageHistory: s.messages, Compactions: s.compactions, ArchivalVecStore: s.archivalVecStore, ContextIndex: s.contextIndex, Embedder: s.embedder, WorkspaceRoot: workspaceRoot}}
}

func decodeRuntimeCheckpoint(stored *workflow.WorkflowCheckpoint, decision *workflow.ApprovalRequest) (*runtimeagent.Checkpoint, error) {
	var messages []llm.ChatMessage
	if err := json.Unmarshal(stored.MessagesJSON, &messages); err != nil {
		return nil, fmt.Errorf("%w: invalid checkpoint messages", agenterrors.ErrInvalidInput)
	}
	var pending *llm.ToolCall
	if len(stored.PendingToolCallJSON) > 0 && string(stored.PendingToolCallJSON) != "null" {
		var call llm.ToolCall
		if err := json.Unmarshal(stored.PendingToolCallJSON, &call); err != nil {
			return nil, fmt.Errorf("%w: invalid checkpoint pending tool call", agenterrors.ErrInvalidInput)
		}
		pending = &call
	}
	var contextTrace runtimeagent.ContextTrace
	metadata := map[string]any{}
	if len(stored.ContextJSON) > 0 {
		contextTrace, metadata = decodeCheckpointContext(stored.ContextJSON)
	}
	metadata["node_id"] = stored.NodeID
	metadata["approval_status"] = approvalDecisionStatus(decision)
	metadata["approval_note"] = approvalDecisionNote(decision)
	metadata["checkpoint_id"] = stored.ID
	metadata["checkpoint_state"] = stored.Status
	metadata["tool_registry_hash"] = stored.ToolRegistryHash
	metadata["tool_policy_hash"] = stored.ToolPolicyHash
	return &runtimeagent.Checkpoint{Messages: messages, MessagesSummary: stored.MessagesSummary, PendingToolCall: pending, Context: contextTrace, Metadata: metadata}, nil
}

type checkpointContextEnvelope struct {
	Context  runtimeagent.ContextTrace `json:"context"`
	Metadata map[string]any            `json:"metadata,omitempty"`
}

func decodeCheckpointContext(raw json.RawMessage) (runtimeagent.ContextTrace, map[string]any) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err == nil {
		if _, ok := root["context"]; ok {
			var envelope checkpointContextEnvelope
			_ = json.Unmarshal(raw, &envelope)
			if envelope.Metadata == nil {
				envelope.Metadata = map[string]any{}
			}
			return envelope.Context, envelope.Metadata
		}
	}
	var trace runtimeagent.ContextTrace
	_ = json.Unmarshal(raw, &trace)
	return trace, map[string]any{}
}

func findCheckpointAgentNode(dsl *flow.DSL, nodeID string) (*flow.Node, error) {
	if dsl == nil {
		return nil, fmt.Errorf("%w: flow version is missing", agenterrors.ErrInvalidInput)
	}
	var fallback *flow.Node
	for i := range dsl.Nodes {
		node := &dsl.Nodes[i]
		if node.Type != "agent_loop" {
			continue
		}
		if fallback == nil {
			fallback = node
		}
		if node.ID == nodeID {
			return node, nil
		}
	}
	if nodeID == "" && fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("%w: checkpoint node %s is not an agent_loop node", agenterrors.ErrInvalidInput, nodeID)
}

func (s *Service) latestApprovalDecision(ctx context.Context, ownerID, runID int64) (*workflow.ApprovalRequest, error) {
	items, err := s.approvals.ListApprovalRequests(ctx, ownerID, "")
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.RunID != runID {
			continue
		}
		if item.Status == workflow.ApprovalStatusPending {
			return nil, fmt.Errorf("%w: approval request is still pending", agenterrors.ErrInvalidInput)
		}
		clone := item
		return &clone, nil
	}
	return nil, nil
}

func approvalDecisionStatus(decision *workflow.ApprovalRequest) string {
	if decision == nil {
		return ""
	}
	return decision.Status
}

func approvalDecisionNote(decision *workflow.ApprovalRequest) string {
	if decision == nil {
		return ""
	}
	return decision.DecisionNote
}
