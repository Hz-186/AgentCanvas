package crypto

import "golang.org/x/crypto/bcrypt"

type PasswordHasher struct {
	Cost int
}

func NewPasswordHasher(cost int) *PasswordHasher {
	if cost <= 0 {
		cost = bcrypt.DefaultCost
	}
	return &PasswordHasher{Cost: cost}
}

func (h *PasswordHasher) Hash(password string) (string, error) {
	data, err := bcrypt.GenerateFromPassword([]byte(password), h.Cost)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (h *PasswordHasher) Verify(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
