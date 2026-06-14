package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type JWTService struct {
	secret []byte
	ttl    time.Duration
}

type JWTClaims struct {
	UserID    int64     `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

type jwtPayload struct {
	Sub string `json:"sub"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Typ string `json:"typ"`
}

func NewJWTService(secret string, ttl time.Duration) *JWTService {
	return &JWTService{secret: []byte(secret), ttl: ttl}
}

func (s *JWTService) IssueAccessToken(userID int64) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(s.ttl)
	header := jwtHeader{Alg: "HS256", Typ: "JWT"}
	payload := jwtPayload{Sub: strconv.FormatInt(userID, 10), Iat: now.Unix(), Exp: expiresAt.Unix(), Typ: "access"}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", time.Time{}, err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", time.Time{}, err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)
	sig := s.sign(unsigned)
	return unsigned + "." + sig, expiresAt, nil
}

func (s *JWTService) VerifyAccessToken(token string) (*JWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token")
	}
	unsigned := parts[0] + "." + parts[1]
	expected := s.sign(unsigned)
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, fmt.Errorf("invalid token signature")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var payload jwtPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, err
	}
	if payload.Typ != "access" {
		return nil, fmt.Errorf("invalid token type")
	}
	if time.Now().UTC().Unix() >= payload.Exp {
		return nil, fmt.Errorf("token expired")
	}
	userID, err := strconv.ParseInt(payload.Sub, 10, 64)
	if err != nil {
		return nil, err
	}
	return &JWTClaims{UserID: userID, ExpiresAt: time.Unix(payload.Exp, 0).UTC()}, nil
}

func (s *JWTService) sign(unsigned string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(unsigned))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
