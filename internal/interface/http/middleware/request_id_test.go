package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authusecase "agentcanvas/internal/application/auth_usecase"
	authdomain "agentcanvas/internal/domain/auth"
	"agentcanvas/internal/pkg/observability"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestRequestIDMiddlewarePropagatesIncomingIDToContextAndResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	r.GET("/", func(c *gin.Context) {
		value, ok := observability.CorrelationFromContext(c.Request.Context())
		if !ok || value.RequestID != "rid-123" {
			t.Fatalf("correlation = %+v, ok=%v", value, ok)
		}
		if got, exists := c.Get(RequestIDKey); !exists || got != "rid-123" {
			t.Fatalf("gin request id = %#v, exists=%v", got, exists)
		}
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "rid-123")
	r.ServeHTTP(recorder, req)
	if recorder.Header().Get("X-Request-ID") != "rid-123" {
		t.Fatalf("response request id = %q", recorder.Header().Get("X-Request-ID"))
	}
}

func TestRequestIDMiddlewareGeneratesAndSharesIDWhenHeaderMissing(t *testing.T) {
	r := gin.New()
	r.Use(RequestID())
	var contextID string
	r.GET("/", func(c *gin.Context) {
		value, ok := observability.CorrelationFromContext(c.Request.Context())
		if !ok || value.RequestID == "" {
			t.Fatalf("correlation = %+v, ok=%v", value, ok)
		}
		contextID = value.RequestID
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if contextID == "" || recorder.Header().Get("X-Request-ID") != contextID {
		t.Fatalf("generated ids differ: context=%q header=%q", contextID, recorder.Header().Get("X-Request-ID"))
	}
}

type failingAccessTokenService struct{}

func (failingAccessTokenService) IssueAccessToken(int64) (string, time.Time, error) {
	return "", time.Time{}, errors.New("not implemented")
}
func (failingAccessTokenService) VerifyAccessToken(string) (*authdomain.AccessTokenClaims, error) {
	return nil, errors.New("invalid token")
}

type emptyAPITokenRepository struct{}

func (emptyAPITokenRepository) Create(context.Context, *authdomain.APIToken) error { return nil }
func (emptyAPITokenRepository) ListByOwner(context.Context, int64) ([]authdomain.APIToken, error) {
	return nil, nil
}
func (emptyAPITokenRepository) FindActiveByHash(context.Context, string, time.Time) (*authdomain.APIToken, error) {
	return nil, gorm.ErrRecordNotFound
}
func (emptyAPITokenRepository) RevokeByID(context.Context, int64, int64, time.Time) error { return nil }

func TestAuthMiddlewareLeavesOwnerAbsentForUnauthenticatedRequest(t *testing.T) {
	service := authusecase.NewService(nil, nil, nil, emptyAPITokenRepository{}, nil, nil, failingAccessTokenService{}, nil, nil, nil, time.Hour)
	r := gin.New()
	r.Use(RequestID())
	r.Use(Auth(service, emptyAPITokenRepository{}))
	r.GET("/", func(c *gin.Context) {
		value, _ := observability.CorrelationFromContext(c.Request.Context())
		if value.OwnerID != 0 {
			t.Fatalf("owner unexpectedly present: %+v", value)
		}
		c.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "rid-unauth")
	r.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}
