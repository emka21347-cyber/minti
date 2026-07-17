package memory

// Memory M2: heartbeat-driven gossip (spec §13.5). Near-verbatim port of
// revocations.Syncer — every /clan/heartbeat carries the sender's cached
// memory digest; on mismatch with ours, fetch the sender's full graph over
// HMAC and merge (§13.4 union). Eventual consistency: any edit reaches every
// connected member within one heartbeat round (~2 s) plus one fetch;
// partitioned members converge on the union when the partition heals.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// MaxFetchBytes is the spec §13.5 fetch guard: 4 MiB — deliberately 2× the
// write cap so a merge union transiently past 2 MiB still syncs instead of
// wedging the Clan in permanent digest mismatch.
const MaxFetchBytes = 4 << 20

// Fetcher abstracts the HMAC HTTP client used to GET /clan/memory from a
// peer. Lets tests inject a fake without TLS + HMAC.
type Fetcher interface {
	Do(req *http.Request) (*http.Response, error)
}

// AddressLookup returns a peer's "ip:port" — empty if unknown. Wired to
// peers.Registry.Snapshot in production.
type AddressLookup func(memberID string) string

// SyncerOpts is the dependency bundle.
type SyncerOpts struct {
	Service      *Service
	Fetcher      Fetcher
	LookupAddr   AddressLookup
	Log          *slog.Logger
	FetchTimeout time.Duration // default 5s
}

// Syncer owns the digest-compare + fetch-on-mismatch logic.
type Syncer struct {
	opts SyncerOpts

	// In-flight de-dup: don't fire concurrent fetches for the same peer when
	// many heartbeats arrive close together.
	mu       sync.Mutex
	inflight map[string]struct{} // key = peer member_id
}

func NewSyncer(opts SyncerOpts) (*Syncer, error) {
	if opts.Service == nil {
		return nil, errors.New("memory sync: Service required")
	}
	if opts.Fetcher == nil {
		return nil, errors.New("memory sync: Fetcher required")
	}
	if opts.LookupAddr == nil {
		return nil, errors.New("memory sync: LookupAddr required")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.FetchTimeout <= 0 {
		opts.FetchTimeout = 5 * time.Second
	}
	return &Syncer{opts: opts, inflight: make(map[string]struct{})}, nil
}

// MaybeSync compares the inbound digest against the local cached one; on
// mismatch, fetches the sender's graph and merges. Runs synchronously in the
// caller's goroutine (the short-lived heartbeat-handler goroutine). Returns
// true iff a fetch was actually triggered.
func (s *Syncer) MaybeSync(ctx context.Context, senderID, theirDigest string) bool {
	if theirDigest == "" {
		return false
	}
	if s.opts.Service.Digest() == theirDigest { // cached — no I/O (§13.5)
		return false
	}

	s.mu.Lock()
	if _, busy := s.inflight[senderID]; busy {
		s.mu.Unlock()
		return false
	}
	s.inflight[senderID] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.inflight, senderID)
		s.mu.Unlock()
	}()

	addr := s.opts.LookupAddr(senderID)
	if addr == "" {
		s.opts.Log.Debug("memory sync: peer addr unknown; deferring", "peer", senderID)
		return false
	}

	fctx, cancel := context.WithTimeout(ctx, s.opts.FetchTimeout)
	defer cancel()
	remote, err := s.fetch(fctx, addr)
	if err != nil {
		s.opts.Log.Warn("memory sync: fetch failed",
			"peer", senderID, "addr", addr, "err", err)
		return true
	}

	changed, err := s.opts.Service.ApplyRemote(remote, senderID)
	if err != nil {
		s.opts.Log.Error("memory sync: apply failed", "peer", senderID, "err", err)
		return true
	}
	if changed {
		s.opts.Log.Info("memory sync: applied",
			"peer", senderID, "new_digest", s.opts.Service.Digest()[:12],
			"nodes", len(remote.Nodes), "edges", len(remote.Edges))
	}
	return true
}

// fetch GETs /clan/memory from the peer at `addr`, bounded by MaxFetchBytes.
func (s *Syncer) fetch(ctx context.Context, addr string) (*Graph, error) {
	url := "https://" + addr + "/clan/memory"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.opts.Fetcher.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	// Read one byte past the guard so "exactly at the limit" stays legal and
	// "over it" is detected without buffering an unbounded body.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(raw) > MaxFetchBytes {
		return nil, fmt.Errorf("graph exceeds the %d-byte fetch guard (spec §13.5); refusing", MaxFetchBytes)
	}
	var g Graph
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &g, nil
}
