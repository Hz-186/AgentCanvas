package middleware

import (
	"context"
	"testing"

	"agentcanvas/internal/domain/audit"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
)

type scopeAuditRepo struct{ logs []audit.Log }

func (r *scopeAuditRepo) Create(_ context.Context, item *audit.Log) error {
	r.logs = append(r.logs, *item)
	return nil
}

func (*scopeAuditRepo) ListByOwner(context.Context, int64, int, int) ([]audit.Log, error) {
	return nil, nil
}

func TestRequireRouteScopeAuthorizesReadWriteAndAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		method string
		path   string
		scopes []string
		want   int
	}{
		{name: "agent read", method: http.MethodGet, path: "/api/v1/agents", scopes: []string{"agent:read"}, want: http.StatusNoContent},
		{name: "agent read cannot write", method: http.MethodPost, path: "/api/v1/agents", scopes: []string{"agent:read"}, want: http.StatusForbidden},
		{name: "agent write", method: http.MethodPost, path: "/api/v1/agents", scopes: []string{"agent:write"}, want: http.StatusNoContent},
		{name: "run read", method: http.MethodGet, path: "/api/v1/runs/1", scopes: []string{"run:read"}, want: http.StatusNoContent},
		{name: "empty fails closed", method: http.MethodGet, path: "/api/v1/memories", scopes: nil, want: http.StatusForbidden},
		{name: "admin can access token management", method: http.MethodGet, path: "/api/v1/api-tokens", scopes: []string{"admin"}, want: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(AuthKindKey, authKindAPIToken)
				c.Set(AuthScopesKey, test.scopes)
				c.Next()
			})
			router.Use(RequireRouteScope())
			router.Handle(test.method, test.path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != test.want {
				t.Fatalf("status = %d body=%s, want %d", recorder.Code, recorder.Body.String(), test.want)
			}
		})
	}
}

func TestRequireRouteScopeRecordsDeniedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	audits := &scopeAuditRepo{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(AuthKindKey, authKindAPIToken)
		c.Set(AuthScopesKey, []string{"agent:read"})
		c.Set(PrincipalKey, Principal{UserID: 7, Kind: authKindAPIToken, Scopes: []string{"agent:read"}})
		c.Set(auditRepositoryKey, audit.Repository(audits))
		c.Next()
	})
	router.Use(RequireRouteScope())
	router.POST("/api/v1/agents", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/agents", nil))
	if recorder.Code != http.StatusForbidden || len(audits.logs) != 1 {
		t.Fatalf("status=%d audits=%+v", recorder.Code, audits.logs)
	}
	if audits.logs[0].Action != "auth.scope_denied" || audits.logs[0].OwnerID != 7 {
		t.Fatalf("audit=%+v", audits.logs[0])
	}
}
