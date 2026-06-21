package flow

import (
	"encoding/json"
	"testing"
)

type testNodeValidator struct {
	types map[string]bool
}

func (v testNodeValidator) ValidateNodeConfig(nodeType string, config []byte) error {
	if !v.types[nodeType] {
		return errUnsupportedNode
	}
	return nil
}

var errUnsupportedNode = &testError{}

type testError struct{}

func (*testError) Error() string { return "unsupported node" }

func TestValidatorAcceptsLinearDSL(t *testing.T) {
	dsl := &DSL{
		SchemaVersion: SchemaVersionV1,
		FlowID:        "flow_test",
		Nodes: []Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{}`)},
			{ID: "message_1", Type: "message", Config: json.RawMessage(`{}`)},
		},
		Edges: []Edge{{From: "begin_1", To: "message_1"}},
	}
	validator := NewValidator(testNodeValidator{types: map[string]bool{"begin": true, "message": true}})
	if err := validator.Validate(dsl); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidatorRejectsCycle(t *testing.T) {
	dsl := &DSL{
		SchemaVersion: SchemaVersionV1,
		FlowID:        "flow_test",
		Nodes: []Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{}`)},
			{ID: "message_1", Type: "message", Config: json.RawMessage(`{}`)},
		},
		Edges: []Edge{{From: "begin_1", To: "message_1"}, {From: "message_1", To: "begin_1"}},
	}
	validator := NewValidator(testNodeValidator{types: map[string]bool{"begin": true, "message": true}})
	if err := validator.Validate(dsl); err == nil {
		t.Fatal("Validate() expected cycle error")
	}
}

func TestValidatorRejectsUnreachableNode(t *testing.T) {
	dsl := &DSL{
		SchemaVersion: SchemaVersionV1,
		FlowID:        "flow_test",
		Nodes: []Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{}`)},
			{ID: "message_1", Type: "message", Config: json.RawMessage(`{}`)},
			{ID: "prompt_1", Type: "prompt", Config: json.RawMessage(`{}`)},
		},
		Edges: []Edge{{From: "begin_1", To: "message_1"}},
	}
	validator := NewValidator(testNodeValidator{types: map[string]bool{"begin": true, "message": true, "prompt": true}})
	if err := validator.Validate(dsl); err == nil {
		t.Fatal("Validate() expected unreachable node error")
	}
}

func TestValidatorRejectsMultipleOutgoingForNonSwitch(t *testing.T) {
	dsl := &DSL{
		SchemaVersion: SchemaVersionV1,
		FlowID:        "flow_test",
		Nodes: []Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{}`)},
			{ID: "message_1", Type: "message", Config: json.RawMessage(`{}`)},
			{ID: "message_2", Type: "message", Config: json.RawMessage(`{}`)},
		},
		Edges: []Edge{{From: "begin_1", To: "message_1"}, {From: "begin_1", To: "message_2"}},
	}
	validator := NewValidator(testNodeValidator{types: map[string]bool{"begin": true, "message": true}})
	if err := validator.Validate(dsl); err == nil {
		t.Fatal("Validate() expected multiple outgoing error")
	}
}

func TestValidatorAcceptsSwitchTargetsOnOutgoingEdges(t *testing.T) {
	dsl := &DSL{
		SchemaVersion: SchemaVersionV1,
		FlowID:        "flow_test",
		Nodes: []Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{}`)},
			{ID: "switch_1", Type: "switch", Config: json.RawMessage(`{"conditions":[{"expr":"{{sys.count}} > 0","target":"message_1"},{"expr":"default","target":"message_2"}]}`)},
			{ID: "message_1", Type: "message", Config: json.RawMessage(`{}`)},
			{ID: "message_2", Type: "message", Config: json.RawMessage(`{}`)},
		},
		Edges: []Edge{{From: "begin_1", To: "switch_1"}, {From: "switch_1", To: "message_1"}, {From: "switch_1", To: "message_2"}},
	}
	validator := NewValidator(testNodeValidator{types: map[string]bool{"begin": true, "switch": true, "message": true}})
	if err := validator.Validate(dsl); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidatorRejectsSwitchTargetOutsideOutgoingEdges(t *testing.T) {
	dsl := &DSL{
		SchemaVersion: SchemaVersionV1,
		FlowID:        "flow_test",
		Nodes: []Node{
			{ID: "begin_1", Type: "begin", Config: json.RawMessage(`{}`)},
			{ID: "switch_1", Type: "switch", Config: json.RawMessage(`{"conditions":[{"expr":"default","target":"message_2"}]}`)},
			{ID: "message_1", Type: "message", Config: json.RawMessage(`{}`)},
			{ID: "message_2", Type: "message", Config: json.RawMessage(`{}`)},
		},
		Edges: []Edge{{From: "begin_1", To: "switch_1"}, {From: "switch_1", To: "message_1"}, {From: "message_1", To: "message_2"}},
	}
	validator := NewValidator(testNodeValidator{types: map[string]bool{"begin": true, "switch": true, "message": true}})
	if err := validator.Validate(dsl); err == nil {
		t.Fatal("Validate() expected switch target error")
	}
}
