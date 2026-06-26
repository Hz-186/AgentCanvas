package flow

import (
	"bytes"
	"encoding/json"
	"sort"
)

const SchemaVersionV1 = "v1"

type DSL struct {
	SchemaVersion string `json:"schema_version"` // 固定 "v1"
	FlowID        string `json:"flow_id"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}

type Node struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func ParseDSL(data json.RawMessage) (*DSL, error) {
	var dsl DSL
	if err := json.Unmarshal(data, &dsl); err != nil {
		return nil, err
	}
	return &dsl, nil
}

func EqualRuntimeDSL(left, right *DSL) (bool, error) {
	leftJSON, err := runtimeDSLJSON(left)
	if err != nil {
		return false, err
	}
	rightJSON, err := runtimeDSLJSON(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}

type runtimeDSL struct {
	SchemaVersion string        `json:"schema_version"`
	FlowID        string        `json:"flow_id"`
	Nodes         []runtimeNode `json:"nodes"`
	Edges         []Edge        `json:"edges"`
}

type runtimeNode struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

func runtimeDSLJSON(dsl *DSL) ([]byte, error) {
	if dsl == nil {
		return json.Marshal(runtimeDSL{})
	}
	nodes := make([]runtimeNode, 0, len(dsl.Nodes))
	for _, node := range dsl.Nodes {
		config, err := runtimeConfigJSON(node.Config)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, runtimeNode{ID: node.ID, Type: node.Type, Name: node.Name, Config: config})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].ID != nodes[j].ID {
			return nodes[i].ID < nodes[j].ID
		}
		if nodes[i].Type != nodes[j].Type {
			return nodes[i].Type < nodes[j].Type
		}
		if nodes[i].Name != nodes[j].Name {
			return nodes[i].Name < nodes[j].Name
		}
		return string(nodes[i].Config) < string(nodes[j].Config)
	})
	edges := append([]Edge(nil), dsl.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return json.Marshal(runtimeDSL{SchemaVersion: dsl.SchemaVersion, FlowID: dsl.FlowID, Nodes: nodes, Edges: edges})
}

func runtimeConfigJSON(data json.RawMessage) (json.RawMessage, error) {
	if len(data) == 0 {
		return json.RawMessage("null"), nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if object, ok := value.(map[string]any); ok {
		delete(object, "_ui")
	}
	return json.Marshal(value)
}

// EqualCanvasDSL 比较两个 DSL 是否完全等价，包含节点位置（_ui）信息。
// 节点位置变化或增删节点/边均视为版本变更，用于版本保存时的等价判断。
func EqualCanvasDSL(left, right *DSL) (bool, error) {
	leftJSON, err := canvasDSLJSON(left)
	if err != nil {
		return false, err
	}
	rightJSON, err := canvasDSLJSON(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}

func canvasDSLJSON(dsl *DSL) ([]byte, error) {
	if dsl == nil {
		return json.Marshal(runtimeDSL{})
	}
	nodes := make([]runtimeNode, 0, len(dsl.Nodes))
	for _, node := range dsl.Nodes {
		config, err := canonicalConfigJSON(node.Config)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, runtimeNode{
			ID:     node.ID,
			Type:   node.Type,
			Name:   node.Name,
			Config: config,
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].ID != nodes[j].ID {
			return nodes[i].ID < nodes[j].ID
		}
		if nodes[i].Type != nodes[j].Type {
			return nodes[i].Type < nodes[j].Type
		}
		if nodes[i].Name != nodes[j].Name {
			return nodes[i].Name < nodes[j].Name
		}
		return string(nodes[i].Config) < string(nodes[j].Config)
	})
	edges := append([]Edge(nil), dsl.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return json.Marshal(runtimeDSL{
		SchemaVersion: dsl.SchemaVersion,
		FlowID:        dsl.FlowID,
		Nodes:         nodes,
		Edges:         edges,
	})
}

// canonicalConfigJSON 对 config 做规范化序列化（稳定 key 顺序）
func canonicalConfigJSON(data json.RawMessage) (json.RawMessage, error) {
	if len(data) == 0 {
		return json.RawMessage("null"), nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
