package middleware

import (
	authusecase "agentcanvas/internal/application/auth_usecase"
	"agentcanvas/internal/domain/audit"
	authdomain "agentcanvas/internal/domain/auth"
	agenterrors "agentcanvas/internal/pkg/errors"
	"agentcanvas/internal/pkg/observability"
	"agentcanvas/internal/pkg/response"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const UserIDKey = "user_id"
const PrincipalKey = "principal"

const (
	AuthKindKey      = "auth_kind"
	AuthScopesKey    = "auth_scopes"
	authKindJWT      = "jwt"
	authKindAPIToken = "api_token"
)

type Principal struct {
	UserID int64
	Kind   string
	Scopes []string
}

func Auth(authService *authusecase.Service, apiTokens authdomain.APITokenRepository, auditRepositories ...audit.Repository) gin.HandlerFunc {
	var audits audit.Repository
	if len(auditRepositories) > 0 {
		audits = auditRepositories[0]
	}
	return func(c *gin.Context) {
		authorization := strings.TrimSpace(c.GetHeader("Authorization"))
		if authorization == "" {
			response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
			c.Abort()
			return
		}
		parts := strings.SplitN(authorization, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
			c.Abort()
			return
		}
		token := strings.TrimSpace(parts[1])
		if token == "" {
			response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
			c.Abort()
			return
		}

		if claims, err := authService.VerifyAccessToken(token); err == nil {
			setPrincipal(c, Principal{UserID: claims.UserID, Kind: authKindJWT})
			if audits != nil {
				c.Set(auditRepositoryKey, audits)
			}
			c.Next()
			return
		}

		if strings.HasPrefix(token, "ac_") {
			hash := authService.HashToken(token)
			apiToken, err := apiTokens.FindActiveByHash(c.Request.Context(), hash, time.Now().UTC())
			if err == nil {
				c.Set(UserIDKey, apiToken.OwnerID)
				var scopes []string
				if raw := strings.TrimSpace(apiToken.Scopes); raw != "" {
					if jsonErr := json.Unmarshal([]byte(raw), &scopes); jsonErr != nil {
						response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
						c.Abort()
						return
					}
				}
				setPrincipal(c, Principal{UserID: apiToken.OwnerID, Kind: authKindAPIToken, Scopes: scopes})
				if audits != nil {
					c.Set(auditRepositoryKey, audits)
				}
				c.Next()
				return
			}
			if err != nil && err != gorm.ErrRecordNotFound {
				response.Error(c, http.StatusInternalServerError, agenterrors.CodeInternal, err.Error())
				c.Abort()
				return
			}
		}

		response.Error(c, http.StatusUnauthorized, agenterrors.CodeUnauthorized, agenterrors.ErrUnauthorized.Error())
		c.Abort()
	}
}

const auditRepositoryKey = "audit_repository"

func setPrincipal(c *gin.Context, principal Principal) {
	c.Set(PrincipalKey, principal)
	c.Set(UserIDKey, principal.UserID)
	c.Set(AuthKindKey, principal.Kind)
	c.Set(AuthScopesKey, append([]string(nil), principal.Scopes...))
	correlation, _ := observability.CorrelationFromContext(c.Request.Context())
	c.Request = c.Request.WithContext(observability.WithCorrelation(c.Request.Context(), correlation.WithOwnerID(principal.UserID)))
}

// RequireRouteScope keeps scope policy in one place while allowing regular JWT
// sessions to retain their existing user-authorized behavior. API tokens are
// fail-closed: an empty or malformed scope list cannot access a protected API.
func RequireRouteScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		if kind, _ := c.Get(AuthKindKey); kind != authKindAPIToken {
			c.Next()
			return
		}
		required := routeScope(c.Request.Method, c.Request.URL.Path)
		if scopes, ok := c.Get(AuthScopesKey); ok {
			if values, ok := scopes.([]string); ok {
				for _, scope := range values {
					if scope == "*" || scope == "admin" || scope == required {
						c.Next()
						return
					}
				}
			}
		}
		response.Error(c, http.StatusForbidden, agenterrors.CodeForbidden, agenterrors.ErrForbidden.Error())
		recordScopeDenied(c, required)
		c.Abort()
	}
}

func recordScopeDenied(c *gin.Context, required string) {
	value, ok := c.Get(auditRepositoryKey)
	repo, repoOK := value.(audit.Repository)
	principal, principalOK := c.Get(PrincipalKey)
	p, pOK := principal.(Principal)
	if !ok || !repoOK || !principalOK || !pOK || p.UserID <= 0 {
		return
	}
	detail := map[string]any{
		"required_scope": required,
		"method":         c.Request.Method,
		"path":           c.Request.URL.Path,
	}
	if err := repo.Create(c.Request.Context(), audit.NewLog(p.UserID, p.UserID, "auth.scope_denied", "http", c.Request.URL.Path, detail, c.ClientIP(), c.Request.UserAgent())); err != nil {
		slog.Default().Warn("record scope denial audit failed", "error", err)
	}
}

func routeScope(method, path string) string {
	access := "read"
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		access = "write"
	}
	resource := "resource"
	switch {
	case strings.HasPrefix(path, "/api/v1/runs"), strings.HasPrefix(path, "/api/v1/agent-turns"):
		resource = "run"
	case strings.HasPrefix(path, "/api/v1/agents"), strings.HasPrefix(path, "/api/v1/workspaces"):
		resource = "agent"
	case strings.HasPrefix(path, "/api/v1/memories"), strings.HasPrefix(path, "/api/v1/memory-"):
		resource = "memory"
	case strings.HasPrefix(path, "/api/v1/provider-catalog"), strings.HasPrefix(path, "/api/v1/model-providers"):
		resource = "resource"
	case strings.HasPrefix(path, "/api/v1/api-tokens"):
		return "admin"
	}
	return resource + ":" + access
}
