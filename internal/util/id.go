package util

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

func NewID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return base64.RawURLEncoding.EncodeToString([]byte(time.Now().Format("150405.000")))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

