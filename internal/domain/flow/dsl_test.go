package flow

import (
	"encoding/json"
	"testing"
)

func TestEqualRuntimeDSLIgnoresLayoutAndOrdering(t *testing.T) {
	left := parseTestDSL(t, `{
		"schema_version":"v1",
		"flow_id":"agent-1",
		"nodes":[
			{"id":"begin","type":"begin","name":"Begin","config":{"_ui":{"x":10,"y":20},"input_schema":{"query":"string"}}},
			{"id":"message","type":"message","name":"Message","config":{"content":"{{sys.query}}","_ui":{"x":200,"y":20}}}
		],
		"edges":[{"from":"begin","to":"message"}]
	}`)
	right := parseTestDSL(t, `{
		"schema_version":"v1",
		"flow_id":"agent-1",
		"nodes":[
			{"id":"message","type":"message","name":"Message","config":{"_ui":{"x":360,"y":80},"content":"{{sys.query}}"}},
			{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"query":"string"},"_ui":{"x":40,"y":70}}}
		],
		"edges":[{"from":"begin","to":"message"}]
	}`)

	equal, err := EqualRuntimeDSL(left, right)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatal("expected layout-only changes to be equal")
	}
}

func TestEqualRuntimeDSLDetectsRuntimeChanges(t *testing.T) {
	left := parseTestDSL(t, `{
		"schema_version":"v1",
		"flow_id":"agent-1",
		"nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"query":"string"}}}],
		"edges":[]
	}`)
	right := parseTestDSL(t, `{
		"schema_version":"v1",
		"flow_id":"agent-1",
		"nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"question":"string"}}}],
		"edges":[]
	}`)

	equal, err := EqualRuntimeDSL(left, right)
	if err != nil {
		t.Fatal(err)
	}
	if equal {
		t.Fatal("expected config changes to be different")
	}
}

func TestEqualCanvasDSLSamePositionIsEqual(t *testing.T) {
	left := parseTestDSL(t, `{"schema_version":"v1","flow_id":"agent-1","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"_ui":{"x":120,"y":170},"input_schema":{"query":"string"}}}],"edges":[]}`)
	// key 顺序不同，但位置和内容完全相同
	right := parseTestDSL(t, `{"schema_version":"v1","flow_id":"agent-1","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"input_schema":{"query":"string"},"_ui":{"x":120,"y":170}}}],"edges":[]}`)
	equal, err := EqualCanvasDSL(left, right)
	if err != nil {
		t.Fatal(err)
	}
	if !equal {
		t.Fatal("expected identical canvas DSL to be equal")
	}
}

func TestEqualCanvasDSLPositionChangeIsNotEqual(t *testing.T) {
	left := parseTestDSL(t, `{"schema_version":"v1","flow_id":"agent-1","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"_ui":{"x":120,"y":170},"input_schema":{"query":"string"}}}],"edges":[]}`)
	// 逻辑完全相同，仅 _ui 位置不同
	right := parseTestDSL(t, `{"schema_version":"v1","flow_id":"agent-1","nodes":[{"id":"begin","type":"begin","name":"Begin","config":{"_ui":{"x":480,"y":260},"input_schema":{"query":"string"}}}],"edges":[]}`)
	equal, err := EqualCanvasDSL(left, right)
	if err != nil {
		t.Fatal(err)
	}
	if equal {
		t.Fatal("expected position change to make canvas DSL not equal")
	}
}

func parseTestDSL(t *testing.T, raw string) *DSL {
	t.Helper()
	var data json.RawMessage = []byte(raw)
	dsl, err := ParseDSL(data)
	if err != nil {
		t.Fatal(err)
	}
	return dsl
}
