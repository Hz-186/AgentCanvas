package memory_usecase

import (
	"context"
	"errors"
	"regexp"
	"strings"

	agentdomain "agentcanvas/internal/domain/agent"
	"agentcanvas/internal/domain/memory"
)

// ErrDurableMemoryWritesDisabled marks the retired candidate path. Durable
// memory is now produced only by the Codex extraction/consolidation pipeline.
var ErrDurableMemoryWritesDisabled = errors.New("durable memory candidate writes are disabled; use the Codex consolidation pipeline")

// CandidateService is shared by Agent tools, Dream, extraction and review
// APIs. It writes proposals only; applying one requires an explicit approval.
type CandidateService struct {
	repository agentdomain.ImprovementRepository
}

func NewCandidateService(repository agentdomain.ImprovementRepository) *CandidateService {
	return &CandidateService{repository: repository}
}

func (s *CandidateService) Suggest(ctx context.Context, request memory.CandidateRequest) (int64, error) {
	// Keep the method for source compatibility, but make the retired writer an
	// unconditional hard stop so accidental re-wiring cannot create proposals.
	return 0, ErrDurableMemoryWritesDisabled
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
