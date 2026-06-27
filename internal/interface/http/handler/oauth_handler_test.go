package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authusecase "agentcanvas/internal/application/auth_usecase"
	authdomain "agentcanvas/internal/domain/auth"
	"agentcanvas/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

func TestGitHubCallbackRedirectsWithOneTimeCodeOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewOAuthHandler(&fakeOAuthService{loginCode: "one-time-code"})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/callback?code=abc&state=state", nil)

	handler.GitHubCallback(ctx)

	if recorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusFound)
	}
	location := recorder.Header().Get("Location")
	if !strings.Contains(location, "oauth_code=one-time-code") {
		t.Fatalf("Location = %q, want oauth_code", location)
	}
	if strings.Contains(location, "access_token") || strings.Contains(location, "refresh_token") {
		t.Fatalf("Location leaks tokens: %q", location)
	}
}

func TestExchangeCodeReturnsAuthResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	expiresAt := time.Now().UTC().Add(time.Hour)
	service := &fakeOAuthService{resp: &authusecase.AuthResponse{
		User: authusecase.UserDTO{ID: 10, Username: "octo"},
		Tokens: authdomain.TokenPair{
			AccessToken:  "access",
			RefreshToken: "refresh",
			TokenType:    "Bearer",
			ExpiresAt:    expiresAt,
		},
	}}
	handler := NewOAuthHandler(service)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/exchange", strings.NewReader(`{"code":"one-time-code"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.ExchangeCode(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var body response.Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	data, ok := body.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", body.Data)
	}
	user, ok := data["user"].(map[string]any)
	if !ok || user["username"] != "octo" {
		t.Fatalf("user = %#v", data["user"])
	}
	tokens, ok := data["tokens"].(map[string]any)
	if !ok || tokens["access_token"] != "access" || tokens["refresh_token"] != "refresh" {
		t.Fatalf("tokens = %#v", data["tokens"])
	}
}

type fakeOAuthService struct {
	loginCode string
	resp      *authusecase.AuthResponse
}

func (s *fakeOAuthService) BeginGitHubOAuth(context.Context) (string, error) {
	return "https://github.com/login/oauth/authorize", nil
}

func (s *fakeOAuthService) HandleGitHubCallbackCode(context.Context, string, string, authusecase.ClientInfo) (string, error) {
	return s.loginCode, nil
}

func (s *fakeOAuthService) ExchangeOAuthCode(context.Context, string) (*authusecase.AuthResponse, error) {
	return s.resp, nil
}
