package txnlog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
)

type SQLiteLog struct {
	db *sql.DB
}

func NewSQLiteLog(path string) (*SQLiteLog, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable WAL
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	// Set busy timeout to reduce "database is locked" errors
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	// Create tables
	ctx := context.Background()
	if err := createTables(ctx, db); err != nil {
		return nil, fmt.Errorf("create tables: %w", err)
	}

	return &SQLiteLog{db: db}, nil
}

func createTables(ctx context.Context, db *sql.DB) error {
	// Core transaction record
	createTx := `
	CREATE TABLE IF NOT EXISTS transactions (
		id TEXT PRIMARY KEY,
		topic_key TEXT NOT NULL,
		status TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL,
		updated_at TIMESTAMP NOT NULL,
		decision_at TIMESTAMP,
		decided_by TEXT,
		error_message TEXT
	);
	`
	if _, err := db.ExecContext(ctx, createTx); err != nil {
		return fmt.Errorf("create transactions: %w", err)
	}

	// Partial unique index for in-flight deduplication
	createIdx := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_topic_inflight 
	ON transactions(topic_key) 
	WHERE status NOT IN ('COMMITTED','ABORTED','FAILED');
	`
	if _, err := db.ExecContext(ctx, createIdx); err != nil {
		return fmt.Errorf("create idx_transactions_topic_inflight: %w", err)
	}

	// Votes table
	createVotes := `
	CREATE TABLE IF NOT EXISTS votes (
		tx_id TEXT NOT NULL,
		participant TEXT NOT NULL,
		vote TEXT NOT NULL,
		reason TEXT,
		prepared_at TIMESTAMP,
		committed_at TIMESTAMP,
		aborted_at TIMESTAMP,
		PRIMARY KEY (tx_id, participant),
		FOREIGN KEY (tx_id) REFERENCES transactions(id) ON DELETE CASCADE
	);
	`
	if _, err := db.ExecContext(ctx, createVotes); err != nil {
		return fmt.Errorf("create votes: %w", err)
	}

	// Indexes
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_votes_tx_id ON votes(tx_id);`); err != nil {
		return fmt.Errorf("create idx_votes_tx_id: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_transactions_updated ON transactions(updated_at);`); err != nil {
		return fmt.Errorf("create idx_transactions_updated: %w", err)
	}

	return nil
}

