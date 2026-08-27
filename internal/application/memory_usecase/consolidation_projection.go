package memory_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/memory"

	"gorm.io/gorm"
)

// Consolidation source kinds recorded in artifact source references.
const (
	ConsolidationSourceRollout = "rollout"
	ConsolidationSourceAdHoc   = "ad_hoc"
)

// consolidationColdWindow is the default cold-cutoff for consolidation input
// selection: inputs older than this window that are not protected by the
// current handbook/summary are excluded.
const consolidationColdWindow = 30 * 24 * time.Hour

// ProjectionSourceRef identifies one evidence source represented by a
// consolidation artifact version. It is stored verbatim in SourceRefsJSON.
type ProjectionSourceRef struct {
	SourceID       int64  `json:"source_id"`
	Kind           string `json:"kind"`
	ConversationID int64  `json:"conversation_id"`
}

// ProjectionInput is one consolidation evidence unit. The source ref is its
// stable identity across artifact versions; SourceAt drives cold selection.
type ProjectionInput struct {
	SourceRef      ProjectionSourceRef
	RawMemory      string
	RolloutSummary string
	SourceAt       time.Time
}

// ConsolidationDiff is the version diff the consolidation agent must read
// first: sources added since the previous artifact version and sources that
// are no longer selected.
type ConsolidationDiff struct {
	Added   []ProjectionSourceRef
	Removed []ProjectionSourceRef
}

// ConsolidationAgent produces the new handbook/summary pair from the diff,
// the prior artifact contents and the rendered raw input. It is called at
// most once per projection run.
type ConsolidationAgent interface {
	Consolidate(ctx context.Context, ownerID int64, diff ConsolidationDiff, priorHandbook, priorSummary, raw string) (string, string, error)
}

// ConsolidationProjection persists Phase-2 consolidation output as versioned
// SQL memory_artifacts rows. It owns input selection (protected sources are
// retained, cold unprotected inputs are excluded), version diff computation,
// the single consolidation call and both artifact writes. No filesystem is
// involved: an artifact write failure is returned as-is so the calling
// pipeline keeps the job retryable.
type ConsolidationProjection struct {
	artifacts memory.MemoryArtifactRepository
	Now       func() time.Time
}

func NewConsolidationProjection(artifacts memory.MemoryArtifactRepository) *ConsolidationProjection {
	return &ConsolidationProjection{
		artifacts: artifacts,
		Now:       time.Now,
	}
}

