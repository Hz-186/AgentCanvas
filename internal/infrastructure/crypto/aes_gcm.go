package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const SecretAlgorithmAES256GCM = "AES-256-GCM"

type EncryptedSecret struct {
	Ciphertext string `json:"ciphertext"` // 加密之后的结果
	Nonce      string `json:"nonce"`      // 临时随机数
	KeyVersion string `json:"key_version"`
	Algorithm  string `json:"algorithm"`
}

type SecretBox struct {
	key        []byte
	keyVersion string
}

func NewSecretBox(secret string) (*SecretBox, error) {
	key, err := decodeKey(secret)
	if err != nil {
		return nil, err
	}
	return &SecretBox{key: key, keyVersion: "v1"}, nil
}

// -> 32
func decodeKey(secret string) ([]byte, error) {
	if secret == "" {
		return nil, fmt.Errorf("secret encrypt key is empty")
	}
	if decoded, err := base64.StdEncoding.DecodeString(secret); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len([]byte(secret)) == 32 {
		return []byte(secret), nil
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:], nil
}

func (b *SecretBox) Encrypt(plain string) (string, error) {
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plain), nil)
	payload := EncryptedSecret{
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		KeyVersion: b.keyVersion,
		Algorithm:  SecretAlgorithmAES256GCM,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil // 返回一个 json string
}

func (b *SecretBox) Decrypt(encoded string) (string, error) {
	if encoded == "" {
		return "", nil
	}
	var payload EncryptedSecret
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		return "", err
	}
	if payload.Algorithm != SecretAlgorithmAES256GCM {
		return "", fmt.Errorf("unsupported secret algorithm: %s", payload.Algorithm)
	}
	nonce, err := base64.StdEncoding.DecodeString(payload.Nonce)
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(payload.Ciphertext)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func MaskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return "****"
	}
	return secret[:4] + "****" + secret[len(secret)-4:]
}
