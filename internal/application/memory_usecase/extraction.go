package memory_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/memory"
)

type ExtractionService struct {
	memories    memory.Repository
	extractions memory.ExtractionJobRepository
	merges      memory.MergeLogRepository
}

func NewExtractionService(memories memory.Repository, extractions memory.ExtractionJobRepository, merges memory.MergeLogRepository) *ExtractionService {
	return &ExtractionService{
		memories:    memories,
		extractions: extractions,
		merges:      merges,
	}
}

func (s *ExtractionService) StartExtraction(ctx context.Context, ownerID, conversationID int64, messageIDs []int64) (int64, error) {
	if existingID, ok := s.findOpenJob(ctx, ownerID, conversationID); ok {
		return existingID, nil
	}
	idsJSON, _ := json.Marshal(messageIDs)
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
	resultJSON, _ := json.Marshal(result)
	job.Status = string(memory.ExtractionCompleted)
	job.ResultJSON = resultJSON
	if result == nil {
		return s.extractions.Update(ctx, job)
	}
	if err := s.applyExtractionResults(ctx, ownerID, result); err != nil {
		job.Status = string(memory.ExtractionFailed)
		job.ErrorMessage = err.Error()
		_ = s.extractions.Update(ctx, job)
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

func (s *ExtractionService) applyExtractionResults(ctx context.Context, ownerID int64, result *memory.ExtractionResult) error {
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
	for _, item := range result.SummaryMemories {
		allItems = append(allItems, struct {
			memoryType string
			item       memory.ExtractedMemoryItem
		}{memory.TypeSummary, item})
	}
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

	for _, entry := range allItems {
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

		if err := s.upsertMemory(ctx, ownerID, entry.memoryType, entry.item.Title, entry.item.Content, importance, entry.item.Confidence); err != nil {
			return err
		}
	}
	return nil
}

func (s *ExtractionService) upsertMemory(ctx context.Context, ownerID int64, memoryType, title, content string, importance, confidence float64) error {
	existing, err := s.memories.List(ctx, ownerID, []string{memoryType}, nil, 20, 0)
	if err != nil {
		return s.createNewMemory(ctx, ownerID, memoryType, title, content, importance)
	}
	contentLower := strings.ToLower(strings.TrimSpace(content))
	for _, mem := range existing {
		memLower := strings.ToLower(strings.TrimSpace(mem.Content))
		similarity := calculateSimilarity(contentLower, memLower)
		if similarity > 0.85 {
			return nil
		}
		if similarity > 0.70 && importance > mem.Importance {
			mem.Content = mem.Content + "\n" + content
			if title != "" {
				mem.Title = title
			}
			if err := s.memories.Update(ctx, &mem); err != nil {
				return err
			}
			if err := s.logMerge(ctx, ownerID, 0, mem.ID, similarity, "auto-merge by extraction"); err != nil {
				return err
			}
			return nil
		}
	}
	return s.createNewMemory(ctx, ownerID, memoryType, title, content, importance)
}

func (s *ExtractionService) createNewMemory(ctx context.Context, ownerID int64, memoryType, title, content string, importance float64) error {
	item := &memory.Memory{
		OwnerID:     ownerID,
		MemoryType:  memoryType,
		MemoryLevel: memory.LevelShortTerm,
		Title:       title,
		Content:     content,
		Importance:  importance,
		Source:      "auto_extraction",
	}
	return s.memories.Create(ctx, item)
}

func (s *ExtractionService) logMerge(ctx context.Context, ownerID, sourceID, targetID int64, similarity float64, reason string) error {
	if s.merges == nil {
		return nil
	}
	return s.merges.Create(ctx, &memory.MergeLog{
		OwnerID:    ownerID,
		SourceID:   sourceID,
		TargetID:   targetID,
		Similarity: similarity,
		Reason:     reason,
	})
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
