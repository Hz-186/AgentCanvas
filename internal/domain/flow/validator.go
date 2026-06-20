package flow

import (
	"encoding/json"
	"fmt"
	"strings"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type NodeTypeValidator interface {
	ValidateNodeConfig(nodeType string, config []byte) error
}

type Validator struct {
	nodes NodeTypeValidator
}

func NewValidator(nodes NodeTypeValidator) *Validator {
	return &Validator{nodes: nodes}
}

func (v *Validator) Validate(dsl *DSL) error {
	if dsl == nil || strings.TrimSpace(dsl.SchemaVersion) != SchemaVersionV1 || strings.TrimSpace(dsl.FlowID) == "" {
		return agenterrors.ErrInvalidInput
	}
	if len(dsl.Nodes) == 0 {
		return fmt.Errorf("%w: flow nodes are required", agenterrors.ErrInvalidInput)
	}
	nodeByID := make(map[string]Node, len(dsl.Nodes))
	beginCount := 0
	beginID := ""
	for _, node := range dsl.Nodes {
		node.ID = strings.TrimSpace(node.ID)
		node.Type = strings.TrimSpace(node.Type)
		if node.ID == "" || node.Type == "" {
			return fmt.Errorf("%w: node id and type are required", agenterrors.ErrInvalidInput)
		}
		if _, exists := nodeByID[node.ID]; exists {
			return fmt.Errorf("%w: duplicate node id %s", agenterrors.ErrInvalidInput, node.ID)
		}
		if node.Type == "begin" {
			beginCount++
			beginID = node.ID
		}
		if v.nodes != nil {
			if err := v.nodes.ValidateNodeConfig(node.Type, node.Config); err != nil {
				return err
			}
		}
		nodeByID[node.ID] = node
	}
	if beginCount != 1 {
		return fmt.Errorf("%w: flow must contain exactly one begin node", agenterrors.ErrInvalidInput)
	}
	adjacency := make(map[string][]string, len(nodeByID))
	indegree := make(map[string]int, len(nodeByID))
	for id := range nodeByID {
		indegree[id] = 0
	}
	for _, edge := range dsl.Edges {
		from := strings.TrimSpace(edge.From)
		to := strings.TrimSpace(edge.To)
		if from == "" || to == "" || from == to {
			return fmt.Errorf("%w: invalid edge", agenterrors.ErrInvalidInput)
		}
		if _, ok := nodeByID[from]; !ok {
			return fmt.Errorf("%w: edge references unknown from node %s", agenterrors.ErrInvalidInput, from)
		}
		if _, ok := nodeByID[to]; !ok {
			return fmt.Errorf("%w: edge references unknown to node %s", agenterrors.ErrInvalidInput, to)
		}
		adjacency[from] = append(adjacency[from], to)
		indegree[to]++
	}
	for id, nexts := range adjacency {
		node := nodeByID[id]
		if node.Type != "switch" && len(nexts) > 1 {
			return fmt.Errorf("%w: node %s has multiple outgoing edges", agenterrors.ErrInvalidInput, id)
		}
		if node.Type == "switch" {
			if err := validateSwitchTargets(node, nexts); err != nil {
				return err
			}
		}
	}
	visited := 0
	queue := make([]string, 0, len(indegree))
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range adjacency[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(nodeByID) {
		return fmt.Errorf("%w: flow contains cycle", agenterrors.ErrInvalidInput)
	}
	reachable := map[string]bool{beginID: true}
	queue = []string{beginID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[id] {
			if reachable[next] {
				continue
			}
			reachable[next] = true
			queue = append(queue, next)
		}
	}
	if len(reachable) != len(nodeByID) {
		return fmt.Errorf("%w: all nodes must be reachable from begin", agenterrors.ErrInvalidInput)
	}
	return nil
}

func validateSwitchTargets(node Node, nexts []string) error {
	allowed := make(map[string]bool, len(nexts))
	for _, next := range nexts {
		allowed[next] = true
	}
	var cfg struct {
		Conditions []struct {
			Target string `json:"target"`
		} `json:"conditions"`
	}
	if err := json.Unmarshal(node.Config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid switch config", agenterrors.ErrInvalidInput)
	}
	for _, condition := range cfg.Conditions {
		target := strings.TrimSpace(condition.Target)
		if target == "" || !allowed[target] {
			return fmt.Errorf("%w: switch node %s target %s must be an outgoing edge", agenterrors.ErrInvalidInput, node.ID, target)
		}
	}
	return nil
}
