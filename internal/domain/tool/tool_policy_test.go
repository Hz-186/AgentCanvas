package tool

import (
	"encoding/json"
	"testing"
)

func TestToolPolicyRequireApprovalForRiskSlice(t *testing.T) {
	p := &ToolPolicy{RequireApprovalForRisk: json.RawMessage(`["high","medium"]`)}
	risks := p.RequireApprovalForRiskSlice()
	if len(risks) != 2 || risks[0] != "high" || risks[1] != "medium" {
		t.Fatalf("unexpected risks: %v", risks)
	}
}

func TestToolPolicyAllowedHostsSlice(t *testing.T) {
	p := &ToolPolicy{AllowedHosts: json.RawMessage(`["api.example.com"]`)}
	hosts := p.AllowedHostsSlice()
	if len(hosts) != 1 || hosts[0] != "api.example.com" {
		t.Fatalf("unexpected hosts: %v", hosts)
	}
}

func TestToolPolicyNormalize(t *testing.T) {
	p := &ToolPolicy{
		RequireApprovalForRisk: json.RawMessage("null"),
		AllowedHosts:           json.RawMessage(""),
	}
	p.normalize()
	if string(p.RequireApprovalForRisk) != "[]" {
		t.Fatalf("expected [], got %s", p.RequireApprovalForRisk)
	}
	if string(p.AllowedHosts) != "[]" {
		t.Fatalf("expected [], got %s", p.AllowedHosts)
	}
}

func TestToolPolicyTableName(t *testing.T) {
	p := ToolPolicy{}
	if p.TableName() != "tool_policies" {
		t.Fatalf("expected tool_policies, got %s", p.TableName())
	}
}

func TestDecodeStringSliceEmpty(t *testing.T) {
	ids := decodeStringSlice(json.RawMessage("null"))
	if ids != nil {
		t.Fatalf("expected nil, got %v", ids)
	}
}

func TestNormalizeFieldPreservesValid(t *testing.T) {
	raw := json.RawMessage(`["valid"]`)
	normalized := normalizeField(raw)
	if string(normalized) != `["valid"]` {
		t.Fatalf("expected [\"valid\"], got %s", normalized)
	}
}

func TestToolPackTableName(t *testing.T) {
	p := ToolPack{}
	if p.TableName() != "tool_packs" {
		t.Fatalf("expected tool_packs, got %s", p.TableName())
	}
}

func TestToolPackItemTableName(t *testing.T) {
	i := ToolPackItem{}
	if i.TableName() != "tool_pack_items" {
		t.Fatalf("expected tool_pack_items, got %s", i.TableName())
	}
}
