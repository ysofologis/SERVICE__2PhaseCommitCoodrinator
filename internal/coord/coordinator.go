package coord

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/modelgrok/2phasecommit/internal/metrics"
	"github.com/modelgrok/2phasecommit/internal/pax"
	"github.com/modelgrok/2phasecommit/internal/txnlog"
)

var (
	ErrTransactionAborted = errors.New("transaction aborted")
	ErrTransactionTimeout = errors.New("transaction timed out")
	ErrTransactionNotFound = errors.New("transaction not found")
	ErrTransactionTerminal = errors.New("transaction already in terminal state")
)

type Coordinator struct {
	log          txnlog.Log
	mu           sync.RWMutex
	inFlight     map[string]*txHandle // tx_id -> handle
	participants map[string]pax.ParticipantClient // name -> client
	startTimes   map[string]time.Time // tx_id -> start time for metrics
}

type txHandle struct {
	tx           *txnlog.Transaction
	state        State
	votes        map[string]voteInfo // participant -> vote
	cancel       chan struct{}
	done         chan struct{}
	participants []ParticipantOp
	mu           sync.RWMutex
}

type ParticipantOp struct {
	Name    string
	Action  string
	Params  map[string]interface{}
	Client  pax.ParticipantClient
}

type voteInfo struct {
	Vote       string
	Reason     string
	At         time.Time
	CommittedAt *time.Time
	AbortedAt   *time.Time
}

func NewCoordinator(log txnlog.Log, participants map[string]pax.ParticipantClient) *Coordinator {
	return &Coordinator{
		log:          log,
		inFlight:     make(map[string]*txHandle),
		participants: participants,
		startTimes:   make(map[string]time.Time),
	}
}

// GenerateTransactionID creates a unique transaction ID from topic_key
func GenerateTransactionID(topicKey string) (string, error) {
	salt := make([]byte, 4) // 8 hex chars
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	return fmt.Sprintf("tx_%s-%s", topicKey, hex.EncodeToString(salt)), nil
}

// WaitForTerminal blocks until the transaction reaches a terminal state or the deadline expires.
// Returns the transaction with votes, or an error if aborted/timeout/not-found.
func (c *Coordinator) WaitForTerminal(ctx context.Context, txID string, timeout time.Duration) (*txnlog.TransactionWithVotes, error) {
	c.mu.RLock()
	handle, ok := c.inFlight[txID]
	c.mu.RUnlock()

	if !ok {
		tx, err := c.log.GetTransaction(ctx, txID)
		if err != nil {
			if errors.Is(err, txnlog.ErrNotFound) {
				return nil, ErrTransactionNotFound
			}
			return nil, fmt.Errorf("get transaction: %w", err)
		}
		if IsTerminal(State(tx.Status)) {
			return tx, nil
		}
		return nil, fmt.Errorf("transaction not in flight and not terminal: %s", tx.Status)
	}

	select {
	case <-handle.done:
		tx, err := c.log.GetTransaction(ctx, txID)
		if err != nil {
			return nil, fmt.Errorf("get final transaction: %w", err)
		}
		if tx.Status == string(StateAborted) || tx.Status == string(StateFailed) {
			return tx, ErrTransactionAborted
		}
		return tx, nil
	case <-ctx.Done():
		return nil, ErrTransactionTimeout
	}
}

