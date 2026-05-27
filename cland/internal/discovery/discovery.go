// Package discovery is cland's mDNS register + browse layer over
// github.com/grandcat/zeroconf. Register publishes our `_minti-clan._tcp`
// service; Browse fires a callback for any peer in the SAME `clan_id`.
//
// Three Phase D peer-review fixes are folded in:
//
//   - mDNS identity is unauthenticated, so the callback emits a
//     `Candidate{Address}` ONLY (qwen3.6 1A). Member-id binding happens
//     downstream when an authenticated /clan/advertise arrives.
//   - Callbacks are debounced at 1 s per address — neutralises mDNS
//     amplification (qwen3.6 1C).
//   - Register is a no-op when state.Clan.IsActive() is false; an
//     unaffiliated daemon has no clan_id to advertise (qwen3.6 3A).
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	ServiceName = "_minti-clan._tcp"
	Domain      = "local."
	Proto       = "1"
	debounce    = 1 * time.Second
)

// Candidate is the address surfaced by the browser. NO `member_id` here —
// identity binding requires the authenticated round-trip.
type Candidate struct {
	Address string
}

// CandidateFunc receives every newly-debounced candidate seen on the LAN.
type CandidateFunc func(Candidate)

// Service holds the mDNS register handle and the browser context.
type Service struct {
	ClanID    string
	MemberID  string
	Port      int
	Interface string // empty = all multicast-capable
	Log       *slog.Logger

	registerOnce sync.Once
	server       *zeroconf.Server
	running      sync.Mutex

	// Debounce state — last-emitted timestamp per address.
	dmu        sync.Mutex
	lastEmit   map[string]time.Time
}

// Register advertises this member on mDNS. Returns nil + skips registration
// (with a log warning) if the host has no multicast-capable interfaces; the
// caller should rely on /clan/peer-add for discovery in that case.
//
// Per qwen3.6 3A: callers MUST gate this on state.Clan.IsActive() before
// invocation; we refuse to register without a clan_id.
func (s *Service) Register() error {
	if s.ClanID == "" {
		return errors.New("discovery: clan_id required (daemon unaffiliated?)")
	}
	if s.MemberID == "" {
		return errors.New("discovery: member_id required")
	}
	if s.Port <= 0 {
		return errors.New("discovery: port required")
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}

	var ifaces []net.Interface
	if s.Interface != "" {
		iface, err := net.InterfaceByName(s.Interface)
		if err != nil {
			s.Log.Warn("discovery: configured interface unavailable; falling back to default", "iface", s.Interface, "err", err)
		} else {
			ifaces = []net.Interface{*iface}
		}
	}

	srv, err := zeroconf.Register(
		s.MemberID,
		ServiceName,
		Domain,
		s.Port,
		[]string{
			"clan_id=" + s.ClanID,
			"member_id=" + s.MemberID,
			"proto=" + Proto,
		},
		ifaces,
	)
	if err != nil {
		// Common failure: no multicast-capable interface (VirtualBox NAT-
		// only host). Degrade gracefully — manual peer-add still works.
		s.Log.Warn("discovery: mDNS register failed; mDNS-based peer discovery disabled, use `minti-cland peer-add` instead",
			"err", err, "service", ServiceName)
		return nil
	}
	s.running.Lock()
	s.server = srv
	s.running.Unlock()
	s.Log.Info("discovery: registered",
		"service", ServiceName, "instance", s.MemberID,
		"clan_id", s.ClanID, "port", s.Port)
	return nil
}

