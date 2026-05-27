package transport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/crypto"
)

// captureAudit records audit events in memory so tests can assert on rejects.
type captureAudit struct {
	mu     sync.Mutex
	events []auditlog.Event
}

func (c *captureAudit) Write(e auditlog.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *captureAudit) Snapshot() []auditlog.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]auditlog.Event, len(c.events))
	copy(out, c.events)
	return out
}

// newServerPair spins up an HTTPS Server on a random localhost port and
// returns it + a properly-pinned, properly-keyed Client. Both sides share
// the same Clan Key (the same SimpleKeyProvider instance).
func newServerPair(t *testing.T) (srv *Server, client *Client, audit *captureAudit, addr string, cleanup func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := crypto.GenerateClanCert(priv, time.Hour, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	kp, err := crypto.NewSimpleKeyProvider(key)
	if err != nil {
		t.Fatal(err)
	}
	audit = &captureAudit{}

	// Bind on :0 so the OS hands us a free port.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr = l.Addr().String()
	_ = l.Close()

	srv, err = NewServer(ServerOpts{
		ListenAddr:  addr,
		Cert:        cc,
		PrivateKey:  priv,
		KeyProvider: kp,
		NonceCache:  NewNonceCache(0, 0),
		Audit:       audit,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		if err := srv.Start(); err != nil {
			t.Logf("server stopped: %v", err)
		}
	}()
	if err := waitForPort(addr, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	client, err = NewClient(ClientOpts{
		MemberID:    "test-member",
		KeyProvider: kp,
		Pin:         cc.Pin,
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	cleanup = func() {
		_ = srv.Shutdown(t.Context())
	}
	return
}

func waitForPort(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("port %s never accepted within %v", addr, timeout)
}

// insecureHTTP returns an HTTP client that skips TLS verification — used by
// tests that construct raw, malformed requests against the server. Real
// production clients pin via VerifyPeerCertificate; that path is exercised
// by TestClient_RejectsBadPin.
func insecureHTTP() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec  // test helper only
				MinVersion:         tls.VersionTLS12,
			},
		},
	}
}

func insecurePost(addr, path, body string) (*http.Response, error) {
	req, err := http.NewRequest("POST", "https://"+addr+path, bytes.NewReader([]byte(body)))
	if err != nil {
		return nil, err
	}
	return insecureHTTP().Do(req)
}

func lastAuditReason(es []auditlog.Event) string {
	if len(es) == 0 {
		return ""
	}
	return es[len(es)-1].Reason
}

// ---------- happy path ----------

func TestServerClient_HappyPath(t *testing.T) {
	srv, client, _, addr, cleanup := newServerPair(t)
	defer cleanup()

	got := make(chan string, 1)
	srv.Handle("POST /clan/ping", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- OriginMember(r.Context()) + ":" + string(body)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	resp, err := client.Post("https://"+addr+"/clan/ping", "application/json", []byte(`{"hi":1}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var env map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if !env["ok"] {
		t.Errorf("response body wrong: %+v", env)
	}

	select {
	case s := <-got:
		want := `test-member:{"hi":1}`
		if s != want {
			t.Errorf("handler saw %q, want %q", s, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never fired")
	}
}

// ---------- auth-middleware reject paths ----------

func TestServer_RejectsMissingHeaders(t *testing.T) {
	srv, _, audit, addr, cleanup := newServerPair(t)
	defer cleanup()

	srv.Handle("POST /clan/ping", func(http.ResponseWriter, *http.Request) {
		t.Errorf("handler should NOT have been called")
	})

	resp, err := insecurePost(addr, "/clan/ping", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Errorf("401 should have empty body, got %q", body)
	}
	if r := lastAuditReason(audit.Snapshot()); r != "missing_headers" {
		t.Errorf("audit reason = %q, want missing_headers", r)
	}
}

func TestServer_RejectsTimestampSkew(t *testing.T) {
	srv, _, audit, addr, cleanup := newServerPair(t)
	defer cleanup()
	srv.Handle("POST /clan/ping", func(http.ResponseWriter, *http.Request) {
		t.Errorf("handler must not fire on skew reject")
	})

	key := srv.opts.KeyProvider.Current()
	body := []byte(`{}`)
	tsBad := time.Now().Add(-3 * time.Minute).UnixMilli()
	nonce, _ := crypto.NewNonce()
	mac := crypto.ComputeMAC(key, "POST", "/clan/ping", body, tsBad, nonce)

	req, _ := http.NewRequest("POST", "https://"+addr+"/clan/ping", bytes.NewReader(body))
	req.Header.Set(crypto.HeaderMember, "skew-member")
	req.Header.Set(crypto.HeaderTimestamp, fmt.Sprintf("%d", tsBad))
	req.Header.Set(crypto.HeaderNonce, nonce)
	req.Header.Set(crypto.HeaderHMAC, mac)
	resp, err := insecureHTTP().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if r := lastAuditReason(audit.Snapshot()); r != "timestamp_skew" {
		t.Errorf("audit reason = %q, want timestamp_skew", r)
	}
}

func TestServer_RejectsBadTimestampHeader(t *testing.T) {
	srv, _, audit, addr, cleanup := newServerPair(t)
	defer cleanup()
	srv.Handle("POST /clan/ping", func(http.ResponseWriter, *http.Request) {
		t.Errorf("handler must not fire")
	})

	body := []byte(`{}`)
	nonce, _ := crypto.NewNonce()
	req, _ := http.NewRequest("POST", "https://"+addr+"/clan/ping", bytes.NewReader(body))
	req.Header.Set(crypto.HeaderMember, "m")
	req.Header.Set(crypto.HeaderTimestamp, "notanint")
	req.Header.Set(crypto.HeaderNonce, nonce)
	req.Header.Set(crypto.HeaderHMAC, "00")
	resp, err := insecureHTTP().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if r := lastAuditReason(audit.Snapshot()); r != "bad_timestamp" {
		t.Errorf("audit reason = %q", r)
	}
}

func TestServer_RejectsWrongKey(t *testing.T) {
	srv, _, audit, addr, cleanup := newServerPair(t)
	defer cleanup()
	srv.Handle("POST /clan/ping", func(http.ResponseWriter, *http.Request) {
		t.Errorf("handler must not fire")
	})

	wrongKey := make([]byte, 32)
	_, _ = rand.Read(wrongKey)
	wrongKP, _ := crypto.NewSimpleKeyProvider(wrongKey)
	cli, _ := NewClient(ClientOpts{
		MemberID:    "wrong-key-member",
		KeyProvider: wrongKP,
		Pin:         srv.opts.Cert.Pin,
		Timeout:     5 * time.Second,
	})
	resp, err := cli.Post("https://"+addr+"/clan/ping", "application/json", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if r := lastAuditReason(audit.Snapshot()); r != "hmac_mismatch" {
		t.Errorf("audit reason = %q", r)
	}
}

func TestServer_AcceptsGraceKey(t *testing.T) {
	// Build a server with KeyProvider srvKP.
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	cc, _ := crypto.GenerateClanCert(priv, time.Hour, nil, nil)
	oldKey := make([]byte, 32)
	_, _ = rand.Read(oldKey)
	srvKP, _ := crypto.NewSimpleKeyProvider(oldKey)

	audit := &captureAudit{}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_ = l.Close()

	srv, err := NewServer(ServerOpts{
		ListenAddr:  addr,
		Cert:        cc,
		PrivateKey:  priv,
		KeyProvider: srvKP,
		NonceCache:  NewNonceCache(0, 0),
		Audit:       audit,
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.Handle("POST /clan/ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	go func() { _ = srv.Start() }()
	defer srv.Shutdown(t.Context())
	if err := waitForPort(addr, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	// Client uses an independent provider still holding the OLD key.
	cliKP, _ := crypto.NewSimpleKeyProvider(oldKey)
	cli, _ := NewClient(ClientOpts{
		MemberID:    "grace-test",
		KeyProvider: cliKP,
		Pin:         cc.Pin,
		Timeout:     5 * time.Second,
	})

	// Server rotates: oldKey demoted to grace, srvKP now holds newKey.
	newKey := make([]byte, 32)
	_, _ = rand.Read(newKey)
	_ = srvKP.Rotate(newKey, 5*time.Minute)

	resp, err := cli.Post("https://"+addr+"/clan/ping", "", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("grace key should still verify during window; got status %d", resp.StatusCode)
	}
}

func TestServer_RejectsReplay(t *testing.T) {
	srv, _, audit, addr, cleanup := newServerPair(t)
	defer cleanup()
	srv.Handle("POST /clan/ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	key := srv.opts.KeyProvider.Current()
	body := []byte(`{}`)
	ts := time.Now().UnixMilli()
	nonce, _ := crypto.NewNonce()
	mac := crypto.ComputeMAC(key, "POST", "/clan/ping", body, ts, nonce)

	for attempt := 1; attempt <= 2; attempt++ {
		req, _ := http.NewRequest("POST", "https://"+addr+"/clan/ping", bytes.NewReader(body))
		req.Header.Set(crypto.HeaderMember, "replay-member")
		req.Header.Set(crypto.HeaderTimestamp, fmt.Sprintf("%d", ts))
		req.Header.Set(crypto.HeaderNonce, nonce)
		req.Header.Set(crypto.HeaderHMAC, mac)
		resp, err := insecureHTTP().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case attempt == 1 && resp.StatusCode != http.StatusOK:
			t.Errorf("first attempt status = %d", resp.StatusCode)
		case attempt == 2 && resp.StatusCode != http.StatusUnauthorized:
			t.Errorf("replay status = %d, want 401", resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	if r := lastAuditReason(audit.Snapshot()); r != "replay" {
		t.Errorf("expected last audit reason 'replay', got %q", r)
	}
}

// ---------- client-side pin enforcement ----------

func TestClient_RejectsBadPin(t *testing.T) {
	srv, _, _, addr, cleanup := newServerPair(t)
	defer cleanup()
	srv.Handle("POST /clan/ping", func(http.ResponseWriter, *http.Request) {})

	wrongClient, err := NewClient(ClientOpts{
		MemberID:    "bad-pin",
		KeyProvider: srv.opts.KeyProvider,
		Pin:         "sha256:" + "00000000000000000000000000000000",
		Timeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = wrongClient.Post("https://"+addr+"/clan/ping", "", []byte("x"))
	if err == nil {
		t.Errorf("expected TLS pin failure error")
	}
}

// ---------- anonymous handlers bypass auth ----------

func TestHandleAnonymous_BypassesAuth(t *testing.T) {
	srv, _, _, addr, cleanup := newServerPair(t)
	defer cleanup()
	srv.HandleAnonymous("POST /clan/join", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("welcome"))
	})

	resp, err := insecurePost(addr, "/clan/join", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("anon handler should accept unauth request; got status %d", resp.StatusCode)
	}
}
