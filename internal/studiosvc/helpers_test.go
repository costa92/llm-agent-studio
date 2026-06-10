package studiosvc

import (
	"crypto/rand"
	"encoding/hex"
)

func randHexSvc() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
