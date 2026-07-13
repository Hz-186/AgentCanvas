package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentusecase "agentcanvas/internal/application/workflow_usecase"
	"agentcanvas/internal/domain/workflow"

	"github.com/gin-gonic/gin"
)

func TestPublishRuleSetReturnsAcceptedAndForwardsIdempotencyKey(t *testing.T) {
	service := &fakeRuleSetHTTPService{job: &workflow.RuleCompileJob{ID: 55, Status: workflow.RuleCompileJobQueued}}
	handler := &WorkflowHandler{ruleSets: service}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/workflows/20/rule-sets/30/publish", strings.NewReader(`{"expected_revision":4}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("Idempotency-Key", "publish-30-v4")
	ctx.Params = gin.Params{{Key: "id", Value: "20"}, {Key: "rule_set_id", Value: "30"}}
	ctx.Set("user_id", int64(1))
	handler.PublishRuleSet(ctx)
	if recorder.Code != http.StatusAccepted || service.idempotencyKey != "publish-30-v4" || service.expectedRevision != 4 {
		t.Fatalf("unexpected publish response/code forwarding: code=%d service=%+v body=%s", recorder.Code, service, recorder.Body.String())
	}
}

func TestPublishRuleSetMapsRevisionConflictToHTTP409(t *testing.T) {
	service := &fakeRuleSetHTTPService{err: workflow.ErrRuleSetConflict}
	handler := &WorkflowHandler{ruleSets: service}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/workflows/20/rule-sets/30/publish", strings.NewReader(`{"expected_revision":4}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: "20"}, {Key: "rule_set_id", Value: "30"}}
	ctx.Set("user_id", int64(1))
	handler.PublishRuleSet(ctx)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected HTTP 409, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

type fakeRuleSetHTTPService struct {
	workflowRuleSetHTTPService
	job              *workflow.RuleCompileJob
	err              error
	idempotencyKey   string
	expectedRevision int64
}

func (f *fakeRuleSetHTTPService) PublishRuleSet(_ context.Context, _, _, _ int64, idempotencyKey string, req agentusecase.PublishRuleSetRequest) (*workflow.RuleCompileJob, error) {
	f.idempotencyKey = idempotencyKey
	f.expectedRevision = req.ExpectedRevision
	return f.job, f.err
}
