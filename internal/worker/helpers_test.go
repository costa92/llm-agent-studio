package worker

import (
	"crypto/rand"
	"encoding/hex"
)

func randHex3() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
