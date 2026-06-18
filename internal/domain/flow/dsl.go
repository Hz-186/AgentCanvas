package flow

import "encoding/json"

const SchemaVersionV1 = "v1"

type DSL struct {
	SchemaVersion string `json:"schema_version"`
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
