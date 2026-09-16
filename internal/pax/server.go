package pax

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

type ParticipantServer struct {
	mu          sync.RWMutex
	ledger      map[string]int64 // account -> balance
	prepared    map[string]bool  // tx_id -> prepared
	txStates    map[string]string // tx_id -> "prepared", "committed", "aborted"
	prepareLock sync.Mutex       // per-account lock simulation
	pendingDeltas map[string]map[string]int64 // tx_id -> {account: delta} for commit/abort
}

type operationRequest struct {
	TxID   string                 `json:"tx_id"`
	Action string                 `json:"action"`
	Params map[string]interface{} `json:"params"`
}

func NewParticipantServer() *ParticipantServer {
	s := &ParticipantServer{
		ledger:       make(map[string]int64),
		prepared:     make(map[string]bool),
		txStates:     make(map[string]string),
		pendingDeltas: make(map[string]map[string]int64),
	}
	// Pre-seed ledger for demo purposes
	s.ledger["A"] = 10000
	s.ledger["B"] = 10000
	return s
}

func (s *ParticipantServer) Routes() *chi.Mux {
	r := chi.NewRouter()
	r.Post("/prepare", s.HandlePrepare)
	r.Post("/commit", s.HandleCommit)
	r.Post("/abort", s.HandleAbort)
	r.Get("/tx/{id}", s.HandleTxStatus)
	return r
}

func (s *ParticipantServer) HandlePrepare(w http.ResponseWriter, r *http.Request) {
	var req operationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Simulate some work
	time.Sleep(50 * time.Millisecond)

	s.prepareLock.Lock()
	defer s.prepareLock.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already prepared
	if s.txStates[req.TxID] == "prepared" || s.prepared[req.TxID] {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PrepareResponse{Vote: "yes"})
		return
	}

	// Check if already committed or aborted
	if status, ok := s.txStates[req.TxID]; ok && (status == "committed" || status == "aborted") {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PrepareResponse{Vote: "yes"})
		return
	}

	// Simulate business logic based on action
	vote := true
	reason := ""
	deltas := make(map[string]int64)

	switch req.Action {
	case "create":
		// Create order: record intent, always vote yes
		if orderID, ok := req.Params["order_id"].(string); ok {
			deltas["orders:"+orderID] = 1 // mark order as created (pending)
		}
	case "authorize":
		// Authorize payment: check balance
		if account, ok := req.Params["account"].(string); ok {
			amount, _ := req.Params["amount"].(float64)
			amt := int64(amount)
			if s.ledger[account] < amt {
				vote = false
				reason = "insufficient funds"
			} else {
				// Reserve the amount (negative delta)
				deltas[account] = -amt
			}
		}
	case "reserve":
		// Reserve inventory: check if item is available
		if item, ok := req.Params["item"].(string); ok {
			qty, _ := req.Params["qty"].(float64)
			q := int64(qty)
			// Simulate inventory check: always available for demo
			// In production, check actual inventory levels
			if q <= 0 {
				vote = false
				reason = "invalid quantity"
			} else {
				deltas["inventory:"+item] = -q // pending reservation
			}
		}
	default:
		// Unknown action: vote yes but log warning
		log.Printf("participant: unknown action %q for tx %s", req.Action, req.TxID)
	}

	if !vote {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(PrepareResponse{Vote: "no", Reason: reason})
		return
	}

	// Mark as prepared and store pending changes
	s.prepared[req.TxID] = true
	s.txStates[req.TxID] = "prepared"
	if len(deltas) > 0 {
		s.pendingDeltas[req.TxID] = deltas
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PrepareResponse{Vote: "yes"})
}

func (s *ParticipantServer) HandleCommit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxID string `json:"tx_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	s.prepareLock.Lock()
	defer s.prepareLock.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already committed (idempotent)
	if status, ok := s.txStates[req.TxID]; ok && status == "committed" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Must be prepared to commit
	if !s.prepared[req.TxID] {
		http.Error(w, "cannot commit: not prepared", http.StatusBadRequest)
		return
	}

	// Apply pending changes
	if deltas, ok := s.pendingDeltas[req.TxID]; ok {
		for account, delta := range deltas {
			s.ledger[account] += delta
		}
		delete(s.pendingDeltas, req.TxID)
	}

	// Mark as committed
	s.prepared[req.TxID] = false
	s.txStates[req.TxID] = "committed"
	w.WriteHeader(http.StatusOK)
}

func (s *ParticipantServer) HandleAbort(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxID string `json:"tx_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	s.prepareLock.Lock()
	defer s.prepareLock.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already aborted (idempotent)
	if status, ok := s.txStates[req.TxID]; ok && status == "aborted" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Clear any pending changes
	delete(s.pendingDeltas, req.TxID)

	// Mark as aborted
	s.prepared[req.TxID] = false
	s.txStates[req.TxID] = "aborted"
	w.WriteHeader(http.StatusOK)
}

func (s *ParticipantServer) HandleTxStatus(w http.ResponseWriter, r *http.Request) {
	txID := chi.URLParam(r, "id")

	s.mu.RLock()
	status, ok := s.txStates[txID]
	s.mu.RUnlock()

	if !ok {
		status = "unknown"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TxStatusResponse{Status: status})
}
