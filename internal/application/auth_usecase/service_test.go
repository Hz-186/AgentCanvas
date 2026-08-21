package auth_usecase

import (
	"context"
	"testing"
	"time"

	authdomain "agentcanvas/internal/domain/auth"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"

	"gorm.io/gorm"
)

type apiTokenTestRepository struct {
	created *authdomain.APIToken
	active  *authdomain.APIToken
}

func (r *apiTokenTestRepository) Create(_ context.Context, token *authdomain.APIToken) error {
	token.ID = 1
	token.CreatedAt = time.Now().UTC()
	clone := *token
	r.created = &clone
	return nil
}
func (*apiTokenTestRepository) ListByOwner(context.Context, int64) ([]authdomain.APIToken, error) {
	return nil, nil
}
func (r *apiTokenTestRepository) FindActiveByHash(context.Context, string, time.Time) (*authdomain.APIToken, error) {
	if r.active == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *r.active
	return &clone, nil
}
func (*apiTokenTestRepository) RevokeByID(context.Context, int64, int64, time.Time) error { return nil }

func TestCreateAPITokenRejectsEmptyScopes(t *testing.T) {
	repository := &apiTokenTestRepository{}
	service := NewService(nil, nil, nil, repository, nil, nil, nil, cryptoinfra.NewTokenHasher("test"), nil, nil, time.Hour)
	for _, scopes := range [][]string{nil, {}, {"", " "}} {
		if _, err := service.CreateAPIToken(context.Background(), 1, CreateAPITokenRequest{Name: "token", Scopes: scopes}); err == nil {
			t.Fatalf("CreateAPIToken(scopes=%q) error = nil", scopes)
		}
	}
	if repository.created != nil {
		t.Fatalf("invalid token was persisted: %+v", repository.created)
	}
}

func TestCreateAPITokenPersistsValidatedUniqueScopes(t *testing.T) {
	repository := &apiTokenTestRepository{}
	service := NewService(nil, nil, nil, repository, nil, nil, nil, cryptoinfra.NewTokenHasher("test"), nil, nil, time.Hour)
	created, err := service.CreateAPIToken(context.Background(), 1, CreateAPITokenRequest{Name: "token", Scopes: []string{" agent:read ", "agent:read", "run:write"}})
	if err != nil {
		t.Fatalf("CreateAPIToken() error = %v", err)
	}
	if len(created.Scopes) != 2 || created.Scopes[0] != "agent:read" || created.Scopes[1] != "run:write" {
		t.Fatalf("created scopes = %+v", created.Scopes)
	}
	if repository.created == nil || repository.created.Scopes != `["agent:read","run:write"]` {
		t.Fatalf("persisted token = %+v", repository.created)
	}
}