// StartTransaction begins a new distributed transaction or returns existing in-flight
func (c *Coordinator) StartTransaction(ctx context.Context, topicKey string, ops []ParticipantOp, timeoutMs int) (string, error) {
	// Check for in-flight transaction with same topic
	existing, err := c.log.FindInFlightByTopic(ctx, topicKey)
	if err != nil && err != txnlog.ErrNotFound {
		return "", fmt.Errorf("check in-flight: %w", err)
	}
	if existing != nil {
		return existing.ID, nil
	}

	// Generate transaction ID
	txID, err := GenerateTransactionID(topicKey)
	if err != nil {
		return "", fmt.Errorf("generate tx id: %w", err)
	}

	now := time.Now().UTC()
	tx := &txnlog.Transaction{
		ID:       txID,
		TopicKey: topicKey,
		Status:   string(StateInit),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := c.log.CreateTransaction(ctx, tx); err != nil {
		return "", fmt.Errorf("create transaction: %w", err)
	}

	// Create handle
	handle := &txHandle{
		tx:   tx,
		state: StateInit,
		votes: make(map[string]voteInfo),
		cancel: make(chan struct{}, 1),
		done:   make(chan struct{}),
		participants: ops,
	}

	c.mu.Lock()
	c.inFlight[txID] = handle
	c.startTimes[txID] = now
	c.mu.Unlock()

	// Record metrics
	metrics.RecordTransactionStart()

	// Start the 2PC process asynchronously with a background context
	// The transaction should continue even after the HTTP request completes
	var txCtx context.Context
	if timeoutMs > 0 {
		var cancel context.CancelFunc
		txCtx, cancel = context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
		_ = cancel // will be cancelled automatically on timeout
	} else {
		txCtx = context.Background()
	}
	go c.runTransaction(txCtx, handle, timeoutMs)

	return txID, nil
}

// AddParticipant adds a new participant to an in-progress transaction (only allowed in PREPARING).
//
// The new participant is appended to the in-memory participants list. The
// runTransaction loop, which is iterating on that list, will see the growth
// and start a new prepare round that includes the new participant. The
// caller is NOT blocked: the prepare is performed by runTransaction's own
// goroutines, not synchronously here. This avoids double-prepare races and
// keeps the commit decision centralized in runTransaction.
//
// Returns nil once the participant is registered. The actual vote result is
// observable via GetTransaction or by waiting for the transaction to reach
// a terminal state.
func (c *Coordinator) AddParticipant(ctx context.Context, txID string, op ParticipantOp) error {
	c.mu.Lock()
	handle, ok := c.inFlight[txID]
	c.mu.Unlock()

	if !ok {
		return fmt.Errorf("transaction not found: %s", txID)
	}

	if handle.state != StatePreparing {
		return fmt.Errorf("cannot add participant in state %s", handle.state)
	}

	// Refuse cancellation in progress: once cancel is signalled, the
	// transaction is heading to ABORT and adding more voters is pointless.
	select {
	case <-handle.cancel:
		return errors.New("transaction is cancelling")
	default:
	}

	// Add to participants list. runTransaction's loop will see this on
	// its next snapshot and run a prepare round that includes the new
	// participant.
	handle.mu.Lock()
	for _, existing := range handle.participants {
		if existing.Name == op.Name {
			handle.mu.Unlock()
			return fmt.Errorf("participant %q already in transaction", op.Name)
		}
	}
	handle.participants = append(handle.participants, op)
	handle.mu.Unlock()

	return nil
}

// CancelTransaction initiates cancellation of a non-terminal transaction.
// It only signals cancellation; the actual abort path is owned by runTransaction
// (or recovery) so we don't double-abort and race with the in-flight protocol.
func (c *Coordinator) CancelTransaction(ctx context.Context, txID string) error {
	c.mu.Lock()
	handle, ok := c.inFlight[txID]
	c.mu.Unlock()

	if !ok {
		return fmt.Errorf("transaction not found: %s", txID)
	}

	if IsTerminal(handle.state) {
		return fmt.Errorf("transaction already in terminal state: %s", handle.state)
	}

	// Signal cancellation. runTransaction (if still alive) observes this and
	// takes the abort path on its own. If runTransaction has already returned
	// (transaction is terminal in the in-memory handle but handle hasn't been
	// removed yet), the abort goroutine below covers that case.
	select {
	case <-handle.cancel:
		// Already cancelled
	default:
		close(handle.cancel)
	}

	// Trigger abort path asynchronously; runTransaction may also call abort
	// if it observes the cancellation, but abortTransaction is idempotent on
	// state (transitionState is a write, double-transition is harmless for
	// the in-memory state and the last writer wins on the log).
	go c.abortTransaction(ctx, handle, "operator-cancelled")
	return nil
}

// runTransaction executes the 2PC protocol for a transaction.
//
// It loops: each round snapshots the current participants, sends prepare in
// parallel, waits for all to return, then either aborts (any error) or —
// if all voted yes and no new participants were added during the round —
// commits. If new participants arrived mid-round (via AddParticipant), it
// re-iterates so they get a vote before the commit decision is made.
func (c *Coordinator) runTransaction(ctx context.Context, handle *txHandle, timeoutMs int) {
	startTime := time.Now()
	defer func() {
		close(handle.done)
		c.mu.Lock()
		delete(c.inFlight, handle.tx.ID)
		delete(c.startTimes, handle.tx.ID)
		c.mu.Unlock()
		// Record transaction duration and count
		metrics.RecordTransactionEnd(handle.tx.Status, startTime)
	}()

	// Transition to PREPARING
	c.transitionState(handle, StatePreparing)

	// Phase 1: Prepare (parallel), repeated for dynamic additions
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	// Track which participants have already voted in any round. Once a
	// participant has voted, we don't re-send prepare to them — the only
	// way to "re-ask" is via AddParticipant, which always uses a fresh
	// participant entry. (Existing vote entries are preserved across
	// rounds.)
	for {
		// Re-check cancellation: if handle.cancel was closed before we
		// even started the loop, abort immediately.
		select {
		case <-handle.cancel:
			c.abortTransaction(timeoutCtx, handle, "cancelled-before-prepare")
			return
		default:
		}

		// Snapshot participants list to avoid data races if AddParticipant
		// is called concurrently. Skip anyone who already has a recorded
		// vote in handle.votes.
		handle.mu.Lock()
		participants := make([]ParticipantOp, 0, len(handle.participants))
		for _, op := range handle.participants {
			if _, hasVote := handle.votes[op.Name]; !hasVote {
				participants = append(participants, op)
			}
		}
		snapshotLen := len(handle.participants)
		handle.mu.Unlock()

		// No new participants to ask, and no one is left without a vote —
		// the next round would be a no-op. Decide here.
		if len(participants) == 0 {
			break
		}

		// Send prepare to all not-yet-voted participants concurrently.
		var wg sync.WaitGroup
		var errMu sync.Mutex
		var prepareErrs []error

		for _, op := range participants {
			wg.Add(1)
			go func(op ParticipantOp) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						log.Printf("panic in prepare for %s: %v", op.Name, r)
						metrics.RecordError("prepare_panic")
						errMu.Lock()
						prepareErrs = append(prepareErrs, fmt.Errorf("panic in prepare for %s: %v", op.Name, r))
						errMu.Unlock()
					}
				}()

				select {
				case <-handle.cancel:
					errMu.Lock()
					prepareErrs = append(prepareErrs, errors.New("cancelled"))
					errMu.Unlock()
					return
				case <-timeoutCtx.Done():
					errMu.Lock()
					prepareErrs = append(prepareErrs, fmt.Errorf("prepare timeout"))
					errMu.Unlock()
					return
				default:
					vote, reason, err := op.Client.Prepare(timeoutCtx, handle.tx.ID, op.Action, op.Params)
					if err != nil {
						errMu.Lock()
						prepareErrs = append(prepareErrs, fmt.Errorf("prepare error: %w", err))
						errMu.Unlock()
						return
					}
					now := time.Now().UTC()
					voteStr := map[bool]string{true: "yes", false: "no"}[vote]
					handle.mu.Lock()
					handle.votes[op.Name] = voteInfo{
						Vote:   voteStr,
						Reason: reason,
						At:     now,
					}
					handle.mu.Unlock()

					// Record the vote in the persistent log. The log keeps
					// the original vote (yes/no/timeout); the outcome
					// (committed/aborted) is recorded separately.
					if err := c.log.RecordVote(context.Background(), handle.tx.ID, op.Name, voteStr, reason, now); err != nil {
						log.Printf("failed to record vote for %s: %v", op.Name, err)
						metrics.RecordError("record_vote_failed")
						// Continue anyway; this is not fatal
					}

					metrics.RecordVote(op.Name, voteStr)

					if !vote {
						errMu.Lock()
						prepareErrs = append(prepareErrs, fmt.Errorf("participant voted no: %s", reason))
						errMu.Unlock()
						return
					}
				}
			}(op)
		}

		wg.Wait()

		if len(prepareErrs) > 0 {
			log.Printf("prepare errors for tx %s: %v", handle.tx.ID, prepareErrs)
			metrics.RecordError("prepare_errors")
			c.abortTransaction(timeoutCtx, handle, "prepare-failure")
			return
		}

			// Did participants list grow during this round? If so, loop and
		// ask the new ones. Otherwise, we're done with prepare.
		handle.mu.Lock()
		grew := len(handle.participants) > snapshotLen
		handle.mu.Unlock()
		if !grew {
			break
		}
	}

	// Grace window: after all current participants voted yes, wait briefly
	// for any concurrent AddParticipant calls to register a new voter. If
	// one arrives, restart the loop so the new participant gets a prepare
	// round. This implements the spec's UC-7 contract — "Add a new
	// participant to an in-progress transaction (only allowed in PREPARING
	// state)" — without forcing clients to make an explicit Finalize call.
	// The grace period is bounded by the transaction's own timeout budget.
	const graceWindow = 200 * time.Millisecond
