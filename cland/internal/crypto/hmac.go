package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Header names per docs/clan-protocol.md §2.3. Exported so transport handlers
// and clients agree on the wire format.
const (
	HeaderMember    = "X-Minti-Member"
	HeaderTimestamp = "X-Minti-Timestamp"
	HeaderNonce     = "X-Minti-Nonce"
	HeaderHMAC      = "X-Minti-HMAC"
)

// Canonical builds the byte string fed into HMAC-SHA256 per spec §2.3.
// Format (one field per line, LF-separated):
//
//	<METHOD>
//	<PATH>
//	<sha256(body) hex>
//	<timestampMillis>
//	<nonce>
//
// Body is hashed first so the canonical string stays bounded regardless of
// request size. Receivers reconstruct the same string from the wire headers
// and the (re-read) body.
func Canonical(method, path string, body []byte, timestampMillis int64, nonce string) []byte {
	bodyHash := sha256.Sum256(body)
	method = strings.ToUpper(method)
	canonical := fmt.Sprintf(
		"%s\n%s\n%s\n%d\n%s",
		method,
		path,
		hex.EncodeToString(bodyHash[:]),
		timestampMillis,
		nonce,
	)
	return []byte(canonical)
}

// ComputeMAC returns the lowercase hex HMAC-SHA256 over the canonical form.
func ComputeMAC(key []byte, method, path string, body []byte, timestampMillis int64, nonce string) string {
	canonical := Canonical(method, path, body, timestampMillis, nonce)
	mac := hmac.New(sha256.New, key)
	mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyMAC returns true iff the provided hex HMAC matches the recomputed
// HMAC under `key`. Uses hmac.Equal (constant-time compare).
func VerifyMAC(key []byte, method, path string, body []byte, timestampMillis int64, nonce, providedHex string) bool {
	expectedHex := ComputeMAC(key, method, path, body, timestampMillis, nonce)
	// Equal-length compare via constant-time path. hex.DecodeString is
	// permissive; do the comparison over bytes to avoid case differences.
	a, err := hex.DecodeString(strings.ToLower(providedHex))
	if err != nil {
		return false
	}
	b, _ := hex.DecodeString(expectedHex)
	return hmac.Equal(a, b)
}

// NewNonce returns a fresh 16-byte hex nonce (32 hex chars) — what clients
// stamp on every outbound request.
func NewNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
