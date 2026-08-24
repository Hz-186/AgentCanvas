package auth_usecase

import (
	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/audit"
	authdomain "agentcanvas/internal/domain/auth"
	"agentcanvas/internal/domain/user"
	agenterrors "agentcanvas/internal/pkg/errors"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Service struct {
	users       user.Repository
	oauth       authdomain.OAuthRepository
	sessions    authdomain.SessionRepository
	apiTokens   authdomain.APITokenRepository
	audits      audit.Repository
	passwords   authdomain.PasswordHasher
	jwt         authdomain.AccessTokenService
	tokenHasher authdomain.TokenHasher
	redis       *redis.Client
	github      authdomain.GitHubOAuthClient
	refreshTTL  time.Duration
}

type ClientInfo struct {
	UserAgent string
	IPAddress string
}

type RegisterRequest struct {
	Username string `json:"username" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type UserDTO struct { // back to front
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Email       *string    `json:"email"`
	AvatarURL   string     `json:"avatar_url"`
	LoginType   string     `json:"login_type"`
	Status      int        `json:"status"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type AuthResponse struct {
	User   UserDTO              `json:"user"`
	Tokens authdomain.TokenPair `json:"tokens"` // access token + refresh token
}

type CreateAPITokenRequest struct {
	Name      string     `json:"name" binding:"required"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

var apiTokenScopes = map[string]struct{}{
	"agent:read": {}, "agent:write": {}, "run:read": {}, "run:write": {},
	"resource:read": {}, "resource:write": {}, "memory:read": {}, "memory:write": {},
	"admin": {}, "*": {},
}

func NewService(users user.Repository,
	oauth authdomain.OAuthRepository,
	sessions authdomain.SessionRepository,
	apiTokens authdomain.APITokenRepository,
	audits audit.Repository,
	passwords authdomain.PasswordHasher,
	jwt authdomain.AccessTokenService,
	tokenHasher authdomain.TokenHasher,
	redisClient *redis.Client,
	githubClient authdomain.GitHubOAuthClient,
	refreshTTL time.Duration) *Service {
	return &Service{
		users:       users,
		oauth:       oauth,
		sessions:    sessions,
		apiTokens:   apiTokens,
		audits:      audits,
		passwords:   passwords,
		jwt:         jwt,
		tokenHasher: tokenHasher,
		redis:       redisClient,
		github:      githubClient,
		refreshTTL:  refreshTTL,
	}
}

func (s *Service) Register(ctx context.Context, req RegisterRequest, client ClientInfo) (*AuthResponse, error) {
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Email == "" || len(req.Password) < 8 {
		return nil, agenterrors.ErrInvalidInput
	}

	if _, err := s.users.FindByEmail(ctx, req.Email); err == nil {
		return nil, agenterrors.ErrConflict
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	if _, err := s.users.FindByUsername(ctx, req.Username); err == nil {
		return nil, agenterrors.ErrConflict
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	passwordHash, err := s.passwords.Hash(req.Password)
	if err != nil {
		return nil, err
	}
	email := req.Email
	u := &user.User{
		Username:     req.Username,
		Email:        email,
		PasswordHash: passwordHash,
		LoginType:    user.LoginTypePassword,
		Status:       user.StatusActive,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	if err := s.audit(ctx, u.ID, u.ID, "auth.register", "user", strconv.FormatInt(u.ID, 10), nil, client); err != nil {
		return nil, err
	}
	tokens, err := s.issueTokens(ctx, u.ID, client)
	if err != nil {
		return nil, err
	}
	return &AuthResponse{User: toUserDTO(u), Tokens: *tokens}, nil
}

func (s *Service) Login(ctx context.Context, req LoginRequest, client ClientInfo) (*AuthResponse, error) {
	email := strings.TrimSpace(strings.ToLower(req.Email))
	u, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, agenterrors.ErrUnauthorized
		}
		return nil, err
	}
	if u.Status != user.StatusActive || u.PasswordHash == "" || !s.passwords.Verify(u.PasswordHash, req.Password) {
		return nil, agenterrors.ErrUnauthorized
	}
	now := time.Now().UTC()
	_ = s.users.UpdateLastLogin(ctx, u.ID, now)
	u.LastLoginAt = &now

	_ = s.audit(ctx, u.ID, u.ID, "auth.login", "user", strconv.FormatInt(u.ID, 10), nil, client)

	tokens, err := s.issueTokens(ctx, u.ID, client)
	if err != nil {
		return nil, err
	}
	return &AuthResponse{User: toUserDTO(u), Tokens: *tokens}, nil
}

func (s *Service) Refresh(ctx context.Context, req RefreshRequest, client ClientInfo) (*authdomain.TokenPair, error) {
	hash := s.tokenHasher.Hash(req.RefreshToken)
	session, err := s.sessions.FindActiveByRefreshHash(ctx, hash, time.Now().UTC())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, agenterrors.ErrUnauthorized
		}
		return nil, err
	}
	if err := s.sessions.RevokeByRefreshHash(ctx, hash, time.Now().UTC()); err != nil {
		return nil, err
	}
	return s.issueTokens(ctx, session.UserID, client)
}

func (s *Service) Logout(ctx context.Context, req LogoutRequest) error {
	hash := s.tokenHasher.Hash(req.RefreshToken)
	return s.sessions.RevokeByRefreshHash(ctx, hash, time.Now().UTC())
}

func (s *Service) Me(ctx context.Context, userID int64) (*UserDTO, error) {
	u, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, agenterrors.ErrNotFound
		}
		return nil, err
	}
	dto := toUserDTO(u)
	return &dto, nil
}

func (s *Service) BeginGitHubOAuth(ctx context.Context) (string, error) {
	if s.redis == nil || s.github == nil {
		return "", fmt.Errorf("github oauth is not configured")
	}
	state, err := authdomain.RandomURLToken(32)
	if err != nil {
		return "", err
	}
	if err := s.redis.Set(ctx, "oauth:github:state:"+state, "1", 10*time.Minute).Err(); err != nil {
		return "", err
	}
	return s.github.AuthCodeURL(state)
}

func (s *Service) HandleGitHubCallback(ctx context.Context, code, state string, client ClientInfo) (*AuthResponse, error) {
	if s.redis == nil || s.github == nil {
		return nil, fmt.Errorf("github oauth is not configured")
	}
	if code == "" || state == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	key := "oauth:github:state:" + state
	if err := s.redis.Get(ctx, key).Err(); err != nil {
		return nil, agenterrors.ErrUnauthorized
	}
	_ = s.redis.Del(ctx, key).Err()

	token, err := s.github.ExchangeCode(ctx, code)
	// 使用这个 token 可以查询 github 的个人信息
	if err != nil {
		return nil, err
	}
	ghUser, err := s.github.GetUser(ctx, token.AccessToken)
	if err != nil {
		return nil, err
	}
	providerUserID := strconv.FormatInt(ghUser.ID, 10)
	account, err := s.oauth.FindByProviderUserID(ctx, "github", providerUserID)
	var u *user.User
	if err == nil {
		u, err = s.users.FindByID(ctx, account.UserID)
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			if delErr := s.oauth.DeleteByProviderUserID(ctx, "github", providerUserID); delErr != nil {
				return nil, delErr
			}
			u, account, err = s.createGitHubUser(ctx, ghUser, providerUserID)
			if err != nil {
				return nil, err
			}
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		u, account, err = s.createGitHubUser(ctx, ghUser, providerUserID)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	now := time.Now().UTC()
	_ = s.users.UpdateLastLogin(ctx, u.ID, now)
	u.LastLoginAt = &now
	_ = account
	_ = s.audit(ctx, u.ID, u.ID, "auth.github_login", "user", strconv.FormatInt(u.ID, 10), map[string]any{"github_user_id": providerUserID}, client)
	tokens, err := s.issueTokens(ctx, u.ID, client)
	if err != nil {
		return nil, err
	}
	return &AuthResponse{User: toUserDTO(u), Tokens: *tokens}, nil
}

func (s *Service) HandleGitHubCallbackCode(ctx context.Context, code, state string, client ClientInfo) (string, error) {
	resp, err := s.HandleGitHubCallback(ctx, code, state, client)
	if err != nil {
		return "", err
	}
	loginCode, err := authdomain.RandomURLToken(32)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	if err := s.redis.Set(ctx, "oauth:login_code:"+loginCode, payload, 2*time.Minute).Err(); err != nil {
		return "", err
	}
	return loginCode, nil
}

func (s *Service) ExchangeOAuthCode(ctx context.Context, code string) (*AuthResponse, error) {
	if s.redis == nil {
		return nil, fmt.Errorf("oauth code store is not configured")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	key := "oauth:login_code:" + code
	data, err := s.redis.Get(ctx, key).Bytes()
	if err != nil {
		return nil, agenterrors.ErrUnauthorized
	}
	_ = s.redis.Del(ctx, key).Err()
	var resp AuthResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *Service) CreateAPIToken(ctx context.Context, ownerID int64, req CreateAPITokenRequest) (*authdomain.APITokenCreated, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	scopes := make([]string, 0, len(req.Scopes))
	seen := make(map[string]struct{}, len(req.Scopes))
	for _, raw := range req.Scopes {
		scope := strings.TrimSpace(raw)
		if scope == "" {
			continue
		}
		if _, ok := apiTokenScopes[scope]; !ok {
			return nil, fmt.Errorf("unsupported api token scope %q", scope)
		}
		if _, ok := seen[scope]; !ok {
			seen[scope] = struct{}{}
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		return nil, fmt.Errorf("at least one api token scope is required")
	}
	raw, err := authdomain.RandomURLToken(32)
	if err != nil {
		return nil, err
	}
	fullToken := "ac_" + raw
	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		return nil, err
	}
	token := &authdomain.APIToken{
		BaseModel:   domain.BaseModel{OwnerID: ownerID},
		Name:        name,
		TokenHash:   s.tokenHasher.Hash(fullToken),
		TokenPrefix: fullToken[:12],
		Scopes:      string(scopesJSON),
		ExpiresAt:   req.ExpiresAt,
	}
	if err := s.apiTokens.Create(ctx, token); err != nil {
		return nil, err
	}
	return &authdomain.APITokenCreated{
		ID:          token.ID,
		Name:        token.Name,
		Token:       fullToken,
		TokenPrefix: token.TokenPrefix,
		Scopes:      scopes,
		ExpiresAt:   token.ExpiresAt,
		CreatedAt:   token.CreatedAt,
	}, nil
}

func (s *Service) ListAPITokens(ctx context.Context, ownerID int64) ([]authdomain.APIToken, error) {
	return s.apiTokens.ListByOwner(ctx, ownerID)
}

func (s *Service) RevokeAPIToken(ctx context.Context, ownerID, id int64) error {
	return s.apiTokens.RevokeByID(ctx, ownerID, id, time.Now().UTC())
}

func (s *Service) VerifyAccessToken(raw string) (*authdomain.AccessTokenClaims, error) {
	return s.jwt.VerifyAccessToken(raw)
}

func (s *Service) HashToken(raw string) string {
	return s.tokenHasher.Hash(raw)
}

func (s *Service) createGitHubUser(ctx context.Context, ghUser *authdomain.GitHubUser, providerUserID string) (*user.User, *authdomain.OAuthAccount, error) {
	username := strings.TrimSpace(ghUser.Login)
	if username == "" {
		username = "github_" + providerUserID
	}
	if _, err := s.users.FindByUsername(ctx, username); err == nil {
		username = fmt.Sprintf("%s_%s", username, providerUserID)
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	if ghUser.Email != "" {
		email := strings.ToLower(ghUser.Email)
		if existing, err := s.users.FindByEmail(ctx, email); err == nil {
			account := &authdomain.OAuthAccount{
				UserID:         existing.ID,
				Provider:       "github",
				ProviderUserID: providerUserID,
			}
			if err := s.oauth.Create(ctx, account); err != nil {
				return nil, nil, err
			}
			return existing, account, nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, err
		}
	}
	u := &user.User{
		Username:  username,
		Email:     ghUser.Email,
		AvatarURL: ghUser.AvatarURL,
		LoginType: user.LoginTypeGithub,
		Status:    user.StatusActive,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, nil, err
	}
	account := &authdomain.OAuthAccount{
		UserID:         u.ID,
		Provider:       "github",
		ProviderUserID: providerUserID,
	}
	if err := s.oauth.Create(ctx, account); err != nil {
		return nil, nil, err
	}
	return u, account, nil
}

func (s *Service) issueTokens(ctx context.Context, userID int64, client ClientInfo) (*authdomain.TokenPair, error) {
	access, accessExpiresAt, err := s.jwt.IssueAccessToken(userID)
	if err != nil {
		return nil, err
	}
	refresh, err := authdomain.RandomURLToken(48)
	if err != nil {
		return nil, err
	}
	session := &authdomain.Session{
		UserID:           userID,
		RefreshTokenHash: s.tokenHasher.Hash(refresh),
		ExpiresAt:        time.Now().UTC().Add(s.refreshTTL),
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, err
	}
	return &authdomain.TokenPair{AccessToken: access,
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresAt:    accessExpiresAt,
	}, nil
}

func (s *Service) audit(ctx context.Context, ownerID, actorID int64, action, resourceType, resourceID string, detail map[string]any, client ClientInfo) error {
	return s.audits.Create(ctx, audit.NewLog(ownerID, actorID, action, resourceType, resourceID, detail, client.IPAddress, client.UserAgent))
}

func toUserDTO(u *user.User) UserDTO {
	var emailPtr *string
	if u.Email != "" {
		emailPtr = &u.Email
	}
	return UserDTO{
		ID:          u.ID,
		Username:    u.Username,
		Email:       emailPtr,
		AvatarURL:   u.AvatarURL,
		LoginType:   u.LoginType,
		Status:      u.Status,
		LastLoginAt: u.LastLoginAt,
		CreatedAt:   u.CreatedAt,
	}
}