graceLoop:
	for {
		// Did anyone add a participant after we exited the prepare loop?
		// The snapshot was the last len before the loop broke; if the
		// list has grown since, the new participant needs a vote.
		handle.mu.Lock()
		currentLen := len(handle.participants)
		// Re-count who hasn't voted yet (defensive — the loop above
		// should already cover this, but AddParticipant may have raced
		// with the loop's exit decision).
		unVoted := 0
		for _, op := range handle.participants {
			if _, ok := handle.votes[op.Name]; !ok {
				unVoted++
			}
		}
		handle.mu.Unlock()
		if unVoted > 0 {
			// A new participant was added after the loop. Go back to
			// the top of the prepare loop and run another round.
			for {
				select {
				case <-handle.cancel:
					c.abortTransaction(timeoutCtx, handle, "cancelled-before-prepare")
					return
				default:
				}
				handle.mu.Lock()
				participants := make([]ParticipantOp, 0, len(handle.participants))
				for _, op := range handle.participants {
					if _, hasVote := handle.votes[op.Name]; !hasVote {
						participants = append(participants, op)
					}
				}
				snapshotLen := len(handle.participants)
				handle.mu.Unlock()
				if len(participants) == 0 {
					break
				}
				var wg sync.WaitGroup
				var errMu sync.Mutex
				var prepareErrs []error
				for _, op := range participants {
					wg.Add(1)
					go func(op ParticipantOp) {
						defer wg.Done()
						select {
						case <-handle.cancel:
							errMu.Lock()
							prepareErrs = append(prepareErrs, errors.New("cancelled"))
							errMu.Unlock()
							return
						case <-timeoutCtx.Done():
							errMu.Lock()
							prepareErrs = append(prepareErrs, fmt.Errorf("prepare timeout"))
							errMu.Unlock()
							return
						default:
							vote, reason, err := op.Client.Prepare(timeoutCtx, handle.tx.ID, op.Action, op.Params)
							if err != nil {
								errMu.Lock()
								prepareErrs = append(prepareErrs, fmt.Errorf("prepare error: %w", err))
								errMu.Unlock()
								return
							}
							now := time.Now().UTC()
							voteStr := map[bool]string{true: "yes", false: "no"}[vote]
							handle.mu.Lock()
							handle.votes[op.Name] = voteInfo{Vote: voteStr, Reason: reason, At: now}
							handle.mu.Unlock()
							if err := c.log.RecordVote(context.Background(), handle.tx.ID, op.Name, voteStr, reason, now); err != nil {
								log.Printf("failed to record vote for %s: %v", op.Name, err)
							}
							metrics.RecordVote(op.Name, voteStr)
							if !vote {
								errMu.Lock()
								prepareErrs = append(prepareErrs, fmt.Errorf("participant voted no: %s", reason))
								errMu.Unlock()
								return
							}
						}
					}(op)
				}
				wg.Wait()
				if len(prepareErrs) > 0 {
					c.abortTransaction(timeoutCtx, handle, "prepare-failure")
					return
				}
				handle.mu.Lock()
				grew := len(handle.participants) > snapshotLen
				handle.mu.Unlock()
				if !grew {
					break
				}
			}
			continue graceLoop
		}
		_ = currentLen

		// Wait for the grace window. Poll for new participants every 10ms;
		// if one arrives, restart the grace window. If cancel fires, abort.
		timer := time.NewTimer(graceWindow)
		ticker := time.NewTicker(10 * time.Millisecond)
		graceDecided := false
		for !graceDecided {
			select {
			case <-handle.cancel:
				timer.Stop()
				ticker.Stop()
				c.abortTransaction(timeoutCtx, handle, "cancelled-during-grace")
				return
			case <-timer.C:
				ticker.Stop()
				graceDecided = true
			case <-ticker.C:
				// Re-check: did a new participant arrive? If so, the
				// outer loop will restart the grace window.
				handle.mu.Lock()
				grew := false
				for _, op := range handle.participants {
					if _, ok := handle.votes[op.Name]; !ok {
						grew = true
						break
					}
				}
				handle.mu.Unlock()
				if grew {
					timer.Stop()
					ticker.Stop()
					continue graceLoop
				}
			}
		}
		break graceLoop
	}

	// After grace: if cancel was signalled mid-flight, abort instead
	// of commit. Prepare errors are already handled above; this catches the
	// case where every prepare succeeded but the operator cancelled in the
	// same window.
	select {
	case <-handle.cancel:
		c.abortTransaction(timeoutCtx, handle, "cancelled-during-prepare")
		return
	default:
	}

	// All yes -> commit
	c.transitionState(handle, StateCommitting)
	c.commitTransaction(timeoutCtx, handle)
}

