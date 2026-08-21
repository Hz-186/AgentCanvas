package auth

import (
	"encoding/base64"
	"testing"
)

func TestRandomURLTokenLength(t *testing.T) {
	token, err := RandomURLToken(32)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("token=%q decoded_bytes=%d err=%v", token, len(decoded), err)
	}
}
