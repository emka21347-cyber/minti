package membership

// Knock flow — spec §3.4. Three joining paths exist (invite token §3.2,
// paste-key §3.3, knock §3.4). This file covers §3.4 end-to-end:
// wire types, receiver-side KnockStore, ECDH/HKDF/AES-GCM crypto, and
// the Service methods that the HTTP handlers and CLI call into.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"

	"github.com/minti/cland/internal/auditlog"
	"github.com/minti/cland/internal/state"
)

// ─── Wire types ──────────────────────────────────────────────────────────────

// KnockRequest is POSTed by the joiner to the receiver's /clan/knock (anonymous).
type KnockRequest struct {
	ClanID             string `json:"clan_id"`
	JoinerMemberID     string `json:"joiner_member_id"`
	JoinerPubKeyB64    string `json:"joiner_pubkey_b64"`    // Ed25519 identity key
	JoinerX25519PubB64 string `json:"joiner_x25519_pub_b64"` // ephemeral ECDH key
	JoinerLANAddress   string `json:"joiner_lan_address"`    // where receiver delivers
}

// KnockResponse is returned synchronously to the joiner by the receiver.
type KnockResponse struct {
	KnockID              string `json:"knock_id"`                // hex 16 bytes
	ReceiverX25519PubB64 string `json:"receiver_x25519_pub_b64"` // ephemeral ECDH key
	ClanCertPin          string `json:"clan_cert_pin"`
	ReceiverLANAddress   string `json:"receiver_lan_address"`
}

