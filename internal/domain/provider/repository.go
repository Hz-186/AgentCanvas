package provider

import "context"

type Repository interface {
	Create(ctx context.Context, p *ModelProvider) error
	ListByOwner(ctx context.Context, ownerID int64) ([]ModelProvider, error)
	FindByID(ctx context.Context, ownerID, id int64) (*ModelProvider, error)
	Update(ctx context.Context, p *ModelProvider) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
}

type SecretCodec interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}