// commitTransaction sends commit to all participants
func (c *Coordinator) commitTransaction(ctx context.Context, handle *txHandle) {
	// Use a reasonable timeout for commit phase
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Snapshot participants to avoid data races
	handle.mu.Lock()
	participants := make([]ParticipantOp, len(handle.participants))
	copy(participants, handle.participants)
	handle.mu.Unlock()

	// Send commit to all participants concurrently with WaitGroup
	var wg sync.WaitGroup
	var commitErrMu sync.Mutex
	var commitErrs []error

	for _, op := range participants {
		wg.Add(1)
		go func(op ParticipantOp) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("panic in commit for %s: %v", op.Name, r)
					metrics.RecordError("commit_panic")
					commitErrMu.Lock()
					commitErrs = append(commitErrs, fmt.Errorf("panic in commit for %s: %v", op.Name, r))
					commitErrMu.Unlock()
				}
			}()

			select {
			case <-handle.cancel:
				commitErrMu.Lock()
				commitErrs = append(commitErrs, errors.New("cancelled during commit"))
				commitErrMu.Unlock()
				return
			case <-ctx.Done():
				commitErrMu.Lock()
				commitErrs = append(commitErrs, fmt.Errorf("commit timeout"))
				commitErrMu.Unlock()
				return
			default:
				if err := op.Client.Commit(ctx, handle.tx.ID); err != nil {
					commitErrMu.Lock()
					commitErrs = append(commitErrs, fmt.Errorf("commit error for %s: %w", op.Name, err))
					commitErrMu.Unlock()
					metrics.RecordError("commit_failed")
					return
				}
				// Record commit timestamp in memory and persistent log
				now := time.Now().UTC()
				handle.mu.Lock()
				if vi, ok := handle.votes[op.Name]; ok {
					vi.CommittedAt = &now
					handle.votes[op.Name] = vi
				}
				handle.mu.Unlock()

				// Update persistent log: set the outcome timestamp only,
				// keep the original vote (yes) intact. We use a separate
				// outcome parameter so the votes.vote column doesn't get
				// overwritten with "committed".
				if err := c.log.RecordOutcome(context.Background(), handle.tx.ID, op.Name, "committed", now); err != nil {
					log.Printf("failed to record commit for %s: %v", op.Name, err)
					metrics.RecordError("record_commit_failed")
				}
			}
		}(op)
	}

	// Wait for all commits to complete
	wg.Wait()

	if len(commitErrs) > 0 {
		// Some commits failed; we still consider it committed from coordinator's perspective,
		// but log error and mark as FAILED for visibility
		c.transitionState(handle, StateFailed)
		// Record error message
		c.log.UpdateStatus(ctx, handle.tx.ID, string(StateFailed), nil, "commit-failure", fmt.Sprintf("commit errors: %v", commitErrs))
		return
	}

	c.transitionState(handle, StateCommitted)
}

