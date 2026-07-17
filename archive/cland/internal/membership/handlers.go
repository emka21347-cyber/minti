package membership

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/minti/cland/internal/transport"
)

// Register wires the Service's HTTP endpoints onto a transport.Server.
// Endpoints follow docs/clan-protocol.md §10:
//
//   POST /clan/invite      authenticated (any active member)
//   POST /clan/join        anonymous (joiner has no clan_key yet)
//   POST /clan/welcome     authenticated (paste-key joiner already has clan_key)
//   GET  /clan/members     authenticated
//   POST /clan/leave       authenticated (the caller marks itself as leaving)
//   POST /clan/revoke      authenticated (any active member can revoke another)
func (s *Service) Register(srv *transport.Server) {
	srv.Handle("POST /clan/invite", s.handleInvite)
	srv.HandleAnonymous("POST /clan/join", s.handleJoin)
	srv.Handle("POST /clan/welcome", s.handleWelcome)
	srv.Handle("GET /clan/members", s.handleMembers)
	srv.Handle("POST /clan/leave", s.handleLeave)
	srv.Handle("POST /clan/revoke", s.handleRevoke)
	s.RegisterKnock(srv)
}

func (s *Service) handleInvite(w http.ResponseWriter, r *http.Request) {
	var req InviteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = 1 * time.Hour // sensible default
	}
	issuer := transport.OriginMember(r.Context())
	if issuer == "" {
		writeJSONError(w, http.StatusUnauthorized, errors.New("no origin member in context"))
		return
	}
	resp, err := s.IssueInvite(issuer, ttl)
	if err != nil {
		writeJSONError(w, statusForErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleJoin(w http.ResponseWriter, r *http.Request) {
	var req JoinRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.RedeemInvite(req)
	if err != nil {
		writeJSONError(w, statusForErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleWelcome(w http.ResponseWriter, r *http.Request) {
	var req WelcomeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.Welcome(req)
	if err != nil {
		writeJSONError(w, statusForErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleMembers(w http.ResponseWriter, r *http.Request) {
	members, err := s.Members()
	if err != nil {
		writeJSONError(w, statusForErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (s *Service) handleLeave(w http.ResponseWriter, r *http.Request) {
	if err := s.Leave(); err != nil {
		writeJSONError(w, statusForErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "left"})
}

func (s *Service) handleRevoke(w http.ResponseWriter, r *http.Request) {
	var req RevokeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	revoker := transport.OriginMember(r.Context())
	if revoker == "" {
		writeJSONError(w, http.StatusUnauthorized, errors.New("no origin member in context"))
		return
	}
	if err := s.Revoke(req.MemberID, req.Reason, revoker); err != nil {
		writeJSONError(w, statusForErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked", "member_id": req.MemberID})
}

// ---------- helpers ----------

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// statusForErr maps known sentinel errors to HTTP status codes; the default
// is 400.
func statusForErr(err error) int {
	switch {
	case errors.Is(err, ErrInviteUnknown):
		return http.StatusForbidden
	case errors.Is(err, ErrInviteExpired):
		return http.StatusForbidden
	case errors.Is(err, ErrInviteTTL):
		return http.StatusBadRequest
	case errors.Is(err, ErrKnockNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrKnockExpired):
		return http.StatusGone
	case errors.Is(err, ErrKnockNotPending):
		return http.StatusConflict
	case errors.Is(err, ErrKnockRateLimit):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrKnockClanID):
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}
