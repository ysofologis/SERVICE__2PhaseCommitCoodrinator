package pax

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestParticipantPrepare(t *testing.T) {
	s := NewParticipantServer()

	// Test successful authorize with sufficient funds
	reqBody := `{"tx_id":"tx1","action":"authorize","params":{"account":"A","amount":5000}}`
	req := httptest.NewRequest("POST", "/prepare", fromString(reqBody))
	rr := httptest.NewRecorder()

	s.HandlePrepare(rr, req)

	if rr.Code != 200 {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp PrepareResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Vote != "yes" {
		t.Errorf("expected vote yes, got %s", resp.Vote)
	}

	// Check that the transaction is marked as prepared
	s.mu.RLock()
	if !s.prepared["tx1"] {
		t.Error("expected tx1 to be marked as prepared")
	}
	if s.txStates["tx1"] != "prepared" {
		t.Errorf("expected tx state 'prepared', got %s", s.txStates["tx1"])
	}
	s.mu.RUnlock()

	// Test idempotent prepare (should return yes again) - create fresh request
	req2 := httptest.NewRequest("POST", "/prepare", fromString(reqBody))
	rr2 := httptest.NewRecorder()
	s.HandlePrepare(rr2, req2)
	if rr2.Code != 200 {
		t.Errorf("expected status 200 on idempotent prepare, got %d", rr2.Code)
	}

	// Test authorize with insufficient funds
	reqBody3 := `{"tx_id":"tx2","action":"authorize","params":{"account":"A","amount":20000}}`
	req3 := httptest.NewRequest("POST", "/prepare", fromString(reqBody3))
	rr3 := httptest.NewRecorder()

	s.HandlePrepare(rr3, req3)

	if rr3.Code != 200 {
		t.Errorf("expected status 200 even on no vote, got %d", rr3.Code)
	}
	var resp3 PrepareResponse
	json.NewDecoder(rr3.Body).Decode(&resp3)
	if resp3.Vote != "no" {
		t.Errorf("expected vote no, got %s", resp3.Vote)
	}
	if resp3.Reason != "insufficient funds" {
		t.Errorf("expected reason 'insufficient funds', got %s", resp3.Reason)
	}
}

func TestParticipantCommit(t *testing.T) {
	s := NewParticipantServer()

	// Prepare a transaction first
	s.prepared["tx1"] = true
	s.txStates["tx1"] = "prepared"
	s.pendingDeltas["tx1"] = map[string]int64{"A": -5000}

	// Commit the transaction
	reqBody := `{"tx_id":"tx1"}`
	req := httptest.NewRequest("POST", "/commit", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.HandleCommit(rr, req)

	if rr.Code != 200 {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	// Check that the ledger was updated
	s.mu.RLock()
	if s.ledger["A"] != 5000 { // 10000 - 5000 = 5000
		t.Errorf("expected ledger[A]=5000, got %d", s.ledger["A"])
	}
	if s.txStates["tx1"] != "committed" {
		t.Errorf("expected tx state 'committed', got %s", s.txStates["tx1"])
	}
	if s.prepared["tx1"] {
		t.Error("expected prepared flag to be cleared")
	}
	s.mu.RUnlock()

	// Test idempotent commit - create fresh request
	req2 := httptest.NewRequest("POST", "/commit", strings.NewReader(reqBody))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	s.HandleCommit(rr2, req2)
	if rr2.Code != 200 {
		t.Errorf("expected status 200 on idempotent commit, got %d", rr2.Code)
	}
}

func TestParticipantAbort(t *testing.T) {
	s := NewParticipantServer()

	// Prepare a transaction
	s.prepared["tx1"] = true
	s.txStates["tx1"] = "prepared"
	s.pendingDeltas["tx1"] = map[string]int64{"A": -5000}

	// Abort the transaction
	reqBody := `{"tx_id":"tx1"}`
	req := httptest.NewRequest("POST", "/abort", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.HandleAbort(rr, req)

	if rr.Code != 200 {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	// Check that pending deltas were cleared and state is aborted
	s.mu.RLock()
	if s.txStates["tx1"] != "aborted" {
		t.Errorf("expected tx state 'aborted', got %s", s.txStates["tx1"])
	}
	if s.prepared["tx1"] {
		t.Error("expected prepared flag to be cleared")
	}
	if _, ok := s.pendingDeltas["tx1"]; ok {
		t.Error("expected pending deltas to be cleared")
	}
	s.mu.RUnlock()

	// Test idempotent abort - create fresh request
	req2 := httptest.NewRequest("POST", "/abort", strings.NewReader(reqBody))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	s.HandleAbort(rr2, req2)
	if rr2.Code != 200 {
		t.Errorf("expected status 200 on idempotent abort, got %d", rr2.Code)
	}
}

func TestTxStatus(t *testing.T) {
	s := NewParticipantServer()

	s.txStates["tx1"] = "committed"

	// Create a request with chi URL param using chi.NewRouter()
	r := chi.NewRouter()
	r.Get("/tx/{id}", s.HandleTxStatus)

	req := httptest.NewRequest("GET", "/tx/tx1", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "committed" {
		t.Errorf("expected status 'committed', got %s", resp["status"])
	}
}

// Helper to create an io.ReadCloser from a string
func fromString(s string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(s))
}

func TestAdminReset(t *testing.T) {
	s := NewParticipantServer()

	// Mutate state: drain account A, prepare a transaction, mark
	// another as committed. After /admin/reset, all of this should be
	// gone and the ledger should be back to the initial seed.
	s.mu.Lock()
	s.ledger["A"] = 250 // drained from 10000
	s.prepared["tx_in_flight"] = true
	s.txStates["tx_in_flight"] = "prepared"
	s.txStates["tx_done"] = "committed"
	s.pendingDeltas["tx_in_flight"] = map[string]int64{"A": -9750}
	s.mu.Unlock()

	r := chi.NewRouter()
	r.Post("/admin/reset", s.HandleAdminReset)
	req := httptest.NewRequest("POST", "/admin/reset", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if got := s.ledger["A"]; got != 10000 {
		t.Errorf("ledger[A]: want 10000 (initial seed), got %d", got)
	}
	if got := s.ledger["B"]; got != 10000 {
		t.Errorf("ledger[B]: want 10000 (initial seed), got %d", got)
	}
	if _, ok := s.prepared["tx_in_flight"]; ok {
		t.Error("tx_in_flight should be removed from prepared set after reset")
	}
	if _, ok := s.txStates["tx_in_flight"]; ok {
		t.Error("tx_in_flight should be removed from txStates after reset")
	}
	if _, ok := s.txStates["tx_done"]; ok {
		t.Error("tx_done should be removed from txStates after reset")
	}
	if _, ok := s.pendingDeltas["tx_in_flight"]; ok {
		t.Error("tx_in_flight pending deltas should be cleared after reset")
	}
}
