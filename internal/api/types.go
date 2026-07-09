package api

import (
	"encoding/json"
	"net/http"
)

type CreateTransactionRequest struct {
	TopicKey       string                 `json:"topic_key"`
	IdempotencyKey string                 `json:"idempotency_key,omitempty"`
	Operations     []Operation            `json:"operations"`
	TimeoutMs      int                    `json:"timeout_ms"`
}

type Operation struct {
	Participant string                 `json:"participant"`
	Action      string                 `json:"action"`
	Params      map[string]interface{} `json:"params"`
}

type CreateTransactionResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	TopicKey  string `json:"topic_key"`
	Message   string `json:"message,omitempty"`
}

type TransactionResponse struct {
	ID        string                 `json:"id"`
	TopicKey  string                 `json:"topic_key"`
	Status    string                 `json:"status"`
	CreatedAt string                 `json:"created_at"`
	UpdatedAt string                 `json:"updated_at"`
	DecisionAt *string               `json:"decision_at,omitempty"`
	DecidedBy  *string               `json:"decided_by,omitempty"`
	Error      *string               `json:"error,omitempty"`
	Phases     map[string]string     `json:"phases,omitempty"`
	Participants []ParticipantStatus `json:"participants,omitempty"`
}

type ParticipantStatus struct {
	Name         string  `json:"name"`
	Vote         string  `json:"vote"`
	Final        string  `json:"final,omitempty"`
	PreparedAt   *string `json:"prepared_at,omitempty"`
	CommittedAt  *string `json:"committed_at,omitempty"`
	AbortedAt    *string `json:"aborted_at,omitempty"`
}

type AddParticipantRequest struct {
	Participant string                 `json:"participant"`
	Action      string                 `json:"action"`
	Params      map[string]interface{} `json:"params"`
}

type CancelRequest struct {
	Reason string `json:"reason,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
