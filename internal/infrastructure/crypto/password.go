package crypto

import "golang.org/x/crypto/bcrypt"

type PasswordHasher struct {
	Cost int
}

func NewPasswordHasher(cost int) *PasswordHasher {
	return &PasswordHasher{Cost: bcrypt.DefaultCost}
}

func (h *PasswordHasher) Hash(password string) (string, error) {
	data, err := bcrypt.GenerateFromPassword([]byte(password), h.Cost)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (h *PasswordHasher) Verify(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
