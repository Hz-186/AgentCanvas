package workflow

import (
	"encoding/json"
	"testing"
)

func TestProfileDefaultToolIDsSlice(t *testing.T) {
	p := &Profile{DefaultToolIDs: json.RawMessage(`[1, 2, 3]`)}
	ids := p.DefaultToolIDsSlice()
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestProfileDefaultToolPackIDsSlice(t *testing.T) {
	p := &Profile{DefaultToolPackIDs: json.RawMessage(`[7, 8]`)}
	ids := p.DefaultToolPackIDsSlice()
	if len(ids) != 2 || ids[0] != 7 || ids[1] != 8 {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestProfileDefaultMCPServerIDsSlice(t *testing.T) {
	p := &Profile{DefaultMCPServerIDs: json.RawMessage(`[11, 12]`)}
	ids := p.DefaultMCPServerIDsSlice()
	if len(ids) != 2 || ids[0] != 11 || ids[1] != 12 {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestProfileDefaultToolIDsNil(t *testing.T) {
	p := &Profile{}
	ids := p.DefaultToolIDsSlice()
	if ids != nil {
		t.Fatalf("expected nil, got %v", ids)
	}
}

func TestProfileDefaultKnowledgeIDsSlice(t *testing.T) {
	p := &Profile{DefaultKnowledgeIDs: json.RawMessage(`[10, 20]`)}
	ids := p.DefaultKnowledgeIDsSlice()
	if len(ids) != 2 || ids[0] != 10 || ids[1] != 20 {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestProfileDefaultCallWorkflowIDsSlice(t *testing.T) {
	p := &Profile{DefaultCallWorkflowIDs: json.RawMessage(`[100, 200, 300]`)}
	ids := p.DefaultCallWorkflowIDsSlice()
	if len(ids) != 3 || ids[0] != 100 || ids[1] != 200 || ids[2] != 300 {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestProfileDefaultCallAgentIDsEmpty(t *testing.T) {
	p := &Profile{DefaultCallWorkflowIDs: json.RawMessage(`[]`)}
	ids := p.DefaultCallWorkflowIDsSlice()
	if len(ids) != 0 {
		t.Fatalf("expected empty slice for empty array, got %v", ids)
	}
}

func TestProfileNormalizeJSONNull(t *testing.T) {
	p := &Profile{
		DefaultToolPackIDs:     json.RawMessage("null"),
		DefaultToolIDs:         json.RawMessage("null"),
		DefaultMCPServerIDs:    json.RawMessage("null"),
		DefaultKnowledgeIDs:    json.RawMessage("null"),
		DefaultCallWorkflowIDs: json.RawMessage("null"),
		OutputSchemaJSON:       json.RawMessage("null"),
	}
	p.normalizeJSON()
	if string(p.DefaultToolPackIDs) != "[]" {
		t.Fatalf("expected [], got %s", p.DefaultToolPackIDs)
	}
	if string(p.DefaultToolIDs) != "[]" {
		t.Fatalf("expected [], got %s", p.DefaultToolIDs)
	}
	if string(p.DefaultMCPServerIDs) != "[]" {
		t.Fatalf("expected [], got %s", p.DefaultMCPServerIDs)
	}
	if string(p.DefaultKnowledgeIDs) != "[]" {
		t.Fatalf("expected [], got %s", p.DefaultKnowledgeIDs)
	}
	if string(p.DefaultCallWorkflowIDs) != "[]" {
		t.Fatalf("expected [], got %s", p.DefaultCallWorkflowIDs)
	}
	if string(p.OutputSchemaJSON) != "{}" {
		t.Fatalf("expected {}, got %s", p.OutputSchemaJSON)
	}
}

func TestProfileNormalizeJSONEmpty(t *testing.T) {
	p := &Profile{
		DefaultToolPackIDs:     json.RawMessage(""),
		DefaultToolIDs:         json.RawMessage(""),
		DefaultMCPServerIDs:    json.RawMessage(""),
		DefaultKnowledgeIDs:    json.RawMessage(""),
		DefaultCallWorkflowIDs: json.RawMessage(""),
		OutputSchemaJSON:       json.RawMessage(""),
	}
	p.normalizeJSON()
	if string(p.DefaultToolIDs) != "[]" {
		t.Fatalf("expected [], got %s", p.DefaultToolIDs)
	}
	if string(p.DefaultToolPackIDs) != "[]" || string(p.DefaultMCPServerIDs) != "[]" || string(p.OutputSchemaJSON) != "{}" {
		t.Fatalf("unexpected normalized profile: %+v", p)
	}
}

func TestProfileNormalizeJSONPreservesValid(t *testing.T) {
	p := &Profile{
		DefaultToolPackIDs:     json.RawMessage(`[9]`),
		DefaultToolIDs:         json.RawMessage(`[1]`),
		DefaultMCPServerIDs:    json.RawMessage(`[10]`),
		DefaultKnowledgeIDs:    json.RawMessage(`[2]`),
		DefaultCallWorkflowIDs: json.RawMessage(`[3]`),
		OutputSchemaJSON:       json.RawMessage(`{"type":"object"}`),
	}
	p.normalizeJSON()
	if string(p.DefaultToolIDs) != `[1]` {
		t.Fatalf("expected [1], got %s", p.DefaultToolIDs)
	}
	if string(p.DefaultKnowledgeIDs) != `[2]` {
		t.Fatalf("expected [2], got %s", p.DefaultKnowledgeIDs)
	}
	if string(p.DefaultCallWorkflowIDs) != `[3]` {
		t.Fatalf("expected [3], got %s", p.DefaultCallWorkflowIDs)
	}
	if string(p.DefaultToolPackIDs) != `[9]` || string(p.DefaultMCPServerIDs) != `[10]` || string(p.OutputSchemaJSON) != `{"type":"object"}` {
		t.Fatalf("unexpected preserved profile JSON: %+v", p)
	}
}

func TestProfileTableName(t *testing.T) {
	p := Profile{}
	if p.TableName() != "workflow_profiles" {
		t.Fatalf("expected workflow_profiles, got %s", p.TableName())
	}
}

func TestProfileDecodeInt64SliceInvalid(t *testing.T) {
	ids := decodeInt64Slice(json.RawMessage(`{"invalid": true}`))
	if ids != nil {
		t.Fatalf("expected nil for invalid JSON, got %v", ids)
	}
}
