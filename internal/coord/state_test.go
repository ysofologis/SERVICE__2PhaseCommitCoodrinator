package coord

import (
	"testing"
	"time"

	"github.com/modelgrok/2phasecommit/internal/pax"
	"github.com/modelgrok/2phasecommit/internal/txnlog"
)

func TestStateTransitions(t *testing.T) {
	// Create a mock log (we'll use SQLite in-memory for simplicity)
	db, err := txnlog.NewSQLiteLog(":memory:")
	if err != nil {
		t.Fatalf("failed to create in-memory db: %v", err)
	}
	defer db.Close()

	coord := NewCoordinator(db, make(map[string]pax.ParticipantClient))

	// Create a transaction handle
	now := time.Now().UTC()
	tx := &txnlog.Transaction{
		ID:        "test-tx-1",
		TopicKey:  "test-topic",
		Status:    "INIT",
		CreatedAt: now,
		UpdatedAt: now,
	}

	handle := &txHandle{
		tx:            tx,
		participants:  []ParticipantOp{},
		votes:         make(map[string]voteInfo),
		state:         StateInit,
		done:          make(chan struct{}),
		cancel:        make(chan struct{}),
	}

	// Test INIT -> PREPARING
	coord.transitionState(handle, StatePreparing)
	if handle.state != StatePreparing {
		t.Errorf("expected state PREPARING, got %s", handle.state)
	}
	if tx.Status != "PREPARING" {
		t.Errorf("expected tx status PREPARING, got %s", tx.Status)
	}

	// Test PREPARING -> COMMITTING
	coord.transitionState(handle, StateCommitting)
	if handle.state != StateCommitting {
		t.Errorf("expected state COMMITTING, got %s", handle.state)
	}

	// Test COMMITTING -> COMMITTED (terminal)
	coord.transitionState(handle, StateCommitted)
	if handle.state != StateCommitted {
		t.Errorf("expected state COMMITTED, got %s", handle.state)
	}
	if tx.DecisionAt == nil {
		t.Error("expected DecisionAt to be set for terminal state")
	}
	if tx.DecidedBy != "protocol" {
		t.Errorf("expected DecidedBy='protocol', got %s", tx.DecidedBy)
	}

	// Create another handle for abort path
	tx2 := &txnlog.Transaction{
		ID:        "test-tx-2",
		TopicKey:  "test-topic-2",
		Status:    "INIT",
		CreatedAt: now,
		UpdatedAt: now,
	}
	handle2 := &txHandle{
		tx:            tx2,
		participants:  []ParticipantOp{},
		votes:         make(map[string]voteInfo),
		state:         StateInit,
		done:          make(chan struct{}),
		cancel:        make(chan struct{}),
	}

	// Test INIT -> PREPARING -> ABORTING -> ABORTED
	coord.transitionState(handle2, StatePreparing)
	coord.transitionState(handle2, StateAborting)
	if handle2.state != StateAborting {
		t.Errorf("expected state ABORTING, got %s", handle2.state)
	}
	coord.transitionState(handle2, StateAborted)
	if handle2.state != StateAborted {
		t.Errorf("expected state ABORTED, got %s", handle2.state)
	}
	if tx2.DecisionAt == nil {
		t.Error("expected DecisionAt to be set for ABORTED state")
	}
	if tx2.DecidedBy != "protocol" {
		t.Errorf("expected DecidedBy='protocol', got %s", tx2.DecidedBy)
	}
}

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		state State
		want  bool
	}{
		{StateInit, false},
		{StatePreparing, false},
		{StateCommitting, false},
		{StateAborting, false},
		{StateCommitted, true},
		{StateAborted, true},
		{StateFailed, true},
	}
	for _, tt := range tests {
		if got := IsTerminal(tt.state); got != tt.want {
			t.Errorf("IsTerminal(%s) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

