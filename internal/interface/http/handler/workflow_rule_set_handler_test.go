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

func TestPublishRuleSetReturnsSynchronously(t *testing.T) {
	service := &fakeRuleSetHTTPService{item: &workflow.RuleSet{ID: 30, Status: workflow.RuleSetStatusPublished}}
	handler := &WorkflowHandler{ruleSets: service}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/workflows/20/rule-sets/30/publish", strings.NewReader(`{"expected_revision":4}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: "20"}, {Key: "rule_set_id", Value: "30"}}
	ctx.Set("user_id", int64(1))
	handler.PublishRuleSet(ctx)
	if recorder.Code != http.StatusOK || service.actorID != 1 || service.expectedRevision != 4 || !strings.Contains(recorder.Body.String(), "published") {
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

func TestCreateRuleSetRejectsLegacyLevelWithHTTP400(t *testing.T) {
	handler := &WorkflowHandler{ruleSets: &fakeRuleSetHTTPService{}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/workflows/20/rule-sets", strings.NewReader(`{"rules":[{"id":"tenant.legacy","level":"l2_scenario","content":"legacy"}]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: "20"}}
	ctx.Set("user_id", int64(1))
	handler.CreateRuleSet(ctx)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "no longer supported") {
		t.Fatalf("expected HTTP 400 for removed level field, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

type fakeRuleSetHTTPService struct {
	workflowRuleSetHTTPService
	item             *workflow.RuleSet
	err              error
	actorID          int64
	expectedRevision int64
}

func (f *fakeRuleSetHTTPService) PublishRuleSet(_ context.Context, _, _, _, actorID int64, req agentusecase.PublishRuleSetRequest) (*workflow.RuleSet, error) {
	f.actorID = actorID
	f.expectedRevision = req.ExpectedRevision
	return f.item, f.err
}
