package txnlog

import (
	"context"
	"time"
)

type Transaction struct {
	ID        string    `json:"id"`
	TopicKey  string    `json:"topic_key"`
	Status    string    `json:"status"` // INIT, PREPARING, COMMITTING, COMMITTED, ABORTING, ABORTED, FAILED
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	DecisionAt *time.Time `json:"decision_at,omitempty"`
	DecidedBy  string     `json:"decided_by,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type Vote struct {
	Participant  string     `json:"participant"`
	Vote         string     `json:"vote"` // yes, no, timeout
	Reason       string     `json:"reason,omitempty"`
	PreparedAt   *time.Time `json:"prepared_at,omitempty"`
	CommittedAt  *time.Time `json:"committed_at,omitempty"`
	AbortedAt    *time.Time `json:"aborted_at,omitempty"`
}

type TransactionWithVotes struct {
	Transaction
	Votes []Vote `json:"votes"`
}

type Log interface {
	CreateTransaction(ctx context.Context, tx *Transaction) error
	UpdateStatus(ctx context.Context, id string, status string, decisionAt *time.Time, decidedBy, errMsg string) error
	RecordVote(ctx context.Context, txID, participant, vote, reason string, ts time.Time) error
	// RecordOutcome sets the final outcome (committed/aborted) on an existing
	// vote row without overwriting the votes.vote column. The vote column
	// retains its original yes/no/timeout value; only the timestamp column
	// matching the outcome is set.
	RecordOutcome(ctx context.Context, txID, participant, outcome string, ts time.Time) error
	GetTransaction(ctx context.Context, id string) (*TransactionWithVotes, error)
	GetVotes(ctx context.Context, txID string) ([]Vote, error)
	ListTransactions(ctx context.Context, limit, offset int) ([]TransactionWithVotes, error)
	FindNonTerminal(ctx context.Context) ([]Transaction, error)
	FindInFlightByTopic(ctx context.Context, topicKey string) (*Transaction, error)
	Close() error
}
