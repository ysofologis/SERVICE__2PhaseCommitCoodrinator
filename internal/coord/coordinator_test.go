package coord

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelgrok/2phasecommit/internal/txnlog"
)

func newTestDB(t *testing.T) (*txnlog.SQLiteLog, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "2pc-test-*")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmpDir, "test.db")
	db, err := txnlog.NewSQLiteLog(path)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatal(err)
	}
	return db, func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}
}

func TestGenerateTransactionID(t *testing.T) {
	tests := []struct {
		topicKey string
		wantPfx  string
	}{
		{"buy-a-new-product", "tx_buy-a-new-product-"},
		{"order-12345", "tx_order-12345-"},
		{"simple", "tx_simple-"},
	}

	for _, tt := range tests {
		id, err := GenerateTransactionID(tt.topicKey)
		if err != nil {
			t.Fatalf("GenerateTransactionID(%q) error: %v", tt.topicKey, err)
		}
		if len(id) < len(tt.wantPfx)+1 {
			t.Errorf("ID too short: %q", id)
		}
		if id[:len(tt.wantPfx)] != tt.wantPfx {
			t.Errorf("ID prefix mismatch: got %q, want prefix %q", id, tt.wantPfx)
		}
		salt := id[len(tt.wantPfx):]
		if len(salt) != 8 {
			t.Errorf("salt length: got %d, want 8", len(salt))
		}
	}
}

func TestGenerateTransactionIDUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := GenerateTransactionID("uniqueness-test")
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID generated: %q", id)
		}
		seen[id] = true
	}
}

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to State
		want      bool
	}{
		{StateInit, StatePreparing, true},
		{StateInit, StateAborting, true},
		{StateInit, StateCommitting, false},
		{StateInit, StateCommitted, false},
		{StatePreparing, StateCommitting, true},
		{StatePreparing, StateAborting, true},
		{StatePreparing, StateCommitted, false},
		{StateCommitting, StateCommitted, true},
		{StateCommitting, StateAborting, true},
		{StateCommitting, StateAborted, false},
		{StateAborting, StateAborted, true},
		{StateAborting, StateCommitted, false},
		{StateCommitted, StateAborted, false},
		{StateAborted, StateCommitted, false},
		{StateFailed, StateAborted, false},
	}
	for _, tt := range tests {
		if got := CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestFindInFlightByTopic(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	ctx := context.Background()

	tx, err := db.FindInFlightByTopic(ctx, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if tx != nil {
		t.Error("expected nil for nonexistent topic")
	}

	tx1 := &txnlog.Transaction{
		ID:       "tx_test-topic-aaa",
		TopicKey: "test-topic",
		Status:   "INIT",
	}
	if err := db.CreateTransaction(ctx, tx1); err != nil {
		t.Fatal(err)
	}

	found, err := db.FindInFlightByTopic(ctx, "test-topic")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("expected to find in-flight transaction")
	}
	if found.ID != "tx_test-topic-aaa" {
		t.Errorf("wrong ID: %s", found.ID)
	}

	// Terminal with same topic shouldn't affect in-flight lookup
	tx2 := &txnlog.Transaction{
		ID:       "tx_test-topic-bbb",
		TopicKey: "test-topic",
		Status:   "COMMITTED",
	}
	if err := db.CreateTransaction(ctx, tx2); err != nil {
		t.Fatal(err)
	}

	found, err = db.FindInFlightByTopic(ctx, "test-topic")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("expected to still find in-flight after terminal created")
	}
}

func TestLogCreateAndGet(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	ctx := context.Background()

	tx := &txnlog.Transaction{
		ID:       "tx_test-1",
		TopicKey: "test-topic",
		Status:   "INIT",
	}
	if err := db.CreateTransaction(ctx, tx); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetTransaction(ctx, "tx_test-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "tx_test-1" {
		t.Errorf("ID: %s", got.ID)
	}
	if got.TopicKey != "test-topic" {
		t.Errorf("TopicKey: %s", got.TopicKey)
	}
	if got.Status != "INIT" {
		t.Errorf("Status: %s", got.Status)
	}

	_, err = db.GetTransaction(ctx, "tx_nonexistent")
	if err != txnlog.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestLogRecordAndGetVotes(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	ctx := context.Background()

	tx := &txnlog.Transaction{
		ID:       "tx_votes-1",
		TopicKey: "test-topic",
		Status:   "PREPARING",
	}
	if err := db.CreateTransaction(ctx, tx); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	if err := db.RecordVote(ctx, "tx_votes-1", "p1", "yes", "", now); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordVote(ctx, "tx_votes-1", "p2", "no", "insufficient funds", now); err != nil {
		t.Fatal(err)
	}

	votes, err := db.GetVotes(ctx, "tx_votes-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(votes) != 2 {
		t.Fatalf("expected 2 votes, got %d", len(votes))
	}

	found := map[string]txnlog.Vote{}
	for _, v := range votes {
		found[v.Participant] = v
	}
	if found["p1"].Vote != "yes" {
		t.Errorf("p1 vote: %s", found["p1"].Vote)
	}
	if found["p2"].Vote != "no" {
		t.Errorf("p2 vote: %s", found["p2"].Vote)
	}
	if found["p2"].Reason != "insufficient funds" {
		t.Errorf("p2 reason: %s", found["p2"].Reason)
	}
}

func TestLogListTransactions(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	ctx := context.Background()

	for i := 0; i < 5; i++ {
		tx := &txnlog.Transaction{
			ID:       "tx_list-" + string(rune('0'+i)),
			TopicKey: "topic-" + string(rune('0'+i)),
			Status:   "INIT",
		}
		if err := db.CreateTransaction(ctx, tx); err != nil {
			t.Fatal(err)
		}
	}

	txs, err := db.ListTransactions(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(txs) != 5 {
		t.Fatalf("expected 5 transactions, got %d", len(txs))
	}
}

func TestLogFindNonTerminal(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	ctx := context.Background()

	for i, st := range []string{"INIT", "PREPARING", "COMMITTED", "ABORTED", "FAILED"} {
		tx := &txnlog.Transaction{
			ID:       "tx_term-" + string(rune('0'+i)),
			TopicKey: "topic-" + string(rune('0'+i)),
			Status:   st,
		}
		if err := db.CreateTransaction(ctx, tx); err != nil {
			t.Fatal(err)
		}
	}

	nonTerm, err := db.FindNonTerminal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonTerm) != 2 {
		t.Fatalf("expected 2 non-terminal, got %d", len(nonTerm))
	}
}

