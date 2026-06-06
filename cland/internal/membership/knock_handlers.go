package membership

import (
	"errors"
	"net"
	"net/http"

	"github.com/minti/cland/internal/transport"
)

// RegisterKnock wires the §3.4 knock-flow endpoints onto the transport.Server.
//
//	POST /clan/knock        anonymous — joiner initiates
//	GET  /clan/knock-list   HMAC      — operator lists pending knocks
//	POST /clan/knock-accept HMAC      — operator accepts (delivers sealed blob)
//	POST /clan/knock-deny   HMAC      — operator denies
func (s *Service) RegisterKnock(srv *transport.Server) {
	srv.HandleAnonymous("POST /clan/knock", s.handleKnock)
	srv.Handle("GET /clan/knock-list", s.handleKnockList)
	srv.Handle("POST /clan/knock-accept", s.handleKnockAccept)
	srv.Handle("POST /clan/knock-deny", s.handleKnockDeny)
}

func (s *Service) handleKnock(w http.ResponseWriter, r *http.Request) {
	var req KnockRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	// Extract source IP for rate limiting.
	sourceIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		sourceIP = r.RemoteAddr
	}
	resp, err := s.Knock(req, sourceIP)
	if err != nil {
		switch {
		case errors.Is(err, ErrKnockClanID):
			writeJSONError(w, http.StatusForbidden, err)
		case errors.Is(err, ErrKnockRateLimit):
			writeJSONError(w, http.StatusTooManyRequests, err)
		default:
			writeJSONError(w, http.StatusBadRequest, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleKnockList(w http.ResponseWriter, _ *http.Request) {
	knocks, err := s.KnockList()
	if err != nil {
		writeJSONError(w, statusForErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"knocks": knocks})
}

func (s *Service) handleKnockAccept(w http.ResponseWriter, r *http.Request) {
	var req KnockAcceptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	actor := transport.OriginMember(r.Context())
	if actor == "" {
		writeJSONError(w, http.StatusUnauthorized, errors.New("no origin member in context"))
		return
	}
	if err := s.KnockAccept(req.KnockID, actor); err != nil {
		switch {
		case errors.Is(err, ErrKnockNotFound):
			writeJSONError(w, http.StatusNotFound, err)
		case errors.Is(err, ErrKnockExpired):
			writeJSONError(w, http.StatusGone, err)
		case errors.Is(err, ErrKnockNotPending):
			// First-write-wins: concurrent accepts from other members get 409.
			writeJSONError(w, http.StatusConflict, err)
		default:
			writeJSONError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted", "knock_id": req.KnockID})
}

func (s *Service) handleKnockDeny(w http.ResponseWriter, r *http.Request) {
	var req KnockDenyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	actor := transport.OriginMember(r.Context())
	if actor == "" {
		writeJSONError(w, http.StatusUnauthorized, errors.New("no origin member in context"))
		return
	}
	if err := s.KnockDeny(req.KnockID, req.Reason, actor); err != nil {
		switch {
		case errors.Is(err, ErrKnockNotFound):
			writeJSONError(w, http.StatusNotFound, err)
		case errors.Is(err, ErrKnockExpired):
			writeJSONError(w, http.StatusGone, err)
		case errors.Is(err, ErrKnockNotPending):
			writeJSONError(w, http.StatusConflict, err)
		default:
			writeJSONError(w, http.StatusInternalServerError, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "denied", "knock_id": req.KnockID})
}
