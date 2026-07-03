package identityhash

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func Short(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