func (p *ConsolidationProjection) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// Project runs one consolidation pass. Empty batches short-circuit with no
// agent call and no artifact writes. Otherwise it loads the previous
// handbook/summary, selects inputs, computes the version diff, calls the agent
// exactly once and writes two versioned artifact rows.
func (p *ConsolidationProjection) Project(ctx context.Context, ownerID int64, inputs []ProjectionInput, agent ConsolidationAgent) (*memory.MemoryArtifact, *memory.MemoryArtifact, error) {
	if p == nil || p.artifacts == nil {
		return nil, nil, fmt.Errorf("consolidation projection is not configured")
	}
	if ownerID <= 0 {
		return nil, nil, fmt.Errorf("consolidation projection owner is required")
	}
	if agent == nil {
		return nil, nil, fmt.Errorf("consolidation agent is required")
	}

	priorHandbook, err := p.priorVersion(ctx, ownerID, memory.ArtifactKindHandbook)
	if err != nil {
		return nil, nil, err
	}
	priorSummary, err := p.priorVersion(ctx, ownerID, memory.ArtifactKindSummary)
	if err != nil {
		return nil, nil, err
	}
	previousRefs := make(map[string]ProjectionSourceRef)
	for _, artifact := range []*memory.MemoryArtifact{priorHandbook, priorSummary} {
		refs := decodeRefs(artifact)
		for _, ref := range refs {
			previousRefs[ref.key()] = ref
		}
	}

	selected := selectProjectionInputs(inputs, previousRefs, p.now())
	newRefs := make([]ProjectionSourceRef, 0, len(selected)+len(previousRefs))
	seen := make(map[string]struct{}, len(selected)+len(previousRefs))
	addRef := func(ref ProjectionSourceRef) {
		if _, ok := seen[ref.key()]; ok {
			return
		}
		seen[ref.key()] = struct{}{}
		newRefs = append(newRefs, ref)
	}
	for key, ref := range previousRefs {
		if _, ok := selected[key]; ok {
			addRef(ref)
		}
	}
	for _, input := range selected {
		addRef(input.SourceRef)
	}
	sortProjectionRefs(newRefs)

	diff := projectionDiff(previousRefs, newRefs)
	if len(diff.Added) == 0 && len(diff.Removed) == 0 {
		// Empty batch (no prior version, no inputs) and unchanged batches both
		// short-circuit: no LLM call, no artifact mutation.
		return nil, nil, nil
	}

	raw := renderProjectionRawInputs(selected)
	handbookText, summaryText, err := agent.Consolidate(ctx, ownerID, diff, priorContent(priorHandbook), priorContent(priorSummary), raw)
	if err != nil {
		return nil, nil, err
	}
	handbookText = strings.TrimSpace(handbookText)
	summaryText = strings.TrimSpace(summaryText)
	if handbookText == "" {
		handbookText = raw
	}
	if summaryText == "" {
		summaryText = summarizeDurableText(handbookText)
	}

	now := p.now().UTC()
	refsJSON := marshalProjectionRefs(newRefs)
	handbook := &memory.MemoryArtifact{
		BaseModel:      domain.BaseModel{OwnerID: ownerID},
		Kind:           memory.ArtifactKindHandbook,
		Version:        priorVersion(priorHandbook) + 1,
		Content:        handbookText,
		Source:         "consolidation",
		SourceRefsJSON: refsJSON,
		Checksum:       projectionChecksum(handbookText),
		ConsolidatedAt: &now,
	}
	summary := &memory.MemoryArtifact{
		BaseModel:      domain.BaseModel{OwnerID: ownerID},
		Kind:           memory.ArtifactKindSummary,
		Version:        priorVersion(priorSummary) + 1,
		Content:        summaryText,
		Source:         "consolidation",
		SourceRefsJSON: refsJSON,
		Checksum:       projectionChecksum(summaryText),
		ConsolidatedAt: &now,
	}
	if err := p.artifacts.Create(ctx, handbook); err != nil {
		return nil, nil, fmt.Errorf("persist handbook artifact: %w", err)
	}
	if err := p.artifacts.Create(ctx, summary); err != nil {
		return nil, nil, fmt.Errorf("persist summary artifact: %w", err)
	}
	return handbook, summary, nil
}

