package coord

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelgrok/2phasecommit/internal/pax"
)

// testParticipant provides a configurable participant behind an httptest.Server
type testParticipant struct {
	server *httptest.Server
	client pax.ParticipantClient
}

func newTestParticipant(t *testing.T, vote bool, reason string, delayMs int) *testParticipant {
	t.Helper()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delayMs > 0 {
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
		}
		switch r.URL.Path {
		case "/prepare":
			w.Header().Set("Content-Type", "application/json")
			if vote {
				w.Write([]byte(`{"vote":"yes","reason":""}`))
			} else {
				w.Write([]byte(`{"vote":"no","reason":"` + reason + `"}`))
			}
		case "/commit":
			w.WriteHeader(http.StatusOK)
		case "/abort":
			w.WriteHeader(http.StatusOK)
		default:
			if strings.HasPrefix(r.URL.Path, "/tx/") {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"status":"prepared"}`))
			} else {
				http.NotFound(w, r)
			}
		}
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &testParticipant{
		server: server,
		client: pax.NewHTTPClient(server.URL),
	}
}

func (tp *testParticipant) Client() pax.ParticipantClient { return tp.client }

func TestIntegrationHappyPath(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	p1 := newTestParticipant(t, true, "", 0)
	p2 := newTestParticipant(t, true, "", 0)

	coord := NewCoordinator(db, map[string]pax.ParticipantClient{
		"p1": p1.Client(),
		"p2": p2.Client(),
	})

	ctx := context.Background()

	txID, err := coord.StartTransaction(ctx, "happy-path-test", []ParticipantOp{
		{Name: "p1", Action: "create", Params: map[string]interface{}{"order_id": "ORD-1"}, Client: p1.Client()},
		{Name: "p2", Action: "reserve", Params: map[string]interface{}{"item": "WIDGET"}, Client: p2.Client()},
	}, 10000)
	if err != nil {
		t.Fatalf("StartTransaction: %v", err)
	}

	waitForTerminal(t, coord, ctx, txID, 10*time.Second)

	tx, err := coord.GetTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if tx.Status != "COMMITTED" {
		t.Errorf("expected COMMITTED, got %s", tx.Status)
	}
	if len(tx.Votes) != 2 {
		t.Fatalf("expected 2 votes, got %d", len(tx.Votes))
	}
	for _, v := range tx.Votes {
		if v.Vote != "yes" {
			t.Errorf("expected yes for %s, got %s", v.Participant, v.Vote)
		}
	}
}

func TestIntegrationVoteNo(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	p1 := newTestParticipant(t, true, "", 0)
	p2 := newTestParticipant(t, false, "insufficient funds", 0)

	coord := NewCoordinator(db, map[string]pax.ParticipantClient{
		"p1": p1.Client(),
		"p2": p2.Client(),
	})

	ctx := context.Background()

	txID, err := coord.StartTransaction(ctx, "vote-no-test", []ParticipantOp{
		{Name: "p1", Action: "create", Params: map[string]interface{}{}, Client: p1.Client()},
		{Name: "p2", Action: "authorize", Params: map[string]interface{}{"amount": 1000}, Client: p2.Client()},
	}, 10000)
	if err != nil {
		t.Fatalf("StartTransaction: %v", err)
	}

	waitForTerminal(t, coord, ctx, txID, 10*time.Second)

	tx, err := coord.GetTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if tx.Status != "ABORTED" {
		t.Errorf("expected ABORTED, got %s", tx.Status)
	}
}

func TestIntegrationTimeout(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	p1 := newTestParticipant(t, true, "", 200)

	coord := NewCoordinator(db, map[string]pax.ParticipantClient{
		"p1": p1.Client(),
	})

	ctx := context.Background()

	txID, err := coord.StartTransaction(ctx, "timeout-test", []ParticipantOp{
		{Name: "p1", Action: "create", Params: map[string]interface{}{}, Client: p1.Client()},
	}, 50)
	if err != nil {
		t.Fatalf("StartTransaction: %v", err)
	}

	waitForTerminal(t, coord, ctx, txID, 10*time.Second)

	tx, err := coord.GetTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if tx.Status != "ABORTED" {
		t.Errorf("expected ABORTED, got %s", tx.Status)
	}
}

func TestIntegrationCancel(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	p1 := newTestParticipant(t, true, "", 100)

	coord := NewCoordinator(db, map[string]pax.ParticipantClient{
		"p1": p1.Client(),
	})

	ctx := context.Background()

	txID, err := coord.StartTransaction(ctx, "cancel-test", []ParticipantOp{
		{Name: "p1", Action: "create", Params: map[string]interface{}{}, Client: p1.Client()},
	}, 10000)
	if err != nil {
		t.Fatalf("StartTransaction: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if err := coord.CancelTransaction(ctx, txID); err != nil {
		t.Fatalf("CancelTransaction: %v", err)
	}

	waitForTerminal(t, coord, ctx, txID, 10*time.Second)

	tx, err := coord.GetTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if tx.Status != "ABORTED" {
		t.Errorf("expected ABORTED after cancel, got %s", tx.Status)
	}
}

func TestIntegrationTopicDedup(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	p1 := newTestParticipant(t, true, "", 0)

	coord := NewCoordinator(db, map[string]pax.ParticipantClient{
		"p1": p1.Client(),
	})

	ctx := context.Background()

	txID1, err := coord.StartTransaction(ctx, "dedup-topic", []ParticipantOp{
		{Name: "p1", Action: "create", Params: map[string]interface{}{}, Client: p1.Client()},
	}, 10000)
	if err != nil {
		t.Fatalf("first StartTransaction: %v", err)
	}

	txID2, err := coord.StartTransaction(ctx, "dedup-topic", []ParticipantOp{
		{Name: "p1", Action: "create", Params: map[string]interface{}{}},
	}, 10000)
	if err != nil {
		t.Fatalf("second StartTransaction: %v", err)
	}
	if txID1 != txID2 {
		t.Errorf("expected same txID for in-flight dedup: %s vs %s", txID1, txID2)
	}

	waitForTerminal(t, coord, ctx, txID1, 10*time.Second)

	txID3, err := coord.StartTransaction(ctx, "dedup-topic", []ParticipantOp{
		{Name: "p1", Action: "create", Params: map[string]interface{}{}, Client: p1.Client()},
	}, 10000)
	if err != nil {
		t.Fatalf("third StartTransaction: %v", err)
	}
	if txID3 == txID1 {
		t.Errorf("expected new txID after terminal, got same: %s", txID1)
	}
}

func TestIntegrationAddParticipant(t *testing.T) {
	db, closeDB := newTestDB(t)
	defer closeDB()

	p1 := newTestParticipant(t, true, "", 0)
	p2 := newTestParticipant(t, true, "", 0)
	p3 := newTestParticipant(t, false, "out of stock", 0)

	coord := NewCoordinator(db, map[string]pax.ParticipantClient{
		"p1": p1.Client(),
		"p2": p2.Client(),
		"p3": p3.Client(),
	})

	ctx := context.Background()

	txID, err := coord.StartTransaction(ctx, "add-part-test", []ParticipantOp{
		{Name: "p1", Action: "create", Params: map[string]interface{}{}, Client: p1.Client()},
	}, 10000)
	if err != nil {
		t.Fatalf("StartTransaction: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := coord.AddParticipant(ctx, txID, ParticipantOp{
		Name:   "p2",
		Action: "reserve",
		Params: map[string]interface{}{},
		Client: p2.Client(),
	}); err != nil {
		t.Fatalf("AddParticipant p2: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	err = coord.AddParticipant(ctx, txID, ParticipantOp{
		Name:   "p3",
		Action: "reserve",
		Params: map[string]interface{}{},
		Client: p3.Client(),
	})
	if err != nil {
		t.Logf("AddParticipant p3 error (expected, votes no): %v", err)
	}

	waitForTerminal(t, coord, ctx, txID, 10*time.Second)

	tx, err := coord.GetTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if tx.Status != "ABORTED" {
		t.Errorf("expected ABORTED after p3 voted no, got %s", tx.Status)
	}
}

func waitForTerminal(t *testing.T, coord *Coordinator, ctx context.Context, txID string, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		for {
			tx, err := coord.GetTransaction(ctx, txID)
			if err == nil && IsTerminal(State(tx.Status)) {
				close(done)
				return
			}
			select {
			case <-time.After(50 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for tx %s to reach terminal state", txID)
	}
}
