package planner

import (
	"crypto/rand"
	"encoding/hex"
)

func randHex2() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