// abortTransaction sends abort to all participants that prepared
func (c *Coordinator) abortTransaction(ctx context.Context, handle *txHandle, reason string) {
	// Transition to ABORTING first (before sending aborts)
	c.transitionState(handle, StateAborting)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Snapshot participants to avoid data races
	handle.mu.Lock()
	participants := make([]ParticipantOp, len(handle.participants))
	copy(participants, handle.participants)
	handle.mu.Unlock()

	// Send abort to all participants that voted yes (prepared) with WaitGroup
	var wg sync.WaitGroup
	var abortErrMu sync.Mutex
	var abortErrs []error

	for _, op := range participants {
		wg.Add(1)
		go func(op ParticipantOp) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("panic in abort for %s: %v", op.Name, r)
					metrics.RecordError("abort_panic")
					abortErrMu.Lock()
					abortErrs = append(abortErrs, fmt.Errorf("panic in abort for %s: %v", op.Name, r))
					abortErrMu.Unlock()
				}
			}()

			select {
			case <-ctx.Done():
				abortErrMu.Lock()
				abortErrs = append(abortErrs, fmt.Errorf("abort timeout"))
				abortErrMu.Unlock()
				return
			default:
				if err := op.Client.Abort(ctx, handle.tx.ID); err != nil {
					abortErrMu.Lock()
					abortErrs = append(abortErrs, fmt.Errorf("abort error for %s: %w", op.Name, err))
					abortErrMu.Unlock()
					metrics.RecordError("abort_failed")
					return
				}
				// Record abort timestamp in memory and persistent log
				now := time.Now().UTC()
				handle.mu.Lock()
				if vi, ok := handle.votes[op.Name]; ok {
					vi.AbortedAt = &now
					handle.votes[op.Name] = vi
				}
				handle.mu.Unlock()

				// Update persistent log: set the outcome timestamp only,
				// keep the original vote (yes) intact. We use a separate
				// outcome parameter so the votes.vote column doesn't get
				// overwritten with "aborted".
				if err := c.log.RecordOutcome(context.Background(), handle.tx.ID, op.Name, "aborted", now); err != nil {
					log.Printf("failed to record abort for %s: %v", op.Name, err)
					metrics.RecordError("record_abort_failed")
				}

				// Record vote metric
				metrics.RecordVote(op.Name, "aborted")
			}
		}(op)
	}

	// Wait for all aborts to complete (but don't fail if some fail)
	wg.Wait()

	// Transition to ABORTED after all aborts sent
	c.transitionState(handle, StateAborted)
	// Use a fresh context for log writes — the passed-in ctx may be
	// expired (e.g. when abort is triggered by a prepare timeout) and
	// we still need to record the terminal state.
	logCtx := context.Background()
	if len(abortErrs) > 0 {
		// Log but don't block
		c.log.UpdateStatus(logCtx, handle.tx.ID, string(StateAborted), nil, "abort-partial-failure", fmt.Sprintf("some aborts failed: %v", abortErrs))
	} else {
		c.log.UpdateStatus(logCtx, handle.tx.ID, string(StateAborted), nil, reason, "")
	}
}

