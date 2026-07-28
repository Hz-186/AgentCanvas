package memory_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/memory"
)

// CandidateService is shared by Agent tools, Dream, extraction and review
// APIs. It writes proposals only; applying one requires an explicit approval.
type CandidateService struct {
	repository agentdomain.ImprovementRepository
}

func NewCandidateService(repository agentdomain.ImprovementRepository) *CandidateService {
	return &CandidateService{repository: repository}
}

func (s *CandidateService) Suggest(ctx context.Context, request memory.CandidateRequest) (int64, error) {
	if s == nil || s.repository == nil {
		return 0, fmt.Errorf("memory candidate repository is not configured")
	}
	content := strings.TrimSpace(request.Content)
	if request.OwnerID <= 0 || content == "" {
		return 0, fmt.Errorf("memory candidate owner_id and content are required")
	}
	memoryType := strings.TrimSpace(request.MemoryType)
	if memoryType == "" {
		memoryType = memory.TypeProfile
	}
	action := strings.TrimSpace(request.Action)
	if action == "" {
		action = "create"
	}
	scopeType, scopeID := memory.ScopeUser, request.OwnerID
	if request.ConversationID > 0 {
		scopeType, scopeID = memory.ScopeConversation, request.ConversationID
	}
	payload, _ := json.Marshal(map[string]any{
		"memory_id": request.MemoryID, "memory_type": memoryType, "level": memory.LevelLongTerm,
		"action": action, "conversation_id": request.ConversationID, "scope_type": scopeType,
		"scope_id": scopeID, "source": strings.TrimSpace(request.Source), "source_id": request.SourceID,
	})
	evidence, _ := json.Marshal(request.Evidence)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%d\x00%s\x00%s\x00%s\x00%s", request.OwnerID, request.AgentID, request.SourceID, action, memoryType, content)))
	confidence := request.Importance
	if confidence <= 0 {
		confidence = .8
	}
	if confidence > 1 {
		confidence = 1
	}
	securityStatus, securityReason := memoryCandidateSecurity(content + "\n" + string(payload))
	status := agentdomain.ProposalStatusPending
	if securityStatus != "passed" {
		status = agentdomain.ProposalStatusRejectedSecurity
	}
	item := &agentdomain.ChangeProposal{OwnerID: request.OwnerID, AgentID: request.AgentID, RunID: request.RunID,
		Kind: agentdomain.ProposalKindMemory, Title: strings.TrimSpace(request.Title), Content: content,
		PayloadJSON: payload, EvidenceJSON: evidence, DiffJSON: json.RawMessage(`{}`), Confidence: confidence,
		Checksum: hex.EncodeToString(sum[:]), SecurityStatus: securityStatus, SecurityReason: securityReason, Status: status}
	if err := s.repository.CreateProposal(ctx, item); err != nil {
		return 0, err
	}
	return item.ID, nil
}

func (s *CandidateService) List(ctx context.Context, ownerID int64, status string, limit int) ([]agentdomain.ChangeProposal, error) {
	items, err := s.repository.ListProposals(ctx, ownerID, 0, strings.TrimSpace(status), limit)
	if err != nil {
		return nil, err
	}
	result := make([]agentdomain.ChangeProposal, 0, len(items))
	for i := range items {
		if items[i].Kind == agentdomain.ProposalKindMemory {
			result = append(result, items[i])
		}
	}
	return result, nil
}

var _ memory.CandidateWriter = (*CandidateService)(nil)

var memoryCandidateSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|secret|password|token)\s*[:=]\s*[^\s,;]{8,}`),
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._-]{12,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]+PRIVATE KEY-----`),
}

func memoryCandidateSecurity(value string) (string, string) {
	lower := strings.ToLower(value)
	for _, marker := range []string{"ignore previous instructions", "ignore all previous", "reveal system prompt", "developer message", "bypass approval", "disable safety"} {
		if strings.Contains(lower, marker) {
			return "blocked", "prompt_injection_pattern"
		}
	}
	for _, pattern := range memoryCandidateSecretPatterns {
		if pattern.MatchString(value) {
			return "blocked", "sensitive_information_pattern"
		}
	}
	return "passed", ""
}
