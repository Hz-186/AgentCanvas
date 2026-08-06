package agent

import (
	"strings"

	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/toolruntime"
)

// ConflictKeyProvider supplies a stable resource key for tools that may not
// run concurrently. An empty key means an otherwise parallelizable call can
// share a parallel segment.
type ConflictKeyProvider interface {
	ConflictKey(call llm.ToolCall, metadata toolruntime.ToolMetadata) string
}

type ToolBatchItem struct {
	Index int
	Call  NormalizedToolCall
}

type ToolBatchSegment struct {
	Parallel bool
	Items    []ToolBatchItem
}

// PlanToolBatch keeps source order while grouping independent reads and
// delegations. Ordinary writes, external actions and calls sharing a conflict
// key are always isolated. Delegation is an explicit execution class: its
// external side effect represents independent child-agent work and is bounded
// later by MaxParallelTools rather than forced into serial execution here.
func PlanToolBatch(calls []NormalizedToolCall, conflicts ConflictKeyProvider) []ToolBatchSegment {
	segments := make([]ToolBatchSegment, 0, len(calls))
	parallel := make([]ToolBatchItem, 0, len(calls))
	flushParallel := func() {
		if len(parallel) == 0 {
			return
		}
		segments = append(segments, ToolBatchSegment{Parallel: len(parallel) > 1, Items: append([]ToolBatchItem(nil), parallel...)})
		parallel = parallel[:0]
	}
	for index, call := range calls {
		metadata := call.Metadata
		key := ""
		if conflicts != nil {
			key = strings.TrimSpace(conflicts.ConflictKey(call.Call, metadata))
		}
		readOnly := metadata.SideEffect == toolruntime.SideEffectNone || metadata.SideEffect == toolruntime.SideEffectRead
		delegation := metadata.ExecutionClass == toolruntime.ExecutionDelegation
		if call.Issue == nil && (readOnly || delegation) && key == "" {
			parallel = append(parallel, ToolBatchItem{Index: index, Call: call})
			continue
		}
		flushParallel()
		segments = append(segments, ToolBatchSegment{Parallel: false, Items: []ToolBatchItem{{Index: index, Call: call}}})
	}
	flushParallel()
	return segments
}
