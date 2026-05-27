package transport

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/minti/cland/internal/crypto"
)

// NewPinnedHTTPClient returns a plain *http.Client that pins the server's
// certificate by SPKI hash but does NOT attach HMAC auth headers. Used for
// the spec §3.2 /clan/join bootstrap where the joiner has presented a valid
// invite token but does NOT yet have clan_key — the token IS the auth, the
// pin establishes that the joiner is talking to the right server.
//
// Production callers should switch to NewClient (HMAC-signed) immediately
// after the bootstrap response delivers clan_key.
func NewPinnedHTTPClient(pin string, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify:    true, //nolint:gosec  // pinned via VerifyPeerCertificate
				VerifyPeerCertificate: crypto.VerifyServerCertPin(pin),
				MinVersion:            tls.VersionTLS12,
			},
		},
	}
}
