package user

import (
	"context"
	"time"
)

type Repository interface {
	Create(ctx context.Context, u *User) error
	FindByID(ctx context.Context, id int64) (*User, error)
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByUsername(ctx context.Context, username string) (*User, error)
	UpdateLastLogin(ctx context.Context, id int64, t time.Time) error
}
