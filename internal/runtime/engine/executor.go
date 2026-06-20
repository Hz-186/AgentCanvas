package engine

import (
	"context"
	"fmt"
	"time"

	"agentcanvas/internal/domain/flow"
	runtimeevent "agentcanvas/internal/runtime/event"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type Executor struct {
	nodes map[string]Node
}

func NewExecutor(nodes []Node) *Executor {
	registered := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		registered[node.Type()] = node
	}
	return &Executor{nodes: registered}
}

func (e *Executor) ValidateNodeConfig(nodeType string, config []byte) error {
	node, ok := e.nodes[nodeType]
	if !ok {
		return fmt.Errorf("%w: unsupported node type %s", agenterrors.ErrInvalidInput, nodeType)
	}
	return node.Validate(config)
}

func (e *Executor) Execute(ctx context.Context, rc *RunContext, dsl *flow.DSL) (NodeOutput, error) {
	if rc.Variables == nil {
		rc.Variables = map[string]any{}
	}
	if rc.NodeOutputs == nil {
		rc.NodeOutputs = map[string]NodeOutput{}
	}
	if err := emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowStarted, RunID: rc.RunID}); err != nil {
		return nil, err
	}
	order, err := executionOrder(dsl)
	if err != nil {
		_ = emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowFailed, RunID: rc.RunID, Payload: map[string]any{"error": err.Error()}})
		return nil, err
	}
	var previous NodeOutput
	for _, spec := range order {
		node, ok := e.nodes[spec.Type]
		if !ok {
			return nil, fmt.Errorf("%w: unsupported node type %s", agenterrors.ErrInvalidInput, spec.Type)
		}
		rc.CurrentNodeID = spec.ID
		rc.CurrentNodeType = spec.Type
		started := time.Now().UTC()
		if err := emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.NodeStarted, RunID: rc.RunID, NodeID: spec.ID, NodeType: spec.Type}); err != nil {
			return nil, err
		}
		output, err := node.Run(ctx, rc, NodeInput(previous), spec.Config)
		if err != nil {
			_ = emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.NodeFailed, RunID: rc.RunID, NodeID: spec.ID, NodeType: spec.Type, Payload: map[string]any{"error": err.Error(), "latency_ms": int(time.Since(started).Milliseconds())}})
			_ = emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowFailed, RunID: rc.RunID, Payload: map[string]any{"error": err.Error()}})
			return nil, err
		}
		rc.NodeOutputs[spec.ID] = output
		previous = output
		if err := emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.NodeFinished, RunID: rc.RunID, NodeID: spec.ID, NodeType: spec.Type, Payload: map[string]any{"latency_ms": int(time.Since(started).Milliseconds())}}); err != nil {
			return nil, err
		}
	}
	if err := emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowFinished, RunID: rc.RunID}); err != nil {
		return nil, err
	}
	return previous, nil
}

func emit(ctx context.Context, rc *RunContext, event runtimeevent.Event) error {
	if rc == nil || rc.Events == nil {
		return nil
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	return rc.Events.Emit(ctx, event)
}

func executionOrder(dsl *flow.DSL) ([]flow.Node, error) {
	nodeByID := make(map[string]flow.Node, len(dsl.Nodes))
	indegree := make(map[string]int, len(dsl.Nodes))
	adjacency := make(map[string][]string, len(dsl.Nodes))
	for _, node := range dsl.Nodes {
		nodeByID[node.ID] = node
		indegree[node.ID] = 0
	}
	for _, edge := range dsl.Edges {
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
		indegree[edge.To]++
	}
	queue := make([]string, 0, len(indegree))
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	ordered := make([]flow.Node, 0, len(dsl.Nodes))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		ordered = append(ordered, nodeByID[id])
		for _, next := range adjacency[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(ordered) != len(dsl.Nodes) {
		return nil, fmt.Errorf("%w: flow contains cycle", agenterrors.ErrInvalidInput)
	}
	return ordered, nil
}