// RemovePrunedSources performs source-aware cleanup after lifecycle pruning:
// the current handbook/summary versions are rewritten with the source refs of
// the deleted memory IDs removed surgically (reference removal, never string
// deletion). The consolidation agent is called at most once with a
// removal-only diff; when no current ref matches a deleted ID this is a no-op
// with no agent call and no writes.
func (p *ConsolidationProjection) RemovePrunedSources(ctx context.Context, ownerID int64, deletedIDs []int64, agent ConsolidationAgent) (int, error) {
	if p == nil || p.artifacts == nil {
		return 0, fmt.Errorf("consolidation projection is not configured")
	}
	if ownerID <= 0 {
		return 0, fmt.Errorf("consolidation projection owner is required")
	}
	if len(deletedIDs) == 0 {
		return 0, nil
	}
	priorHandbook, err := p.priorVersion(ctx, ownerID, memory.ArtifactKindHandbook)
	if err != nil {
		return 0, err
	}
	priorSummary, err := p.priorVersion(ctx, ownerID, memory.ArtifactKindSummary)
	if err != nil {
		return 0, err
	}
	deleted := make(map[int64]struct{}, len(deletedIDs))
	for _, id := range deletedIDs {
		if id > 0 {
			deleted[id] = struct{}{}
		}
	}
	removedRefs, survivingRefs := removeProjectionRefs(priorHandbook, priorSummary, deleted)
	if len(removedRefs) == 0 {
		return 0, nil
	}
	if agent == nil {
		return 0, fmt.Errorf("consolidation agent is required for pruned-source cleanup")
	}
	diff := ConsolidationDiff{Removed: removedRefs}
	priorHandbookText, priorSummaryText := priorContent(priorHandbook), priorContent(priorSummary)
	handbookText, summaryText, err := agent.Consolidate(ctx, ownerID, diff, priorHandbookText, priorSummaryText, "")
	if err != nil {
		return 0, err
	}
	handbookText = strings.TrimSpace(handbookText)
	summaryText = strings.TrimSpace(summaryText)
	// Removal-only runs must never empty the artifacts: empty agent output
	// keeps the prior content while the refs are still dropped surgically.
	if handbookText == "" {
		handbookText = priorHandbookText
	}
	if summaryText == "" {
		summaryText = priorSummaryText
	}
	now := p.now().UTC()
	refsJSON := marshalProjectionRefs(survivingRefs)
	handbook := &memory.MemoryArtifact{
		BaseModel:      domain.BaseModel{OwnerID: ownerID},
		Kind:           memory.ArtifactKindHandbook,
		Version:        priorVersion(priorHandbook) + 1,
		Content:        handbookText,
		Source:         "consolidation",
		SourceRefsJSON: refsJSON,
		Checksum:       projectionChecksum(handbookText),
		ConsolidatedAt: &now,
	}
	summary := &memory.MemoryArtifact{
		BaseModel:      domain.BaseModel{OwnerID: ownerID},
		Kind:           memory.ArtifactKindSummary,
		Version:        priorVersion(priorSummary) + 1,
		Content:        summaryText,
		Source:         "consolidation",
		SourceRefsJSON: refsJSON,
		Checksum:       projectionChecksum(summaryText),
		ConsolidatedAt: &now,
	}
	if err := p.artifacts.Create(ctx, handbook); err != nil {
		return 0, fmt.Errorf("persist handbook artifact: %w", err)
	}
	if err := p.artifacts.Create(ctx, summary); err != nil {
		return 0, fmt.Errorf("persist summary artifact: %w", err)
	}
	return len(removedRefs), nil
}

// removeProjectionRefs splits the union of the current handbook/summary
// source refs into the refs matching a deleted memory ID and the survivors.
func removeProjectionRefs(handbook, summary *memory.MemoryArtifact, deleted map[int64]struct{}) (removed, surviving []ProjectionSourceRef) {
	removed = make([]ProjectionSourceRef, 0)
	surviving = make([]ProjectionSourceRef, 0)
	seen := make(map[string]struct{})
	for _, artifact := range []*memory.MemoryArtifact{handbook, summary} {
		for _, ref := range decodeRefs(artifact) {
			if _, ok := seen[ref.key()]; ok {
				continue
			}
			seen[ref.key()] = struct{}{}
			if _, ok := deleted[ref.SourceID]; ok {
				removed = append(removed, ref)
			} else {
				surviving = append(surviving, ref)
			}
		}
	}
	sortProjectionRefs(removed)
	sortProjectionRefs(surviving)
	return removed, surviving
}

func (p *ConsolidationProjection) priorVersion(ctx context.Context, ownerID int64, kind string) (*memory.MemoryArtifact, error) {
	artifact, err := p.artifacts.Latest(ctx, ownerID, kind)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load prior %s artifact: %w", kind, err)
	}
	return artifact, nil
}

func priorVersion(artifact *memory.MemoryArtifact) int {
	if artifact == nil {
		return 0
	}
	return artifact.Version
}

func priorContent(artifact *memory.MemoryArtifact) string {
	if artifact == nil {
		return ""
	}
	return artifact.Content
}

func (r ProjectionSourceRef) key() string {
	return fmt.Sprintf("%s:%d", r.Kind, r.SourceID)
}

func decodeRefs(artifact *memory.MemoryArtifact) []ProjectionSourceRef {
	if artifact == nil || len(artifact.SourceRefsJSON) == 0 {
		return nil
	}
	var refs []ProjectionSourceRef
	if err := json.Unmarshal(artifact.SourceRefsJSON, &refs); err != nil {
		return nil
	}
	return refs
}

