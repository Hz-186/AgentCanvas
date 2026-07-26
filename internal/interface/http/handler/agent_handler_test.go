package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentusecase "agentcanvas/internal/application/agent_usecase"

	"github.com/gin-gonic/gin"
)

func TestBindStrictAgentJSONRejectsInternalConfiguration(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "legacy goal", body: `{"name":"agent","settings":{"provider_id":1,"model":"","system_prompt":"","knowledge_ids":[]},"goal":"hidden"}`},
		{name: "complete definition", body: `{"name":"agent","definition":{"provider_id":1}}`},
		{name: "internal policy", body: `{"provider_id":1,"model":"","system_prompt":"","knowledge_ids":[],"tool_policy_json":{}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/agents", strings.NewReader(test.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			var target any
			if test.name == "internal policy" {
				target = &agentusecase.AgentEditableSettings{}
			} else {
				target = &agentusecase.CreateAgentRequest{}
			}
			if err := bindStrictAgentJSON(ctx, target); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("expected unknown field rejection, got %v", err)
			}
		})
	}
}
