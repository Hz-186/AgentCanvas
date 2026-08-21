package memory_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/memory"
)

type ExtractionService struct {
	memories    memory.Repository
	extractions memory.ExtractionJobRepository
	merges      memory.MergeLogRepository
	messages    DreamMessageRepository
	candidates  memory.CandidateWriter
}

func (s *ExtractionService) ConfigureCandidates(candidates memory.CandidateWriter) {
	s.candidates = candidates
}

func NewExtractionService(memories memory.Repository, extractions memory.ExtractionJobRepository, merges memory.MergeLogRepository, messageRepositories ...DreamMessageRepository) *ExtractionService {
	service := &ExtractionService{
		memories:    memories,
		extractions: extractions,
		merges:      merges,
	}
	if len(messageRepositories) > 0 {
		service.messages = messageRepositories[0]
	}
	return service
}

func (s *ExtractionService) ScheduleDream(ctx context.Context, ownerID, conversationID int64, roundNumber int, cfg DreamConfig) (*memory.ExtractionJob, error) {
	if s == nil || s.extractions == nil || s.messages == nil || ownerID <= 0 || conversationID <= 0 {
		return nil, nil
	}
	messages, err := s.messages.ListActiveByConversation(ctx, ownerID, conversationID)
	if err != nil || len(messages) == 0 {
		return nil, err
	}
	through := messages[len(messages)-1].ID
	ids := make([]int64, 0, len(messages))
	for _, item := range messages {
		ids = append(ids, item.ID)
	}
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal dream message ids: %w", err)
	}
	now := time.Now().UTC()
	due := now.Add(cfg.IdleTimeout)
	reason := "idle"
	if cfg.TriggerEveryNTurns > 0 && roundNumber > 0 && roundNumber%cfg.TriggerEveryNTurns == 0 {
		due = now
		reason = "turns"
	}
	key := fmt.Sprintf("dream:%d:%d:%d", ownerID, conversationID, through)
	if existing, findErr := s.extractions.FindByIdempotencyKey(ctx, ownerID, key); findErr == nil {
		if existing.DueAt == nil || due.Before(*existing.DueAt) {
			existing.DueAt = &due
			existing.TriggerReason = reason
			if err := s.extractions.Update(ctx, existing); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}
	job := &memory.ExtractionJob{OwnerID: ownerID, ConversationID: conversationID, IdempotencyKey: key, TriggerReason: reason,
		SourceMessageIDs: idsJSON, ThroughMessageID: through, Status: string(memory.ExtractionPending), DueAt: &due}
	if err := s.extractions.Create(ctx, job); err != nil {
		if existing, findErr := s.extractions.FindByIdempotencyKey(ctx, ownerID, key); findErr == nil {
			return existing, nil
		}
		return nil, err
	}
	return job, nil
}

func (s *ExtractionService) StartExtraction(ctx context.Context, ownerID, conversationID int64, messageIDs []int64) (int64, error) {
	if existingID, ok := s.findOpenJob(ctx, ownerID, conversationID); ok {
		return existingID, nil
	}
	idsJSON, err := json.Marshal(messageIDs)
	if err != nil {
		return 0, fmt.Errorf("marshal extraction message ids: %w", err)
	}
	job := &memory.ExtractionJob{
		OwnerID:          ownerID,
		ConversationID:   conversationID,
		SourceMessageIDs: idsJSON,
		Status:           string(memory.ExtractionPending),
	}
	if err := s.extractions.Create(ctx, job); err != nil {
		return 0, err
	}
	return job.ID, nil
}

// ProcessNextDream drains legacy extraction rows through the canonical Dream
// pipeline. New turns publish Dream jobs directly, so this compatibility
// consumer closes old pending jobs without applying a second extraction pass.
func (s *ExtractionService) ProcessNextDream(ctx context.Context, dream *DreamWorker) (bool, error) {
	if s == nil || s.extractions == nil || dream == nil {
		return false, nil
	}
	jobs, err := s.extractions.ListPending(ctx, 1)
	if err != nil || len(jobs) == 0 {
		return false, err
	}
	job := jobs[0]
	if err := dream.HandleDreamJob(ctx, DreamPayload{JobID: job.ID, OwnerID: job.OwnerID, ConversationID: job.ConversationID}); err != nil {
		return true, err
	}
	return true, nil
}

func (s *ExtractionService) findOpenJob(ctx context.Context, ownerID, conversationID int64) (int64, bool) {
	for _, status := range []string{string(memory.ExtractionPending), string(memory.ExtractionRunning)} {
		jobs, err := s.extractions.ListByStatus(ctx, ownerID, status, 20)
		if err != nil {
			continue
		}
		for _, job := range jobs {
			if job.ConversationID == conversationID {
				return job.ID, true
			}
		}
	}
	return 0, false
}