func marshalProjectionRefs(refs []ProjectionSourceRef) json.RawMessage {
	data, err := json.Marshal(refs)
	if err != nil {
		return nil
	}
	return data
}

func sortProjectionRefs(refs []ProjectionSourceRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		return refs[i].SourceID < refs[j].SourceID
	})
}

// selectProjectionInputs keeps warm inputs and protected inputs, and excludes
// cold unprotected inputs. Protected sources stay selected even when their
// content window has aged out, so the source reference survives in the next
// artifact version.
func selectProjectionInputs(inputs []ProjectionInput, previous map[string]ProjectionSourceRef, now time.Time) map[string]ProjectionInput {
	cutoff := now.Add(-consolidationColdWindow)
	selected := make(map[string]ProjectionInput, len(inputs))
	for _, input := range inputs {
		_, protected := previous[input.SourceRef.key()]
		if !protected && !input.SourceAt.IsZero() && input.SourceAt.Before(cutoff) {
			continue
		}
		selected[input.SourceRef.key()] = input
	}
	return selected
}

func projectionDiff(previous map[string]ProjectionSourceRef, current []ProjectionSourceRef) ConsolidationDiff {
	currentSet := make(map[string]struct{}, len(current))
	for _, ref := range current {
		currentSet[ref.key()] = struct{}{}
	}
	var added, removed []ProjectionSourceRef
	for _, ref := range current {
		if _, ok := previous[ref.key()]; !ok {
			added = append(added, ref)
		}
	}
	for key, ref := range previous {
		if _, ok := currentSet[key]; !ok {
			removed = append(removed, ref)
		}
	}
	sortProjectionRefs(added)
	sortProjectionRefs(removed)
	return ConsolidationDiff{Added: added, Removed: removed}
}

func renderProjectionRawInputs(inputs map[string]ProjectionInput) string {
	if len(inputs) == 0 {
		return ""
	}
	refs := make([]ProjectionSourceRef, 0, len(inputs))
	for _, input := range inputs {
		refs = append(refs, input.SourceRef)
	}
	sortProjectionRefs(refs)
	var builder strings.Builder
	builder.WriteString("# Raw Memories\n\n")
	for _, ref := range refs {
		input := inputs[ref.key()]
		switch ref.Kind {
		case ConsolidationSourceRollout:
			builder.WriteString(fmt.Sprintf("## Rollout %d\n\n", ref.SourceID))
			if input.RawMemory != "" {
				builder.WriteString(input.RawMemory)
				builder.WriteString("\n\n")
			}
			if input.RolloutSummary != "" {
				builder.WriteString("Summary: ")
				builder.WriteString(input.RolloutSummary)
				builder.WriteString("\n\n")
			}
		default:
			builder.WriteString(fmt.Sprintf("## Ad-hoc note %d\n\n", ref.SourceID))
			builder.WriteString("[ad-hoc note]\n")
			if input.RawMemory != "" {
				builder.WriteString(input.RawMemory)
				builder.WriteString("\n")
			}
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func projectionChecksum(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// RenderDiff formats the version diff for the consolidation agent prompt. The
// diff is always presented before the existing artifacts and raw input.
func (d ConsolidationDiff) RenderDiff() string {
	var builder strings.Builder
	builder.WriteString("## Sources added since the previous artifact version\n")
	if len(d.Added) == 0 {
		builder.WriteString("- none\n")
	}
	for _, ref := range d.Added {
		builder.WriteString(fmt.Sprintf("- %s %d (conversation %d)\n", ref.Kind, ref.SourceID, ref.ConversationID))
	}
	builder.WriteString("## Sources removed since the previous artifact version\n")
	if len(d.Removed) == 0 {
		builder.WriteString("- none\n")
	}
	for _, ref := range d.Removed {
		builder.WriteString(fmt.Sprintf("- %s %d (conversation %d)\n", ref.Kind, ref.SourceID, ref.ConversationID))
	}
	return builder.String()
}
