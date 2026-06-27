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
	if rc.NodeInputs == nil {
		rc.NodeInputs = map[string]NodeInput{}
	}
	if rc.NodeOutputs == nil {
		rc.NodeOutputs = map[string]NodeOutput{}
	}
	if rc.NodeErrors == nil {
		rc.NodeErrors = map[string]string{}
	}
	if rc.NodeLatencies == nil {
		rc.NodeLatencies = map[string]int{}
	}
	if rc.ExecutedNodes == nil {
		rc.ExecutedNodes = map[string]bool{}
	}
	if err := emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowStarted, RunID: rc.RunID}); err != nil {
		return nil, err
	}
	graph, beginID, err := buildExecutionGraph(dsl)
	if err != nil {
		_ = emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowFailed, RunID: rc.RunID, Payload: map[string]any{"error": err.Error()}})
		return nil, err
	}
	var previous NodeOutput
	currentID := beginID
	for steps := 0; currentID != ""; steps++ {
		select {
		case <-ctx.Done():
			err := ctx.Err()
			_ = emit(context.Background(), rc, runtimeevent.Event{Type: runtimeevent.WorkflowFailed, RunID: rc.RunID, Payload: map[string]any{"error": err.Error()}})
			return nil, err
		default:
		}
		if steps >= len(dsl.Nodes) {
			err := fmt.Errorf("%w: flow execution exceeded node count", agenterrors.ErrInvalidInput)
			_ = emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowFailed, RunID: rc.RunID, Payload: map[string]any{"error": err.Error()}})
			return nil, err
		}
		spec, ok := graph.nodes[currentID]
		if !ok {
			err := fmt.Errorf("%w: unknown node %s", agenterrors.ErrInvalidInput, currentID)
			_ = emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowFailed, RunID: rc.RunID, Payload: map[string]any{"error": err.Error()}})
			return nil, err
		}
		if rc.ExecutedNodes[spec.ID] {
			err := fmt.Errorf("%w: node %s executed more than once", agenterrors.ErrInvalidInput, spec.ID)
			_ = emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowFailed, RunID: rc.RunID, Payload: map[string]any{"error": err.Error()}})
			return nil, err
		}
		node, ok := e.nodes[spec.Type]
		if !ok {
			return nil, fmt.Errorf("%w: unsupported node type %s", agenterrors.ErrInvalidInput, spec.Type)
		}
		rc.CurrentNodeID = spec.ID
		rc.CurrentNodeType = spec.Type
		started := time.Now().UTC()
		input := NodeInput(previous)
		rc.NodeInputs[spec.ID] = input
		rc.ExecutedNodes[spec.ID] = true
		if err := emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.NodeStarted, RunID: rc.RunID, NodeID: spec.ID, NodeType: spec.Type}); err != nil {
			return nil, err
		}
		output, err := node.Run(ctx, rc, input, spec.Config)
		latencyMS := int(time.Since(started).Milliseconds())
		rc.NodeLatencies[spec.ID] = latencyMS
		if err != nil {
			rc.NodeErrors[spec.ID] = err.Error()
			_ = emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.NodeFailed, RunID: rc.RunID, NodeID: spec.ID, NodeType: spec.Type, Payload: map[string]any{"error": err.Error(), "latency_ms": latencyMS}})
			_ = emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowFailed, RunID: rc.RunID, Payload: map[string]any{"error": err.Error()}})
			return nil, err
		}
		rc.NodeOutputs[spec.ID] = output
		previous = output
		if err := emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.NodeFinished, RunID: rc.RunID, NodeID: spec.ID, NodeType: spec.Type, Payload: map[string]any{"latency_ms": latencyMS}}); err != nil {
			return nil, err
		}
		nextID, err := e.nextNode(ctx, rc, node, spec, graph, output)
		if err != nil {
			_ = emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowFailed, RunID: rc.RunID, Payload: map[string]any{"error": err.Error()}})
			return nil, err
		}
		currentID = nextID
	}
	if err := emit(ctx, rc, runtimeevent.Event{Type: runtimeevent.WorkflowFinished, RunID: rc.RunID}); err != nil {
		return nil, err
	}
	return previous, nil
}

func (e *Executor) nextNode(ctx context.Context, rc *RunContext, node Node, spec flow.Node, graph executionGraph, output NodeOutput) (string, error) {
	nexts := graph.adjacency[spec.ID]
	if router, ok := node.(RouterNode); ok {
		nextID, err := router.NextNodeID(ctx, rc, output, spec.Config)
		if err != nil {
			return "", err
		}
		if nextID == "" {
			return "", nil
		}
		for _, allowed := range nexts {
			if allowed == nextID {
				return nextID, nil
			}
		}
		return "", fmt.Errorf("%w: node %s selected non-adjacent target %s", agenterrors.ErrInvalidInput, spec.ID, nextID)
	}
	if len(nexts) == 0 {
		return "", nil
	}
	if len(nexts) > 1 {
		return "", fmt.Errorf("%w: node %s has multiple outgoing edges but is not a router", agenterrors.ErrInvalidInput, spec.ID)
	}
	return nexts[0], nil
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

type executionGraph struct {
	nodes     map[string]flow.Node
	adjacency map[string][]string
}

func buildExecutionGraph(dsl *flow.DSL) (executionGraph, string, error) {
	nodeByID := make(map[string]flow.Node, len(dsl.Nodes))
	adjacency := make(map[string][]string, len(dsl.Nodes))
	beginID := ""
	for _, node := range dsl.Nodes {
		nodeByID[node.ID] = node
		if node.Type == "begin" {
			beginID = node.ID
		}
	}
	for _, edge := range dsl.Edges {
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
	}
	if beginID == "" {
		return executionGraph{}, "", fmt.Errorf("%w: begin node is required", agenterrors.ErrInvalidInput)
	}
	return executionGraph{nodes: nodeByID, adjacency: adjacency}, beginID, nil
}
