package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/pkg/tokencounter"
)

// fakeMemoryArtifactRepository is an in-memory MemoryArtifactRepository fake
// that records Latest calls and can inject a read failure.
type fakeMemoryArtifactRepository struct {
	artifact    *memory.MemoryArtifact
	err         error
	latestCalls int
	latestOwner int64
	latestKind  string
}

func (r *fakeMemoryArtifactRepository) Create(context.Context, *memory.MemoryArtifact) error {
	return nil
}

func (r *fakeMemoryArtifactRepository) Latest(_ context.Context, ownerID int64, kind string) (*memory.MemoryArtifact, error) {
	r.latestCalls++
	r.latestOwner = ownerID
	r.latestKind = kind
	if r.err != nil {
		return nil, r.err
	}
	return r.artifact, nil
}

// summaryArtifact builds a version-3 summary artifact carrying two stable
// memory IDs in its source references.
func summaryArtifact(content string) *memory.MemoryArtifact {
	return &memory.MemoryArtifact{
		BaseModel:      domain.BaseModel{OwnerID: 1},
		Kind:           memory.ArtifactKindSummary,
		Version:        3,
		Content:        content,
		SourceRefsJSON: json.RawMessage(`[{"source_id":102,"kind":"rollout","conversation_id":5},{"source_id":101,"kind":"ad_hoc","conversation_id":6}]`),
	}
}

// AutomaticMemoryBlockTest#shouldInjectBoundedAdvisorySummary
func TestAutomaticMemoryBlockShouldInjectBoundedAdvisorySummary(t *testing.T) {
	long := strings.Repeat("durable summary detail ", 300)
	if tokens := tokencounter.Count("", "", long).Tokens; tokens <= 1200 {
		t.Fatalf("precondition: mock summary must exceed the 1200-token bound, got %d tokens", tokens)
	}
	artifacts := &fakeMemoryArtifactRepository{artifact: summaryArtifact(long)}
	n := runtimeCore{coreRepositories: coreRepositories{MemoryArtifacts: artifacts}}
	cfg := agentRuntimeConfig{RuntimeMemoryPolicy: RuntimeMemoryPolicy{MemoryEnabled: true}}

	block := n.buildAutomaticMemoryBlock(context.Background(), &RunContext{OwnerID: 1}, cfg)

	if block == nil {
		t.Fatal("expected an automatic summary block")
	}
	if block.Name != automaticSummaryBlockName {
		t.Fatalf("stable block id = %q, want %q", block.Name, automaticSummaryBlockName)
	}
	if block.Role != conversation.RoleSystem {
		t.Fatalf("block role = %q, want system", block.Role)
	}
	if tokens := tokencounter.Count("", "", block.Content).Tokens; tokens > 1200 {
		t.Fatalf("summary block exceeds the 1200-token bound: %d tokens", tokens)
	}
	if !strings.Contains(block.Content, automaticSummaryAdvisory) || !strings.Contains(block.Content, "read_memory") {
		t.Fatalf("summary block lost advisory/read_memory guidance: %q", block.Content)
	}
	if !strings.Contains(block.Content, "Memory IDs: 101, 102") {
		t.Fatalf("summary block lost stable memory IDs: %q", block.Content)
	}
	if !strings.Contains(block.Content, automaticSummaryFreshnessNote) {
		t.Fatalf("summary block lost freshness hint: %q", block.Content)
	}
	if artifacts.latestCalls != 1 || artifacts.latestOwner != 1 || artifacts.latestKind != memory.ArtifactKindSummary {
		t.Fatalf("summary repository call = %d (owner %d kind %q), want exactly one owner-1 summary read", artifacts.latestCalls, artifacts.latestOwner, artifacts.latestKind)
	}
}

// AutomaticMemoryBlockTest#shouldSkipDelegatedRun
func TestAutomaticMemoryBlockShouldSkipDelegatedRun(t *testing.T) {
	artifacts := &fakeMemoryArtifactRepository{artifact: summaryArtifact("User prefers concise answers.")}
	n := runtimeCore{coreRepositories: coreRepositories{MemoryArtifacts: artifacts}}
	cfg := agentRuntimeConfig{RuntimeMemoryPolicy: RuntimeMemoryPolicy{MemoryEnabled: true}}

	parentID := int64(9)
	if block := n.buildAutomaticMemoryBlock(context.Background(), &RunContext{OwnerID: 1, ParentRunID: &parentID}, cfg); block != nil {
		t.Fatal("expected no summary block for a run with a parent")
	}
	if block := n.buildAutomaticMemoryBlock(context.Background(), &RunContext{OwnerID: 1, DelegationDepth: 1}, cfg); block != nil {
		t.Fatal("expected no summary block for a delegated run")
	}
	if artifacts.latestCalls != 0 {
		t.Fatalf("summary repository calls = %d, want zero for delegated runs", artifacts.latestCalls)
	}
}

// AutomaticMemoryBlockTest#shouldReturnNilOnSummaryFailure
func TestAutomaticMemoryBlockShouldReturnNilOnSummaryFailure(t *testing.T) {
	artifacts := &fakeMemoryArtifactRepository{err: errors.New("summary artifact read failed")}
	n := runtimeCore{coreRepositories: coreRepositories{MemoryArtifacts: artifacts}}
	cfg := agentRuntimeConfig{RuntimeMemoryPolicy: RuntimeMemoryPolicy{MemoryEnabled: true}}

	if block := n.buildAutomaticMemoryBlock(context.Background(), &RunContext{OwnerID: 1}, cfg); block != nil {
		t.Fatalf("expected no context block on summary read failure, got %+v", block)
	}
	if artifacts.latestCalls != 1 {
		t.Fatalf("summary repository calls = %d, want exactly one read attempt", artifacts.latestCalls)
	}
	// A core without the artifact repository (and without any legacy file
	// store) must degrade to no block instead of falling back to a file.
	empty := runtimeCore{}
	if block := empty.buildAutomaticMemoryBlock(context.Background(), &RunContext{OwnerID: 1}, cfg); block != nil {
		t.Fatalf("expected no context block when the summary repository is absent, got %+v", block)
	}
}

// AutomaticMemoryBlockTest#shouldPreserveAdvisoryText
func TestAutomaticMemoryBlockShouldPreserveAdvisoryText(t *testing.T) {
	artifacts := &fakeMemoryArtifactRepository{artifact: summaryArtifact("User prefers concise answers.")}
	n := runtimeCore{coreRepositories: coreRepositories{MemoryArtifacts: artifacts}}
	cfg := agentRuntimeConfig{RuntimeMemoryPolicy: RuntimeMemoryPolicy{MemoryEnabled: true}}

	block := n.buildAutomaticMemoryBlock(context.Background(), &RunContext{OwnerID: 1}, cfg)
	if block == nil {
		t.Fatal("expected an automatic summary block")
	}
	if count := strings.Count(block.Content, automaticSummaryAdvisory); count != 1 {
		t.Fatalf("advisory wording appears %d times, want exactly once", count)
	}
	if count := strings.Count(block.Content, automaticSummaryFreshnessNote); count != 1 {
		t.Fatalf("freshness hint appears %d times, want exactly once", count)
	}
	if !strings.Contains(block.Content, "Memory IDs: 101, 102") {
		t.Fatalf("stable memory IDs missing: %q", block.Content)
	}
	if !strings.Contains(block.Content, "version 3") {
		t.Fatalf("stale version missing from the freshness hint: %q", block.Content)
	}
}
