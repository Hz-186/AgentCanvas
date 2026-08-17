package jsonutil

import (
	"encoding/json"
	"testing"
)

func TestHashCanonicalizesJSON(t *testing.T) {
	left := Hash(json.RawMessage(`{"b":2,"a":1}`))
	right := Hash(json.RawMessage("{\n  \"a\": 1, \"b\": 2\n}"))
	if left != right {
		t.Fatalf("equivalent JSON produced different hashes: %s != %s", left, right)
	}
}
