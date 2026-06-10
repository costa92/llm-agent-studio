package todos

import (
	"crypto/rand"
	"encoding/hex"
)

func time2hex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
