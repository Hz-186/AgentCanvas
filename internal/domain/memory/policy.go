package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	WriteModeSuggest = "suggest"
)

type Policy struct {
	// Enabled is retained for one compatibility cycle. New clients should use
	// the top-level memory_enabled switch plus RecallEnabled.
	Enabled       *bool  `json:"enabled,omitempty"`
	RecallEnabled *bool  `json:"recall_enabled,omitempty"`
	WriteMode     string `json:"write_mode,omitempty"`
	TopK          int    `json:"top_k,omitempty"`
	TokenBudget   int    `json:"token_budget,omitempty"`
}

func DefaultPolicy() Policy {
	recall := true
	return Policy{
		RecallEnabled: &recall,
		WriteMode:     WriteModeSuggest,
		TopK:          8, TokenBudget: 1200,
	}
}

func ParsePolicy(raw json.RawMessage) (Policy, error) {
	policy := DefaultPolicy()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("{}")) || bytes.Equal(trimmed, []byte("null")) {
		return policy, nil
	}
	if err := json.Unmarshal(trimmed, &policy); err != nil {
		return Policy{}, err
	}
	return policy.Normalize()
}

func (p Policy) Normalize() (Policy, error) {
	if p.RecallEnabled == nil {
		recall := true
		p.RecallEnabled = &recall
	}
	p.WriteMode = strings.TrimSpace(p.WriteMode)
	if p.WriteMode == "" {
		p.WriteMode = WriteModeSuggest
	}
	if p.WriteMode != WriteModeSuggest {
		return Policy{}, fmt.Errorf("unsupported memory write_mode %q", p.WriteMode)
	}
	if p.TopK == 0 {
		p.TopK = 8
	}
	if p.TopK < 1 || p.TopK > 20 {
		return Policy{}, fmt.Errorf("memory top_k must be between 1 and 20")
	}
	if p.TokenBudget == 0 {
		p.TokenBudget = 1200
	}
	if p.TokenBudget < 128 || p.TokenBudget > 8192 {
		return Policy{}, fmt.Errorf("memory token_budget must be between 128 and 8192")
	}
	return p, nil
}

func (p Policy) RecallActive(memoryEnabled bool) bool {
	return memoryEnabled && p.RecallEnabled != nil && *p.RecallEnabled
}
