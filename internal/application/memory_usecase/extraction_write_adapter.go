package memory_usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"agentcanvas/internal/domain/memory"
)

// Extraction write wiring (design Decision 9): gated candidates become
// ordinary memory write jobs with source extraction and the per-candidate
// idempotency key extraction:<job-id>:<index>. The memory row's
// DeduplicationKey is derived per source by SQLMemoryWriter; cross-job
// content deduplication happens there, never on this envelope.

// extractionWriteSource is the canonical write-job source for rows produced
// by durable extraction.
const extractionWriteSource = "extraction"

// extractionWriteKey is the per-candidate idempotency key: index is the
// accepted candidate's position within the job's gated stream, so retries
// re-enqueue the exact same envelope.
func extractionWriteKey(jobID int64, index int) string {
	return fmt.Sprintf("extraction:%d:%d", jobID, index)
}

// extractionWriteRequest turns one gated candidate into the unified write-job
// envelope. Content and title are redacted again as defense in depth: the
// model only ever saw redacted evidence, but a stored memory row must never
// carry a raw secret. The candidate's provenance (model type, confidence and
// evidence refs) rides in metadata_json.
func extractionWriteRequest(job *memory.ExtractionJob, index int, candidate ExtractionCandidate) (WriteJobRequest, error) {
	metadata, err := json.Marshal(map[string]any{
		"candidate_type": candidate.Type,
		"confidence":     candidate.Confidence,
		"evidence_refs":  candidate.EvidenceRefs,
	})
	if err != nil {
		return WriteJobRequest{}, fmt.Errorf("encode extraction candidate metadata: %w", err)
	}
	conversationID := job.ConversationID
	return WriteJobRequest{
		OwnerID:        job.OwnerID,
		Source:         extractionWriteSource,
		IdempotencyKey: extractionWriteKey(job.ID, index),
		Payload: memory.WriteRequest{
			OwnerID:              job.OwnerID,
			ConversationID:       conversationID,
			SourceConversationID: &conversationID,
			Source:               extractionWriteSource,
			MemoryType:           memory.TypeArchival,
			Title:                redactDurableSecrets(candidate.Title),
			Content:              redactDurableSecrets(candidate.Content),
			Importance:           candidate.Importance,
			MetadataJSON:         metadata,
		},
	}, nil
}

// enqueueExtractionWrites submits every accepted candidate as one idempotent
// memory write job. A failure is returned as-is so Phase 1 stays retryable;
// the unique (owner_id, idempotency_key) makes the retry's re-enqueue
// exactly-once.
func (w *DurableMemoryWorker) enqueueExtractionWrites(ctx context.Context, job *memory.ExtractionJob, candidates []ExtractionCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	if w.writes == nil {
		return errors.New("durable memory extraction write pipeline is not configured")
	}
	for index, candidate := range candidates {
		request, err := extractionWriteRequest(job, index, candidate)
		if err != nil {
			return err
		}
		if err := w.writes.Enqueue(ctx, request); err != nil {
			return err
		}
	}
	return nil
}