func (l *SQLiteLog) CreateTransaction(ctx context.Context, tx *Transaction) error {
	now := time.Now().UTC()
	tx.CreatedAt = now
	tx.UpdatedAt = now

	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = l.db.ExecContext(
			ctx,
			`INSERT INTO transactions (id, topic_key, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			tx.ID, tx.TopicKey, tx.Status, tx.CreatedAt, tx.UpdatedAt,
		)
		if err == nil {
			return nil
		}
		if strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "SQLITE_BUSY") {
			if attempt < 2 {
				time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
				continue
			}
		}
		return fmt.Errorf("insert transaction: %w", err)
	}
	return fmt.Errorf("insert transaction after retries: %w", err)
}

func (l *SQLiteLog) UpdateStatus(ctx context.Context, id, status string, decisionAt *time.Time, decidedBy, errMsg string) error {
	now := time.Now().UTC()
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = l.db.ExecContext(
			ctx,
			`UPDATE transactions SET status = ?, updated_at = ?, decision_at = ?, decided_by = ?, error_message = ? WHERE id = ?`,
			status, now, decisionAt, decidedBy, errMsg, id,
		)
		if err == nil {
			return nil
		}
		if strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "SQLITE_BUSY") {
			if attempt < 2 {
				time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
				continue
			}
		}
		return fmt.Errorf("update transaction: %w", err)
	}
	return fmt.Errorf("update transaction after retries: %w", err)
}

func (l *SQLiteLog) RecordVote(ctx context.Context, txID, participant, vote, reason string, ts time.Time) error {
	// Retry up to 3 times with exponential backoff for SQLITE_BUSY
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		// Upsert vote
		_, err = l.db.ExecContext(
			ctx,
			`INSERT INTO votes (tx_id, participant, vote, reason, prepared_at, committed_at, aborted_at) 
			 VALUES (?, ?, ?, ?, ?, ?, ?) 
			 ON CONFLICT(tx_id, participant) DO UPDATE SET 
			 vote = excluded.vote,
			 reason = excluded.reason,
			 prepared_at = COALESCE(votes.prepared_at, excluded.prepared_at),
			 committed_at = COALESCE(votes.committed_at, excluded.committed_at),
			 aborted_at = COALESCE(votes.aborted_at, excluded.aborted_at)`,
			txID, participant, vote, reason,
			func() interface{} {
				if vote == "yes" {
					return ts
				}
				return nil
			}(),
			func() interface{} {
				if vote == "committed" {
					return ts
				}
				return nil
			}(),
			func() interface{} {
				if vote == "no" || vote == "timeout" || vote == "aborted" {
					return ts
				}
				return nil
			}(),
		)
		if err == nil {
			return nil
		}
		// Check if error is SQLITE_BUSY; if not, fail immediately
		if strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "SQLITE_BUSY") {
			if attempt < 2 {
				time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
				continue
			}
		}
		return fmt.Errorf("upsert vote: %w", err)
	}
	return fmt.Errorf("upsert vote after retries: %w", err)
}

func (l *SQLiteLog) RecordOutcome(ctx context.Context, txID, participant, outcome string, ts time.Time) error {
	// Update only the timestamp column matching the outcome. Keep the
	// votes.vote column as-is so the original vote (yes/no/timeout) is
	// preserved. We deliberately don't touch the reason column.
	col := "aborted_at"
	if outcome == "committed" {
		col = "committed_at"
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		_, err = l.db.ExecContext(
			ctx,
			`UPDATE votes SET `+col+` = ? WHERE tx_id = ? AND participant = ?`,
			ts, txID, participant,
		)
		if err == nil {
			return nil
		}
		if strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "SQLITE_BUSY") {
			if attempt < 2 {
				time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
				continue
			}
		}
		return fmt.Errorf("record outcome: %w", err)
	}
	return fmt.Errorf("record outcome after retries: %w", err)
}

func (l *SQLiteLog) GetTransaction(ctx context.Context, id string) (*TransactionWithVotes, error) {
	tx := &Transaction{}
	err := l.db.QueryRowContext(
		ctx,
		`SELECT id, topic_key, status, created_at, updated_at, decision_at, decided_by, error_message 
		 FROM transactions WHERE id = ?`,
		id,
	).Scan(
		&tx.ID, &tx.TopicKey, &tx.Status, &tx.CreatedAt, &tx.UpdatedAt,
		func() interface{} {
			var da sql.NullTime
			return &da
		}(),
		func() interface{} {
			var db sql.NullString
			return &db
		}(),
		func() interface{} {
			var err sql.NullString
			return &err
		}(),
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query transaction: %w", err)
	}

	// Load votes
	rows, err := l.db.QueryContext(ctx, `SELECT participant, vote, reason, prepared_at, committed_at, aborted_at FROM votes WHERE tx_id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("query votes: %w", err)
	}
	defer rows.Close()

	var twv TransactionWithVotes
	twv.Transaction = *tx
	for rows.Next() {
		var v Vote
		var preparedAt, committedAt, abortedAt sql.NullTime
		err := rows.Scan(
			&v.Participant, &v.Vote, &v.Reason,
			&preparedAt, &committedAt, &abortedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan vote: %w", err)
		}
		if preparedAt.Valid {
			t := preparedAt.Time
			v.PreparedAt = &t
		}
		if committedAt.Valid {
			t := committedAt.Time
			v.CommittedAt = &t
		}
		if abortedAt.Valid {
			t := abortedAt.Time
			v.AbortedAt = &t
		}
		twv.Votes = append(twv.Votes, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vote rows err: %w", err)
	}

	return &twv, nil
}

func (l *SQLiteLog) GetVotes(ctx context.Context, txID string) ([]Vote, error) {
	rows, err := l.db.QueryContext(
		ctx,
		`SELECT participant, vote, reason, prepared_at, committed_at, aborted_at FROM votes WHERE tx_id = ?`,
		txID,
	)
	if err != nil {
		return nil, fmt.Errorf("query votes: %w", err)
	}
	defer rows.Close()

	var votes []Vote
	for rows.Next() {
		var v Vote
		var preparedAt, committedAt, abortedAt sql.NullTime
		err := rows.Scan(
			&v.Participant, &v.Vote, &v.Reason,
			&preparedAt, &committedAt, &abortedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan vote: %w", err)
		}
		if preparedAt.Valid {
			t := preparedAt.Time
			v.PreparedAt = &t
		}
		if committedAt.Valid {
			t := committedAt.Time
			v.CommittedAt = &t
		}
		if abortedAt.Valid {
			t := abortedAt.Time
			v.AbortedAt = &t
		}
		votes = append(votes, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vote rows err: %w", err)
	}
	return votes, nil
}

func (l *SQLiteLog) ListTransactions(ctx context.Context, limit, offset int) ([]TransactionWithVotes, error) {
	rows, err := l.db.QueryContext(
		ctx,
		`SELECT id, topic_key, status, created_at, updated_at, decision_at, decided_by, error_message 
		 FROM transactions ORDER BY updated_at DESC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()

	var txs []TransactionWithVotes
	for rows.Next() {
		tx := &Transaction{}
		var decisionAt sql.NullTime
		var decidedBy sql.NullString
		var errorMsg sql.NullString
		err := rows.Scan(
			&tx.ID, &tx.TopicKey, &tx.Status, &tx.CreatedAt, &tx.UpdatedAt,
			&decisionAt, &decidedBy, &errorMsg,
		)
		if err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		if decisionAt.Valid {
			t := decisionAt.Time
			tx.DecisionAt = &t
		}
		if decidedBy.Valid {
			tx.DecidedBy = decidedBy.String
		}
		if errorMsg.Valid {
			tx.Error = errorMsg.String
		}

		// Load votes for this transaction
		voteRows, err := l.db.QueryContext(ctx, `SELECT participant, vote, reason, prepared_at, committed_at, aborted_at FROM votes WHERE tx_id = ?`, tx.ID)
		if err != nil {
			return nil, fmt.Errorf("query votes for tx %s: %w", tx.ID, err)
		}
		var votes []Vote
		for voteRows.Next() {
			var v Vote
			var preparedAt, committedAt, abortedAt sql.NullTime
			err := voteRows.Scan(
				&v.Participant, &v.Vote, &v.Reason,
				&preparedAt, &committedAt, &abortedAt,
			)
			if err != nil {
				voteRows.Close()
				return nil, fmt.Errorf("scan vote: %w", err)
			}
			if preparedAt.Valid {
				t := preparedAt.Time
				v.PreparedAt = &t
			}
			if committedAt.Valid {
				t := committedAt.Time
				v.CommittedAt = &t
			}
			if abortedAt.Valid {
				t := abortedAt.Time
				v.AbortedAt = &t
			}
			votes = append(votes, v)
		}
		voteRows.Close()

		txs = append(txs, TransactionWithVotes{
			Transaction: *tx,
			Votes:       votes,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rows err: %w", err)
	}
	return txs, nil
}

func (l *SQLiteLog) FindNonTerminal(ctx context.Context) ([]Transaction, error) {
	rows, err := l.db.QueryContext(
		ctx,
		`SELECT id, topic_key, status, created_at, updated_at FROM transactions 
		 WHERE status NOT IN ('COMMITTED','ABORTED','FAILED')`,
	)
	if err != nil {
		return nil, fmt.Errorf("find non-terminal: %w", err)
	}
	defer rows.Close()

	var txs []Transaction
	for rows.Next() {
		var tx Transaction
		err := rows.Scan(&tx.ID, &tx.TopicKey, &tx.Status, &tx.CreatedAt, &tx.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan non-terminal: %w", err)
		}
		txs = append(txs, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("non-terminal rows err: %w", err)
	}
	return txs, nil
}

func (l *SQLiteLog) FindInFlightByTopic(ctx context.Context, topicKey string) (*Transaction, error) {
	tx := &Transaction{}
	err := l.db.QueryRowContext(
		ctx,
		`SELECT id, topic_key, status, created_at, updated_at FROM transactions 
		 WHERE topic_key = ? AND status NOT IN ('COMMITTED','ABORTED','FAILED') 
		 LIMIT 1`,
		topicKey,
	).Scan(&tx.ID, &tx.TopicKey, &tx.Status, &tx.CreatedAt, &tx.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find in-flight by topic: %w", err)
	}
	return tx, nil
}

func (l *SQLiteLog) Close() error {
	return l.db.Close()
}
