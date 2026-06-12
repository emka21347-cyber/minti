package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// connectBlob is the wire shape packed inside a connection token. The single
// opaque string a founder shares ("MINTI1-…") carries everything a joiner
// needs — invite token, the member's LAN address, and the cert pin — so the
// joiner pastes ONE thing instead of three separate fields.
type connectBlob struct {
	T string `json:"t"` // invite token
	A string `json:"a"` // LAN address (ip:port)
	P string `json:"p"` // cert pin (sha256:<hex>)
}

// connectPrefix versions the format so a future v2 (e.g. paste-key blobs) can
// coexist. Decoders reject anything without it.
const connectPrefix = "MINTI1-"

// encodeConnectBlob packs an invite into the single shareable connection token.
func encodeConnectBlob(token, address, pin string) string {
	raw, _ := json.Marshal(connectBlob{T: token, A: address, P: pin})
	return connectPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// decodeConnectBlob unpacks a connection token into token/address/pin. It
// tolerates surrounding whitespace (pasted blobs often pick up a trailing
// newline) and reports a clear error on a malformed or wrong-version string.
func decodeConnectBlob(s string) (token, address, pin string, err error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, connectPrefix) {
		return "", "", "", fmt.Errorf("not a connection token (expected %sprefix)", connectPrefix)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, connectPrefix))
	if err != nil {
		return "", "", "", fmt.Errorf("connection token: bad encoding: %w", err)
	}
	var b connectBlob
	if err := json.Unmarshal(raw, &b); err != nil {
		return "", "", "", fmt.Errorf("connection token: bad payload: %w", err)
	}
	if b.T == "" || b.A == "" || b.P == "" {
		return "", "", "", fmt.Errorf("connection token: missing token/address/pin")
	}
	return b.T, b.A, b.P, nil
}