func (s *ExtractionService) CompleteExtraction(ctx context.Context, jobID, ownerID int64, result *memory.ExtractionResult) error {
	job, err := s.extractions.FindByID(ctx, ownerID, jobID)
	if err != nil {
		return err
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal extraction result: %w", err)
	}
	job.Status = string(memory.ExtractionCompleted)
	job.ResultJSON = resultJSON
	if result == nil {
		return s.extractions.Update(ctx, job)
	}
	if err := s.applyExtractionResults(ctx, job, result); err != nil {
		job.Status = string(memory.ExtractionFailed)
		job.ErrorMessage = err.Error()
		if updateErr := s.extractions.Update(ctx, job); updateErr != nil {
			return fmt.Errorf("apply extraction result: %v; persist failure state: %w", err, updateErr)
		}
		return err
	}
	return s.extractions.Update(ctx, job)
}

func (s *ExtractionService) FailExtraction(ctx context.Context, jobID, ownerID int64, errMsg string) error {
	job, err := s.extractions.FindByID(ctx, ownerID, jobID)
	if err != nil {
		return err
	}
	job.Status = string(memory.ExtractionFailed)
	job.ErrorMessage = errMsg
	return s.extractions.Update(ctx, job)
}

func (s *ExtractionService) applyExtractionResults(ctx context.Context, job *memory.ExtractionJob, result *memory.ExtractionResult) error {
	if s.candidates == nil {
		return fmt.Errorf("memory candidate service is not configured")
	}
	allItems := make([]struct {
		memoryType string
		item       memory.ExtractedMemoryItem
	}, 0)
	for _, item := range result.ProfileMemories {
		allItems = append(allItems, struct {
			memoryType string
			item       memory.ExtractedMemoryItem
		}{memory.TypeProfile, item})
	}
	// summary_memory is a read-only compatibility type. Conversation continuity
	// is owned exclusively by conversation_compactions in V2.
	for _, item := range result.EpisodicMemories {
		allItems = append(allItems, struct {
			memoryType string
			item       memory.ExtractedMemoryItem
		}{memory.TypeEpisodic, item})
	}
	for _, item := range result.TaskMemories {
		allItems = append(allItems, struct {
			memoryType string
			item       memory.ExtractedMemoryItem
		}{memory.TypeTask, item})
	}

	for index, entry := range allItems {
		if entry.item.Content == "" || entry.item.Confidence < 0.5 {
			continue
		}
		importance := entry.item.Importance
		if importance <= 0 {
			importance = 0.5
		}
		if importance > 1 {
			importance = 1
		}

		if _, err := s.candidates.Suggest(ctx, memory.CandidateRequest{OwnerID: job.OwnerID, ConversationID: job.ConversationID,
			SourceID: fmt.Sprintf("legacy-extraction:%d:%s:%d", job.ID, entry.memoryType, index), MemoryType: entry.memoryType,
			Title: entry.item.Title, Content: entry.item.Content, Action: "create", Importance: importance,
			Evidence: []string{fmt.Sprintf("extraction_job:%d", job.ID)}, Source: "legacy_extraction"}); err != nil {
			return err
		}
	}
	return nil
}

func calculateSimilarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	return calculateJaccardSimilarity(a, b)
}

func calculateJaccardSimilarity(a, b string) float64 {
	aWords := strings.Fields(a)
	bWords := strings.Fields(b)
	aSet := make(map[string]bool)
	for _, w := range aWords {
		aSet[w] = true
	}
	intersection := 0
	bSet := make(map[string]bool)
	for _, w := range bWords {
		bSet[w] = true
		if aSet[w] {
			intersection++
		}
	}
	union := len(aSet) + len(bSet) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func FormatExtractionPrompt(messages string, existingMemories string) string {
	prompt := `You are a memory extraction system. Analyze the following conversation and extract key memories.

CONVERSATION:
%s

EXISTING MEMORIES (for reference, avoid duplicates):
%s

Extract important information into these categories and return as JSON:
1. profile_memories: user preferences, traits, skills, background
2. summary_memories: key facts, decisions, summaries  
3. episodic_memories: significant events or experiences
4. task_memories: ongoing tasks, goals, or reminders

For each item provide: title, content, importance (0-1), confidence (0-1).
Only include items with confidence >= 0.6.
Return ONLY valid JSON, no markdown or explanation.`
	return fmt.Sprintf(prompt, messages, existingMemories)
}
