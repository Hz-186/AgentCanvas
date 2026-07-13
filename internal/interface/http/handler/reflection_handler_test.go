package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	reflectionusecase "agentcanvas/internal/application/reflection_usecase"
	"agentcanvas/internal/domain/reflection"
	agenterrors "agentcanvas/internal/pkg/errors"

	"github.com/gin-gonic/gin"
)

type fakeReflectionHTTPService struct {
	items        []reflection.Reflection
	err          error
	ownerID      int64
	workflowID   int64
	runID        int64
	reflectionID int64
	status       string
	verdict      string
	limit        int
	offset       int
}

func (f *fakeReflectionHTTPService) List(_ context.Context, ownerID, workflowID int64, status string, limit, offset int) ([]reflection.Reflection, error) {
	f.ownerID, f.workflowID, f.status, f.limit, f.offset = ownerID, workflowID, status, limit, offset
	return f.items, f.err
}

func (f *fakeReflectionHTTPService) SetStatus(_ context.Context, ownerID, workflowID, reflectionID int64, req reflectionusecase.UpdateStatusRequest) error {
	f.ownerID, f.workflowID, f.reflectionID, f.status = ownerID, workflowID, reflectionID, req.Status
	return f.err
}

func (f *fakeReflectionHTTPService) Feedback(_ context.Context, ownerID, runID, reflectionID int64, req reflectionusecase.FeedbackRequest) error {
	f.ownerID, f.runID, f.reflectionID, f.verdict = ownerID, runID, reflectionID, req.Verdict
	return f.err
}

func TestReflectionHandlerRequiresAuthentication(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/workflows/20/reflections", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "20"}}

	NewReflectionHandler(&fakeReflectionHTTPService{}).List(ctx)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected HTTP 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestReflectionHandlerListsOwnerScopedWorkflowItems(t *testing.T) {
	service := &fakeReflectionHTTPService{items: []reflection.Reflection{{ID: 7, OwnerID: 1, WorkflowID: 20}}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/workflows/20/reflections?status=active&limit=10&offset=2", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "20"}}
	ctx.Set("user_id", int64(1))

	NewReflectionHandler(service).List(ctx)

	if recorder.Code != http.StatusOK || service.ownerID != 1 || service.workflowID != 20 || service.status != "active" || service.limit != 10 || service.offset != 2 {
		t.Fatalf("unexpected list forwarding: code=%d service=%+v body=%s", recorder.Code, service, recorder.Body.String())
	}
}

func TestReflectionHandlerMapsCrossWorkflowStatusUpdateToForbidden(t *testing.T) {
	service := &fakeReflectionHTTPService{err: agenterrors.ErrForbidden}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/workflows/20/reflections/7", strings.NewReader(`{"status":"archived"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: "20"}, {Key: "reflection_id", Value: "7"}}
	ctx.Set("user_id", int64(1))

	NewReflectionHandler(service).SetStatus(ctx)

	if recorder.Code != http.StatusForbidden || service.reflectionID != 7 {
		t.Fatalf("expected HTTP 403, got %d service=%+v body=%s", recorder.Code, service, recorder.Body.String())
	}
}

func TestReflectionHandlerForwardsRunFeedback(t *testing.T) {
	service := &fakeReflectionHTTPService{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/runs/30/reflections/7/feedback", strings.NewReader(`{"verdict":"helpful","note":"fixed the retry"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "id", Value: "30"}, {Key: "reflection_id", Value: "7"}}
	ctx.Set("user_id", int64(1))

	NewReflectionHandler(service).Feedback(ctx)

	if recorder.Code != http.StatusOK || service.ownerID != 1 || service.runID != 30 || service.reflectionID != 7 || service.verdict != "helpful" {
		t.Fatalf("unexpected feedback forwarding: code=%d service=%+v body=%s", recorder.Code, service, recorder.Body.String())
	}
}
