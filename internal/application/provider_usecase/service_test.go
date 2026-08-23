package provider_usecase

import (
	"agentcanvas/internal/domain"
	"context"
	"errors"
	"testing"

	providerdomain "agentcanvas/internal/domain/provider"
	"agentcanvas/internal/infrastructure/llm"
)

func TestProviderTestReturnsFailedStatusPersistenceError(t *testing.T) {
	testErr := errors.New("provider unavailable")
	updateErr := errors.New("provider update unavailable")
	repository := &providerTestRepository{
		item:      &providerdomain.ModelProvider{SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{ID: 2, OwnerID: 1}}, ProviderType: providerdomain.TypeOpenAICompatible, EncryptedAPIKey: "encrypted", DefaultChatModel: "model"},
		updateErr: updateErr,
	}
	service := NewService(repository, nil, providerTestSecrets{}, providerTester{err: testErr})

	item, err := service.Test(context.Background(), 1, 2, ClientInfo{})
	if item != nil || !errors.Is(err, testErr) || !errors.Is(err, updateErr) || repository.updateCalls != 1 {
		t.Fatalf("Test() item=%+v error=%v updates=%d, want both errors and one persistence attempt", item, err, repository.updateCalls)
	}
}

type providerTestRepository struct {
	item        *providerdomain.ModelProvider
	updateErr   error
	updateCalls int
}

func (r *providerTestRepository) Create(context.Context, *providerdomain.ModelProvider) error {
	return nil
}
func (r *providerTestRepository) ListByOwner(context.Context, int64) ([]providerdomain.ModelProvider, error) {
	return nil, nil
}
func (r *providerTestRepository) FindByID(context.Context, int64, int64) (*providerdomain.ModelProvider, error) {
	clone := *r.item
	return &clone, nil
}
func (r *providerTestRepository) Update(context.Context, *providerdomain.ModelProvider) error {
	r.updateCalls++
	return r.updateErr
}
func (r *providerTestRepository) SoftDelete(context.Context, int64, int64) error { return nil }

type providerTestSecrets struct{}

func (providerTestSecrets) Encrypt(value string) (string, error) { return value, nil }
func (providerTestSecrets) Decrypt(string) (string, error)       { return "api-key", nil }

type providerTester struct{ err error }

func (t providerTester) Test(context.Context, llm.ProviderTestConfig) error { return t.err }
