package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Transaction metrics
	TransactionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "2pc_transactions_total",
			Help: "Total number of transactions by final status",
		},
		[]string{"status"},
	)

	TransactionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "2pc_transaction_duration_seconds",
			Help:    "Duration of transactions from start to terminal state",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10), // 0.1s to ~100s
		},
		[]string{"status"},
	)

	// In-flight transactions gauge
	InFlightTransactions = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "2pc_in_flight_transactions",
			Help: "Number of transactions currently in non-terminal state",
		},
	)

	// Participant metrics
	ParticipantVotesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "2pc_participant_votes_total",
			Help: "Total votes by participant and vote type",
		},
		[]string{"participant", "vote"},
	)

	// Error metrics
	ErrorsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "2pc_errors_total",
			Help: "Total errors by type",
		},
		[]string{"type"},
	)

	// Recovery metrics
	RecoveryAttemptsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "2pc_recovery_attempts_total",
			Help: "Total number of recovery attempts",
		},
	)

	RecoverySuccessTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "2pc_recovery_success_total",
			Help: "Total number of successful recoveries",
		},
	)
)

// RecordTransactionStart records the start of a transaction
func RecordTransactionStart() {
	InFlightTransactions.Inc()
}

// RecordTransactionEnd records the end of a transaction with duration
func RecordTransactionEnd(status string, startTime time.Time) {
	duration := time.Since(startTime).Seconds()
	TransactionDuration.WithLabelValues(status).Observe(duration)
	TransactionsTotal.WithLabelValues(status).Inc()
	InFlightTransactions.Dec()
}

// RecordVote records a vote from a participant
func RecordVote(participant, vote string) {
	ParticipantVotesTotal.WithLabelValues(participant, vote).Inc()
}

// RecordError records an error
func RecordError(errorType string) {
	ErrorsTotal.WithLabelValues(errorType).Inc()
}

// RecordRecoveryAttempt records a recovery attempt
func RecordRecoveryAttempt() {
	RecoveryAttemptsTotal.Inc()
}

// RecordRecoverySuccess records a successful recovery
func RecordRecoverySuccess() {
	RecoverySuccessTotal.Inc()
}