// Browse runs the mDNS browser until ctx is cancelled. For each ServiceEntry
// whose TXT records say `clan_id` matches ours, the callback is invoked with
// a Candidate{Address: "<ip>:<port>"}. Mismatching clan_ids → WARN log,
// no callback (qwen3.6 — explicit foreign-clan logging is a useful
// diagnostic).
func (s *Service) Browse(ctx context.Context, onCandidate CandidateFunc) error {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	if onCandidate == nil {
		return errors.New("discovery: onCandidate callback required")
	}

	var ifaces []net.Interface
	if s.Interface != "" {
		iface, err := net.InterfaceByName(s.Interface)
		if err == nil {
			ifaces = []net.Interface{*iface}
		}
	}

	resolver, err := zeroconf.NewResolver(zeroconf.SelectIfaces(ifaces))
	if err != nil {
		s.Log.Warn("discovery: NewResolver failed; mDNS browsing disabled", "err", err)
		return nil
	}

	entries := make(chan *zeroconf.ServiceEntry, 16)
	go s.dispatchEntries(ctx, entries, onCandidate)

	if err := resolver.Browse(ctx, ServiceName, Domain, entries); err != nil {
		return fmt.Errorf("discovery: resolver.Browse: %w", err)
	}
	return nil
}

// dispatchEntries reads ServiceEntries, filters by clan_id, debounces, and
// fires the callback. Runs until either ctx cancels OR the entries channel
// closes.
func (s *Service) dispatchEntries(ctx context.Context, entries <-chan *zeroconf.ServiceEntry, cb CandidateFunc) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-entries:
			if !ok {
				return
			}
			if e == nil {
				continue
			}
			candidate, ok := s.parse(e)
			if !ok {
				continue
			}
			if s.shouldEmit(candidate.Address) {
				cb(candidate)
			}
		}
	}
}

// parse extracts a Candidate from a ServiceEntry IF the TXT records carry
// our clan_id. Returns (Candidate{}, false) for foreign clans or malformed
// entries.
func (s *Service) parse(e *zeroconf.ServiceEntry) (Candidate, bool) {
	if e == nil {
		return Candidate{}, false
	}
	// Refuse to surface a record whose member_id equals our own — that's
	// ourselves echoing.
	clanID, memberID := parseTXT(e.Text)
	if clanID == "" || memberID == "" {
		return Candidate{}, false
	}
	if clanID != s.ClanID {
		s.Log.Warn("discovery: foreign clan on LAN, ignoring",
			"foreign_clan_id", clanID, "our_clan_id", s.ClanID)
		return Candidate{}, false
	}
	if memberID == s.MemberID {
		// Self-echo; suppress silently.
		return Candidate{}, false
	}
	// Prefer IPv4 over IPv6 to match transport client expectations.
	var ip net.IP
	if len(e.AddrIPv4) > 0 {
		ip = e.AddrIPv4[0]
	} else if len(e.AddrIPv6) > 0 {
		ip = e.AddrIPv6[0]
	} else {
		return Candidate{}, false
	}
	addr := net.JoinHostPort(ip.String(), fmt.Sprintf("%d", e.Port))
	return Candidate{Address: addr}, true
}

// parseTXT pulls clan_id + member_id from an entry's TXT records.
func parseTXT(txt []string) (clanID, memberID string) {
	for _, t := range txt {
		switch {
		case strings.HasPrefix(t, "clan_id="):
			clanID = strings.TrimPrefix(t, "clan_id=")
		case strings.HasPrefix(t, "member_id="):
			memberID = strings.TrimPrefix(t, "member_id=")
		}
	}
	return
}

// shouldEmit enforces the per-address 1 s debounce. Returns true if this
// address hasn't been emitted in the last `debounce` window.
func (s *Service) shouldEmit(address string) bool {
	s.dmu.Lock()
	defer s.dmu.Unlock()
	if s.lastEmit == nil {
		s.lastEmit = make(map[string]time.Time)
	}
	now := time.Now()
	if last, ok := s.lastEmit[address]; ok && now.Sub(last) < debounce {
		return false
	}
	s.lastEmit[address] = now
	return true
}

// Shutdown deregisters from mDNS. Idempotent.
func (s *Service) Shutdown() {
	s.running.Lock()
	srv := s.server
	s.server = nil
	s.running.Unlock()
	if srv != nil {
		srv.Shutdown()
	}
}
