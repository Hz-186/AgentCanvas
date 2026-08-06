package agent

import (
	"context"
	"fmt"
	"sync"

	"agentcanvas/internal/runtime/toolruntime"
)

type ToolBatchExecution struct {
	Index  int
	CallID string
	Result *toolruntime.ToolResult
	Err    error
}

// ExecuteToolBatch executes a planned batch and always returns one entry for
// each planned item. Results retain input order even when a read segment runs
// concurrently. The supplied execute function is the sole side-effect point.
func ExecuteToolBatch(ctx context.Context, segments []ToolBatchSegment, maxParallel int, execute func(context.Context, ToolBatchItem) (*toolruntime.ToolResult, error)) []ToolBatchExecution {
	if maxParallel <= 0 {
		maxParallel = 1
	}
	results := make([]ToolBatchExecution, 0)
	for _, segment := range segments {
		if !segment.Parallel || len(segment.Items) <= 1 {
			for _, item := range segment.Items {
				results = append(results, executeOne(ctx, item, execute))
			}
			continue
		}
		segmentResults := make([]ToolBatchExecution, len(segment.Items))
		sem := make(chan struct{}, maxParallel)
		var wg sync.WaitGroup
		for index, item := range segment.Items {
			wg.Add(1)
			go func(index int, item ToolBatchItem) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
					segmentResults[index] = executeOne(ctx, item, execute)
				case <-ctx.Done():
					segmentResults[index] = ToolBatchExecution{Index: item.Index, CallID: item.Call.Call.ID, Err: ctx.Err(), Result: &toolruntime.ToolResult{ContentText: ctx.Err().Error(), IsError: true, Metadata: map[string]any{"error_code": "cancelled"}}}
				}
			}(index, item)
		}
		wg.Wait()
		results = append(results, segmentResults...)
	}
	return results
}

func executeOne(ctx context.Context, item ToolBatchItem, execute func(context.Context, ToolBatchItem) (*toolruntime.ToolResult, error)) (result ToolBatchExecution) {
	result = ToolBatchExecution{Index: item.Index, CallID: item.Call.Call.ID}
	if item.Call.Issue != nil {
		result.Err = fmt.Errorf("%s: %s", item.Call.Issue.Code, item.Call.Issue.Message)
		result.Result = &toolruntime.ToolResult{ContentText: item.Call.Issue.Message, IsError: true, Metadata: map[string]any{"error_code": string(item.Call.Issue.Code)}}
		return result
	}
	result.Result, result.Err = execute(ctx, item)
	if result.Result == nil {
		result.Result = &toolruntime.ToolResult{}
	}
	if result.Err != nil {
		result.Result.IsError = true
	}
	return result
}