// transitionState updates the state in memory and persists to log
func (c *Coordinator) transitionState(handle *txHandle, newState State) {
	handle.state = newState
	now := time.Now().UTC()
	handle.tx.Status = string(newState)
	handle.tx.UpdatedAt = now

	// Persist state change (decision time for terminal states)
	var decisionAt *time.Time
	var decidedBy string
	if IsTerminal(newState) {
		t := now
		decisionAt = &t
		handle.tx.DecisionAt = decisionAt
		decidedBy = "protocol"
		handle.tx.DecidedBy = decidedBy
	}

	// Update transaction in log (async, ignore errors for now)
	go func() {
		ctx := context.Background()
		c.log.UpdateStatus(ctx, handle.tx.ID, string(newState), decisionAt, decidedBy, "")
	}()
}

// GetTransaction returns the full transaction state
func (c *Coordinator) GetTransaction(ctx context.Context, txID string) (*txnlog.TransactionWithVotes, error) {
	c.mu.RLock()
	handle, ok := c.inFlight[txID]
	c.mu.RUnlock()

	if ok {
		// Build TransactionWithVotes from in-memory handle
		tx := handle.tx
		var votes []txnlog.Vote
		handle.mu.Lock()
		for p, vi := range handle.votes {
			v := txnlog.Vote{
				Participant: p,
				Vote:        vi.Vote,
				Reason:      vi.Reason,
			}
			if vi.At.IsZero() {
				// skip
			} else if vi.Vote == "yes" {
				v.PreparedAt = &vi.At
			} else if vi.Vote == "committed" {
				v.CommittedAt = &vi.At
			} else {
				v.AbortedAt = &vi.At
			}
			votes = append(votes, v)
		}
		handle.mu.Unlock()
		return &txnlog.TransactionWithVotes{
			Transaction: *tx,
			Votes:       votes,
		}, nil
	}

	// Not in memory, fetch from log
	return c.log.GetTransaction(ctx, txID)
}

