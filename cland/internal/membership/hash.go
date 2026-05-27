package membership

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// shortHashHex returns the sha256 hex of the base64-decoded input. If
// decoding fails, falls back to hashing the raw string — gives a stable
// identifier regardless of input shape.
func shortHashHex(b64 string) string {
	if raw, err := base64.StdEncoding.DecodeString(b64); err == nil {
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256([]byte(b64))
	return hex.EncodeToString(sum[:])
}
