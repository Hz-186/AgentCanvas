package jsonutil

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func Hash(raw json.RawMessage) string {
	data := []byte(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) == nil {
		if canonical, err := json.Marshal(value); err == nil {
			data = canonical
		}
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