// ListTransactions returns recent transactions
func (c *Coordinator) ListTransactions(ctx context.Context, limit, offset int) ([]txnlog.TransactionWithVotes, error) {
	return c.log.ListTransactions(ctx, limit, offset)
}

// RecoverInFlight re-drives any non-terminal transactions after restart
func (c *Coordinator) RecoverInFlight(ctx context.Context) {
	metrics.RecordRecoveryAttempt()

	nonTerminal, err := c.log.FindNonTerminal(ctx)
	if err != nil {
		log.Printf("recovery: failed to find non-terminal transactions: %v", err)
		metrics.RecordError("recovery_find_failed")
		return
	}

	for _, tx := range nonTerminal {
		log.Printf("recovery: processing tx %s in state %s", tx.ID, tx.Status)

		// Load votes for this transaction
		votes, err := c.log.GetVotes(ctx, tx.ID)
		if err != nil {
			log.Printf("recovery: failed to get votes for %s: %v", tx.ID, err)
			metrics.RecordError("recovery_get_votes_failed")
			continue
		}

		// Determine the fate based on participant states
		// Query each participant to see what they did
		allPrepared := true
		anyCommitted := false
		anyAborted := false

		for _, vote := range votes {
			// If we already have a final outcome from a participant, use it
			if vote.Vote == "committed" {
				anyCommitted = true
			}
			if vote.Vote == "aborted" || vote.Vote == "no" || vote.Vote == "timeout" {
				anyAborted = true
			}
			// If vote is "yes" but no committed/aborted timestamp, we need to query
			if vote.Vote == "yes" && vote.CommittedAt == nil && vote.AbortedAt == nil {
				// Query participant to see if it eventually committed or aborted
				client, ok := c.participants[vote.Participant]
				if !ok {
					log.Printf("recovery: unknown participant %s for tx %s", vote.Participant, tx.ID)
					metrics.RecordError("recovery_unknown_participant")
					allPrepared = false
					continue
				}
				status, err := client.TxStatus(ctx, tx.ID)
				if err != nil {
					log.Printf("recovery: failed to query participant %s for tx %s: %v", vote.Participant, tx.ID, err)
					metrics.RecordError("recovery_query_participant_failed")
					allPrepared = false
					continue
				}
				switch status {
				case "committed":
					anyCommitted = true
					// Update our vote record
					now := time.Now().UTC()
					c.log.RecordVote(ctx, tx.ID, vote.Participant, "committed", "", now)
				case "aborted":
					anyAborted = true
					now := time.Now().UTC()
					c.log.RecordVote(ctx, tx.ID, vote.Participant, "aborted", "", now)
				default:
					// Still prepared or unknown - treat as uncertain
					allPrepared = false
				}
			}
		}

		// Decision logic:
		// - If any participant aborted or voted no -> ABORT
		// - If all participants are committed -> COMMITTED (already done)
		// - If all prepared and none aborted -> try to commit
		// - Otherwise (mixed or uncertain) -> FAILED (requires manual intervention)

		if anyAborted {
			// At least one participant aborted, so we must abort
			log.Printf("recovery: tx %s has aborted participants, sending abort", tx.ID)
			c.abortTransaction(ctx, &txHandle{
				tx:           &tx,
				participants: c.buildParticipantOps(),
				state:        StateAborting,
				cancel:       make(chan struct{}),
				done:         make(chan struct{}),
				mu:           sync.RWMutex{},
				votes:        make(map[string]voteInfo),
			}, "recovery-abort")
		} else if anyCommitted {
			// At least one committed, but not all - this is an inconsistent state
			// We can't safely commit the rest, so mark as FAILED
			log.Printf("recovery: tx %s has partial commits, marking FAILED", tx.ID)
			c.log.UpdateStatus(ctx, tx.ID, string(StateFailed), nil, "recovery", "partial commit detected")
		} else if allPrepared {
			// All participants are prepared (or we couldn't query them), try to commit
			log.Printf("recovery: tx %s all prepared, attempting commit", tx.ID)
			c.commitTransaction(ctx, &txHandle{
				tx:           &tx,
				participants: c.buildParticipantOps(),
				state:        StateCommitting,
				cancel:       make(chan struct{}),
				done:         make(chan struct{}),
				mu:           sync.RWMutex{},
				votes:        make(map[string]voteInfo),
			})
		} else {
			// Mixed or uncertain state - can't decide automatically
			log.Printf("recovery: tx %s in uncertain state, marking FAILED", tx.ID)
			c.log.UpdateStatus(ctx, tx.ID, string(StateFailed), nil, "recovery", "uncertain participant states")
		}
	}

	// If we got here without panicking, record recovery success
	metrics.RecordRecoverySuccess()
}

// buildParticipantOps creates ParticipantOp entries for all registered participants
// Note: This doesn't have the original actions/params, which would be needed for a full
// prepare recovery. In this implementation, we only use it for commit/abort which don't
// require actions, or for querying participant status.
func (c *Coordinator) buildParticipantOps() []ParticipantOp {
	ops := make([]ParticipantOp, 0, len(c.participants))
	for name, client := range c.participants {
		ops = append(ops, ParticipantOp{
			Name:    name,
			Client:  client,
		})
	}
	return ops
}
