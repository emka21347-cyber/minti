package transport

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/minti/cland/internal/crypto"
)

// ClientOpts is the dependency bundle for NewClient. Pin must be the
// "sha256:<hex>" value the candidate captured at join (or the founder's own
// cert pin if calling self).
type ClientOpts struct {
	MemberID    string
	KeyProvider crypto.KeyProvider
	Pin         string
	Timeout     time.Duration
}

// Client makes HMAC-stamped HTTPS calls to peer cland endpoints.
//
// Cert pinning is done via tls.Config.VerifyPeerCertificate — InsecureSkipVerify
// is set to true so the stdlib chain-validator doesn't reject our self-signed
// cert; we replace it with our SPKI pin check (which is the actual security
// boundary).
type Client struct {
	opts ClientOpts
	http *http.Client
}

func NewClient(opts ClientOpts) (*Client, error) {
	if opts.MemberID == "" {
		return nil, errors.New("transport.NewClient: MemberID required")
	}
	if opts.KeyProvider == nil {
		return nil, errors.New("transport.NewClient: KeyProvider required")
	}
	if opts.Pin == "" {
		return nil, errors.New("transport.NewClient: Pin required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}

	tlsCfg := &tls.Config{
		InsecureSkipVerify:    true, //nolint:gosec  // we pin instead — see VerifyPeerCertificate below
		VerifyPeerCertificate: crypto.VerifyServerCertPin(opts.Pin),
		MinVersion:            tls.VersionTLS12,
	}
	return &Client{
		opts: opts,
		http: &http.Client{
			Timeout:   opts.Timeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}, nil
}

// Do stamps the request with the four X-Minti-* auth headers and sends it.
// The body is buffered into memory because we need to hash it for the HMAC
// and re-supply it to the underlying http.Request. Streaming bodies > a few
// MiB belong in a different code path.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("transport.Client: read body: %w", err)
		}
		_ = req.Body.Close()
		body = b
	}

	nonce, err := crypto.NewNonce()
	if err != nil {
		return nil, fmt.Errorf("transport.Client: nonce: %w", err)
	}
	tsMillis := time.Now().UnixMilli()
	mac := crypto.ComputeMAC(c.opts.KeyProvider.Current(), req.Method, req.URL.Path, body, tsMillis, nonce)

	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	req.Header.Set(crypto.HeaderMember, c.opts.MemberID)
	req.Header.Set(crypto.HeaderTimestamp, fmt.Sprintf("%d", tsMillis))
	req.Header.Set(crypto.HeaderNonce, nonce)
	req.Header.Set(crypto.HeaderHMAC, mac)

	return c.http.Do(req)
}

// Post is a convenience wrapper for POST + JSON body. The body must be the
// raw bytes to send (already JSON-encoded if applicable).
func (c *Client) Post(url, contentType string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.Do(req)
}