// KnockDeliverRequest is POSTed by the receiver to joiner_lan_address/clan/knock-deliver.
// On accept: CiphertextB64 is set.  On deny: Denied is true.
type KnockDeliverRequest struct {
	KnockID       string `json:"knock_id"`
	CiphertextB64 string `json:"ciphertext_b64,omitempty"`
	Denied        bool   `json:"denied,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// KnockAcceptRequest — operator's POST /clan/knock-accept body (HMAC-gated).
type KnockAcceptRequest struct {
	KnockID string `json:"knock_id"`
}

// KnockDenyRequest — operator's POST /clan/knock-deny body (HMAC-gated).
type KnockDenyRequest struct {
	KnockID string `json:"knock_id"`
	Reason  string `json:"reason,omitempty"`
}

// KnockWelcome is the plaintext sealed inside the AES-256-GCM ciphertext.
// Mirrors JoinResponse but delivered via the knock channel.
type KnockWelcome struct {
	ClanID             string               `json:"clan_id"`
	ClanKeyB64         string               `json:"clan_key_b64"`
	ClanCertPEM        string               `json:"clan_cert_pem"`
	ClanCertPrivKeyB64 string               `json:"clan_cert_priv_key_b64"`
	Roster             []state.RosterMember `json:"roster"`
}

// PendingKnock is held on the receiver side until the operator decides.
type PendingKnock struct {
	KnockID                string    `json:"knock_id"`
	ClanID                 string    `json:"clan_id"`
	JoinerMemberID         string    `json:"joiner_member_id"`
	JoinerPubKeyB64        string    `json:"joiner_pubkey_b64"`
	JoinerX25519PubBytes   []byte    `json:"-"`
	ReceiverX25519PrivBytes []byte   `json:"-"` // held for SealKnockWelcome
	ReceiverX25519PubBytes []byte    `json:"-"`
	DerivedKey             []byte    `json:"-"`
	DerivedNonce           []byte    `json:"-"`
	KnockIDBytes           []byte    `json:"-"` // raw bytes for AEAD aad
	SAS                    string    `json:"sas"`
	JoinerLANAddress       string    `json:"joiner_lan_address"`
	ExpiresAt              time.Time `json:"expires_at"`
	State                  string    `json:"state"` // pending | accepted | denied
}

// ─── Sentinel errors ─────────────────────────────────────────────────────────

var (
	ErrKnockNotFound   = errors.New("knock: not found or already consumed")
	ErrKnockExpired    = errors.New("knock: expired")
	ErrKnockNotPending = errors.New("knock: already accepted or denied")
	ErrKnockRateLimit  = errors.New("knock: rate limit exceeded")
	ErrKnockClanID     = errors.New("knock: clan_id mismatch")
)

// ─── KnockStore (receiver side) ──────────────────────────────────────────────

const (
	KnockTTL    = 5 * time.Minute
	KnockTTLMin = 60 * time.Second
	KnockTTLMax = 15 * time.Minute

	knockRatePerSrc  = 10
	knockRatePerClan = 30
	knockRateWindow  = 60 * time.Second
)

// KnockStore holds pending knocks on the receiver side. Entries are lost
// on daemon restart (same design rationale as InviteStore).
type KnockStore struct {
	mu     sync.Mutex
	knocks map[string]*PendingKnock
	rlSrc  map[string]*rlBucket // source IP → bucket
	rlClan map[string]*rlBucket // clan_id → bucket
}

type rlBucket struct {
	count     int
	windowEnd time.Time
}

func (b *rlBucket) allow(limit int, window time.Duration, now time.Time) bool {
	if now.After(b.windowEnd) {
		b.count = 0
		b.windowEnd = now.Add(window)
	}
	if b.count >= limit {
		return false
	}
	b.count++
	return true
}

func NewKnockStore() *KnockStore {
	return &KnockStore{
		knocks: make(map[string]*PendingKnock),
		rlSrc:  make(map[string]*rlBucket),
		rlClan: make(map[string]*rlBucket),
	}
}

// Store records a pending knock after rate-limit checks.
func (s *KnockStore) Store(pk *PendingKnock, sourceIP string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if _, ok := s.rlSrc[sourceIP]; !ok {
		s.rlSrc[sourceIP] = &rlBucket{}
	}
	if !s.rlSrc[sourceIP].allow(knockRatePerSrc, knockRateWindow, now) {
		return fmt.Errorf("%w: too many knocks from %s", ErrKnockRateLimit, sourceIP)
	}
	if _, ok := s.rlClan[pk.ClanID]; !ok {
		s.rlClan[pk.ClanID] = &rlBucket{}
	}
	if !s.rlClan[pk.ClanID].allow(knockRatePerClan, knockRateWindow, now) {
		return fmt.Errorf("%w: too many knocks targeting this clan", ErrKnockRateLimit)
	}
	s.knocks[pk.KnockID] = pk
	return nil
}

// List returns all non-expired pending knocks.
func (s *KnockStore) List() []*PendingKnock {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	out := make([]*PendingKnock, 0, len(s.knocks))
	for _, pk := range s.knocks {
		if !now.After(pk.ExpiresAt) && pk.State == "pending" {
			out = append(out, pk)
		}
	}
	return out
}

// Consume transitions a knock from "pending" to targetState (CAS).
// Mirrors InviteStore.Redeem's single-use semantics (spec §3.4 §3.2 parity).
func (s *KnockStore) Consume(knockID, targetState string) (*PendingKnock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pk, ok := s.knocks[knockID]
	if !ok {
		return nil, ErrKnockNotFound
	}
	if time.Now().UTC().After(pk.ExpiresAt) {
		delete(s.knocks, knockID)
		return nil, ErrKnockExpired
	}
	if pk.State != "pending" {
		return nil, ErrKnockNotPending
	}
	pk.State = targetState
	return pk, nil
}

// Sweep removes expired and terminal-state entries.
func (s *KnockStore) Sweep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	purged := 0
	for id, pk := range s.knocks {
		if now.After(pk.ExpiresAt) || pk.State != "pending" {
			delete(s.knocks, id)
			purged++
		}
	}
	return purged
}

// Size — diagnostic.
func (s *KnockStore) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.knocks)
}

// ─── Crypto ──────────────────────────────────────────────────────────────────

// GenerateX25519 returns a fresh ephemeral keypair.
func GenerateX25519() (priv, pub []byte, err error) {
	priv = make([]byte, 32)
	if _, err = rand.Read(priv); err != nil {
		return nil, nil, fmt.Errorf("knock: rand X25519 priv: %w", err)
	}
	pub, err = curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, nil, fmt.Errorf("knock: derive X25519 pub: %w", err)
	}
	return priv, pub, nil
}

// DeriveKnockBundle runs the spec §3.4 key derivation:
//
//	shared    = X25519(myPriv, peerPub)
//	info      = clan_id || knock_id || joiner_pub || receiver_pub
//	kdf       = HKDF-SHA256(IKM=shared, salt="minti-knock-v1", info)
//	key       = kdf[0:32]    AES-256-GCM key
//	nonce     = kdf[32:44]   12-byte AES-GCM nonce
//	code_bytes = kdf[32:36]  first 4 bytes of nonce, also the SAS source
//
// Returns key, nonce, and the formatted SAS "XXXX-XXXXX".
func DeriveKnockBundle(myPriv, peerPub []byte, clanID string, knockIDBytes, joinerPub, receiverPub []byte) (key, nonce []byte, sas string, err error) {
	shared, err := curve25519.X25519(myPriv, peerPub)
	if err != nil {
		return nil, nil, "", fmt.Errorf("knock: X25519: %w", err)
	}
	info := append([]byte(clanID), knockIDBytes...)
	info = append(info, joinerPub...)
	info = append(info, receiverPub...)

	r := hkdf.New(sha256.New, shared, []byte("minti-knock-v1"), info)
	bundle := make([]byte, 48)
	if _, err := io.ReadFull(r, bundle); err != nil {
		return nil, nil, "", fmt.Errorf("knock: HKDF: %w", err)
	}

	key = bundle[0:32]
	nonce = bundle[32:44]
	// 30-bit SAS from bundle[32:36] (overlaps start of nonce — intentional per spec)
	sasInt := binary.BigEndian.Uint32(bundle[32:36]) & 0x3FFFFFFF
	sasN := sasInt % 1_000_000_000
	s := fmt.Sprintf("%09d", sasN)
	sas = s[:4] + "-" + s[4:]
	return key, nonce, sas, nil
}

// SealKnockWelcome encrypts w under key/nonce with aad=knockIDBytes.
func SealKnockWelcome(key, nonce, knockIDBytes []byte, w KnockWelcome) ([]byte, error) {
	pt, err := json.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("knock: marshal welcome: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("knock: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("knock: gcm: %w", err)
	}
	return gcm.Seal(nil, nonce, pt, knockIDBytes), nil
}

// OpenKnockWelcome decrypts a ciphertext produced by SealKnockWelcome.
// The GCM tag is the receiver-authenticity proof (spec §3.4).
func OpenKnockWelcome(key, nonce, knockIDBytes, ciphertext []byte) (*KnockWelcome, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, nonce, ciphertext, knockIDBytes)
	if err != nil {
		return nil, fmt.Errorf("knock: AES-GCM open failed (MITM or wrong key): %w", err)
	}
	var w KnockWelcome
	if err := json.Unmarshal(pt, &w); err != nil {
		return nil, fmt.Errorf("knock: unmarshal welcome: %w", err)
	}
	return &w, nil
}

// randKnockID returns (hex string, raw bytes) for a 16-byte random knock_id.
func randKnockID() (string, []byte, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(b), b, nil
}

// DecodeKnockID decodes a hex knock_id string to raw bytes, enforcing 16 bytes.
func DecodeKnockID(knockID string) ([]byte, error) {
	b, err := hex.DecodeString(knockID)
	if err != nil || len(b) != 16 {
		return nil, fmt.Errorf("knock: invalid knock_id %q", knockID)
	}
	return b, nil
}

// decodeKnockID is the package-internal alias used inside knock.go service methods.
func decodeKnockID(knockID string) ([]byte, error) { return DecodeKnockID(knockID) }

// ─── Service methods ─────────────────────────────────────────────────────────

// Knock handles an incoming knock on the receiver side (POST /clan/knock).
// sourceIP is extracted from the request by the handler (rate limiting).
func (s *Service) Knock(req KnockRequest, sourceIP string) (*KnockResponse, error) {
	if req.ClanID == "" || req.JoinerMemberID == "" || req.JoinerPubKeyB64 == "" ||
		req.JoinerX25519PubB64 == "" || req.JoinerLANAddress == "" {
		return nil, errors.New("clan_id, joiner_member_id, joiner_pubkey_b64, joiner_x25519_pub_b64, joiner_lan_address required")
	}

	clan, err := s.store.LoadClan()
	if err != nil {
		return nil, err
	}
	if !clan.IsActive() {
		return nil, errors.New("receiver is not in a Clan")
	}
	if clan.ClanID != req.ClanID {
		return nil, fmt.Errorf("%w: got %s want %s", ErrKnockClanID, req.ClanID, clan.ClanID)
	}

	joinerX25519Pub, err := base64.StdEncoding.DecodeString(req.JoinerX25519PubB64)
	if err != nil || len(joinerX25519Pub) != 32 {
		return nil, errors.New("knock: invalid joiner_x25519_pub_b64")
	}

	recvPriv, recvPub, err := GenerateX25519()
	if err != nil {
		return nil, err
	}

	knockIDHex, knockIDRaw, err := randKnockID()
	if err != nil {
		return nil, fmt.Errorf("knock: rand knock_id: %w", err)
	}

	key, nonce, sas, err := DeriveKnockBundle(recvPriv, joinerX25519Pub, req.ClanID, knockIDRaw, joinerX25519Pub, recvPub)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	pk := &PendingKnock{
		KnockID:                knockIDHex,
		ClanID:                 req.ClanID,
		JoinerMemberID:         req.JoinerMemberID,
		JoinerPubKeyB64:        req.JoinerPubKeyB64,
		JoinerX25519PubBytes:   joinerX25519Pub,
		ReceiverX25519PrivBytes: recvPriv,
		ReceiverX25519PubBytes: recvPub,
		DerivedKey:             key,
		DerivedNonce:           nonce,
		KnockIDBytes:           knockIDRaw,
		SAS:                    sas,
		JoinerLANAddress:       req.JoinerLANAddress,
		ExpiresAt:              now.Add(KnockTTL),
		State:                  "pending",
	}

	if err := s.knocks.Store(pk, sourceIP); err != nil {
		return nil, err
	}

	_ = s.audit.Write(auditlog.Event{
		Server:   "minti-cland",
		Tool:     "membership.knock",
		Decision: "allow",
		Args:     map[string]any{"knock_id": knockIDHex, "joiner": req.JoinerMemberID, "src": sourceIP},
	})
	s.log.Info("knock received", "knock_id", knockIDHex, "joiner", req.JoinerMemberID, "sas", sas)

	return &KnockResponse{
		KnockID:              knockIDHex,
		ReceiverX25519PubB64: base64.StdEncoding.EncodeToString(recvPub),
		ClanCertPin:          clan.ClanCertPin,
		ReceiverLANAddress:   s.listenAddr,
	}, nil
}

// KnockList returns pending knocks for the operator TUI/CLI.
func (s *Service) KnockList() ([]*PendingKnock, error) {
	clan, err := s.store.LoadClan()
	if err != nil {
		return nil, err
	}
	if !clan.IsActive() {
		return nil, errors.New("unaffiliated")
	}
	return s.knocks.List(), nil
}

// KnockAccept seals and delivers the welcome blob to the joiner, then adds
// the joiner to the roster as "admitted". The KnockStore CAS ensures only one
// concurrent accept wins (409 behaviour is enforced at the HTTP layer via
// ErrKnockNotPending).
func (s *Service) KnockAccept(knockID, actingMemberID string) error {
	pk, err := s.knocks.Consume(knockID, "accepted")
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	clan, err := s.store.LoadClan()
	if err != nil {
		return err
	}
	if !clan.IsActive() {
		return errors.New("unaffiliated")
	}

	welcome := KnockWelcome{
		ClanID:             clan.ClanID,
		ClanKeyB64:         clan.ClanKeyB64,
		ClanCertPEM:        clan.ClanCertPEM,
		ClanCertPrivKeyB64: clan.ClanCertPrivKeyB64,
		Roster:             clan.Roster,
	}
	ct, err := SealKnockWelcome(pk.DerivedKey, pk.DerivedNonce, pk.KnockIDBytes, welcome)
	if err != nil {
		return err
	}

	deliver := KnockDeliverRequest{
		KnockID:       knockID,
		CiphertextB64: base64.StdEncoding.EncodeToString(ct),
	}
	if err := postKnockDeliver("http://"+pk.JoinerLANAddress+"/clan/knock-deliver", deliver); err != nil {
		// Delivery failed — joiner can knock again. Roster was not yet updated.
		_ = s.audit.Write(auditlog.Event{
			Server: "minti-cland", Tool: "membership.knock_accept", Decision: "deny",
			Args: map[string]any{"knock_id": knockID, "joiner": pk.JoinerMemberID}, Reason: err.Error(),
		})
		return fmt.Errorf("knock: deliver to joiner failed: %w", err)
	}

	// Delivery succeeded — add joiner to roster.
	now := time.Now().UTC()
	updated := upsertRoster(clan.Roster, state.RosterMember{
		MemberID:   pk.JoinerMemberID,
		PubKeyB64:  pk.JoinerPubKeyB64,
		State:      "admitted",
		AdmittedAt: now,
		LastSeenAt: now,
	})
	clan.Roster = updated
	if err := s.store.SaveClan(clan); err != nil {
		return err
	}
	_ = s.audit.Write(auditlog.Event{
		Server: "minti-cland", Tool: "membership.knock_accept", Decision: "allow",
		Args: map[string]any{"knock_id": knockID, "joiner": pk.JoinerMemberID, "accepted_by": actingMemberID},
	})
	s.fireEvent("member_joined", pk.JoinerMemberID)
	s.log.Info("knock accepted, joiner admitted", "knock_id", knockID, "joiner", pk.JoinerMemberID)
	return nil
}

// KnockDeny rejects the knock and notifies the joiner.
func (s *Service) KnockDeny(knockID, reason, actingMemberID string) error {
	pk, err := s.knocks.Consume(knockID, "denied")
	if err != nil {
		return err
	}
	deny := KnockDeliverRequest{
		KnockID: knockID,
		Denied:  true,
		Reason:  reason,
	}
	// Best-effort: if the joiner is already gone or timed out, that's fine.
	_ = postKnockDeliver("http://"+pk.JoinerLANAddress+"/clan/knock-deliver", deny)
	_ = s.audit.Write(auditlog.Event{
		Server: "minti-cland", Tool: "membership.knock_deny", Decision: "allow",
		Args: map[string]any{"knock_id": knockID, "joiner": pk.JoinerMemberID, "denied_by": actingMemberID, "reason": reason},
	})
	s.log.Info("knock denied", "knock_id", knockID, "joiner", pk.JoinerMemberID)
	return nil
}

// postKnockDeliver POSTs a KnockDeliverRequest to the joiner's plain-HTTP
// /clan/knock-deliver endpoint. No TLS, no HMAC — GCM tag is the auth.
func postKnockDeliver(url string, req KnockDeliverRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("joiner returned %d", resp.StatusCode)
	}
	return nil
}
