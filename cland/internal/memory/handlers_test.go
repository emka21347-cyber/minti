package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// importHandler builds a Handler over a fresh service with a fixed origin.
func importHandler(t *testing.T) (*Handler, *Service) {
	t.Helper()
	svc, _ := newTestService(t)
	h := &Handler{
		Svc:    svc,
		Origin: func(context.Context) string { return "remote-member" },
	}
	return h, svc
}

func importBody(t *testing.T, mode string) []byte {
	t.Helper()
	g := graph([]Node{node("00000000-0000-4000-8000-0000000000cc", 1, "2026-06-11T10:00:00Z")}, nil)
	bp, err := ExportBlueprint(g, "clan-src", "", false, time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(ImportRequest{Blueprint: bp, Mode: mode})
	return body
}

// TestImportReplaceLoopbackOnly pins the M0-review F4 gate: mode "replace"
// from a non-local TCP peer is 403; merge from anywhere is fine; replace
// from loopback is fine (spec §13.6).
func TestImportReplaceLoopbackOnly(t *testing.T) {
	h, _ := importHandler(t)

	// Remote replace → 403.
	req := httptest.NewRequest(http.MethodPost, "/clan/memory/import", bytes.NewReader(importBody(t, "replace")))
	req.RemoteAddr = "203.0.113.7:51234"
	rec := httptest.NewRecorder()
	h.handleImport(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote replace = %d, want 403", rec.Code)
	}

	// Remote merge → 200.
	req = httptest.NewRequest(http.MethodPost, "/clan/memory/import", bytes.NewReader(importBody(t, "merge")))
	req.RemoteAddr = "203.0.113.7:51234"
	rec = httptest.NewRecorder()
	h.handleImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remote merge = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// Loopback replace → 200.
	h2, svc2 := importHandler(t)
	req = httptest.NewRequest(http.MethodPost, "/clan/memory/import", bytes.NewReader(importBody(t, "replace")))
	req.RemoteAddr = "127.0.0.1:51234"
	rec = httptest.NewRecorder()
	h2.handleImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("loopback replace = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	snap := svc2.Snapshot()
	if len(snap.Nodes) != 1 || snap.Nodes[0].Provenance.Source != "import" {
		t.Fatalf("replace did not land the imported graph: %+v", snap.Nodes)
	}
}

// TestImportRejectsTamperedBlueprint: the daemon re-validates server-side —
// a tampered file must never reach the graph even if a client skips its own
// validation.
func TestImportRejectsTamperedBlueprint(t *testing.T) {
	h, svc := importHandler(t)

	g := graph([]Node{node("00000000-0000-4000-8000-0000000000dd", 1, "2026-06-11T10:00:00Z")}, nil)
	bp, err := ExportBlueprint(g, "clan-src", "", false, time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	bp.Graph.Nodes[0].Title = "tampered after checksum"
	body, _ := json.Marshal(ImportRequest{Blueprint: bp, Mode: "merge"})

	req := httptest.NewRequest(http.MethodPost, "/clan/memory/import", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:51234"
	rec := httptest.NewRecorder()
	h.handleImport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tampered import = %d, want 400", rec.Code)
	}
	if len(svc.Snapshot().Nodes) != 0 {
		t.Fatal("tampered graph must not land")
	}
}
