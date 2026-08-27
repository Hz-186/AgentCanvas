package memory

import (
	"context"
	"encoding/json"
)

// TerminalReflectionRequest is the terminal reflection extraction envelope.
// Inline reflection evidence becomes a normal memory write job (source
// reflection) instead of a reflection row.
type TerminalReflectionRequest struct {
	OwnerID int64
	AgentID int64
	RunID   int64
	Task    string
	// Content is the derived lesson text assembled from the inline reflection
	// trace. A run without inline evidence produces no write job.
	Content string
	// EvidenceJSON carries the run outcome and the full inline reflection
	// trace that justify the derived memory.
	EvidenceJSON json.RawMessage
}

// TerminalReflectionWriter enqueues terminal-reflection extraction into the
// unified memory write pipeline. The retired reflection analyzer/queue is
// replaced by this producer; inline reflection stays a runtime-only signal
// unless it becomes evidence for a write job.
type TerminalReflectionWriter interface {
	EnqueueTerminalReflection(ctx context.Context, req TerminalReflectionRequest) error
}

// ProposalMemoryWriter routes approved reflection proposals into the unified
// memory write pipeline (source proposal). It replaced the retired reflection
// repository: proposals are ordinary memory write jobs, not reflection rows.
type ProposalMemoryWriter interface {
	EnqueueProposalWriteJob(ctx context.Context, req ProposalWriteJobRequest) error
}

// ProposalWriteJobRequest is the proposal memory write envelope. The
// implementing adapter derives the idempotency key from ProposalID.
type ProposalWriteJobRequest struct {
	OwnerID    int64
	AgentID    int64
	RunID      int64
	ProposalID int64
	Title      string
	Content    string
	Evidence   string
	Confidence float64
	Checksum   string
}
