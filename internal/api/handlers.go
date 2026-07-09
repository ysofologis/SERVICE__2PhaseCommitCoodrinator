package api

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelgrok/2phasecommit/internal/coord"
	"github.com/modelgrok/2phasecommit/internal/pax"
	"github.com/modelgrok/2phasecommit/internal/txnlog"
)

type Handler struct {
	coordinator *coord.Coordinator
	participants map[string]pax.ParticipantClient // name -> client
	templates   *template.Template
}

func NewHandler(coordinator *coord.Coordinator) *Handler {
	tmpl := template.Must(template.ParseGlob("web/templates/*.html"))
	return &Handler{
		coordinator: coordinator,
		participants: make(map[string]pax.ParticipantClient),
		templates:   tmpl,
	}
}

// RegisterParticipant adds a participant client for the coordinator to use
func (h *Handler) RegisterParticipant(name, baseURL string) {
	h.participants[name] = pax.NewHTTPClient(baseURL)
}

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	// TODO: check DB connectivity
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) CreateTransaction(w http.ResponseWriter, r *http.Request) {
	var req CreateTransactionRequest
	var err error

	// Support both JSON and form data
	if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" || r.Header.Get("Content-Type") == "multipart/form-data" {
		// Parse form data
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid form: %v", err))
			return
		}
		req.TopicKey = r.FormValue("topic_key")
		req.TimeoutMs, _ = strconv.Atoi(r.FormValue("timeout_ms"))
		participants := r.Form["participant[]"]
		actions := r.Form["action[]"]
		params := r.Form["params[]"]

		n := len(participants)
		req.Operations = make([]Operation, 0, n)
		for i := 0; i < n; i++ {
			var p map[string]interface{}
			if i < len(params) && params[i] != "" {
				json.Unmarshal([]byte(params[i]), &p)
			}
		req.Operations = append(req.Operations, Operation{
			Participant: participants[i],
			Action:      actions[i],
			Params:      p,
		})
		}
	} else {
		// JSON body
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
			return
		}
	}

	if req.TopicKey == "" {
		writeError(w, http.StatusBadRequest, "topic_key is required")
		return
	}

	if len(req.Operations) == 0 {
		writeError(w, http.StatusBadRequest, "at least one operation is required")
		return
	}

	// Convert operations to participantOps with clients
	ops := make([]coord.ParticipantOp, 0, len(req.Operations))
	for _, op := range req.Operations {
		client, ok := h.participants[op.Participant]
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown participant: %s", op.Participant))
			return
		}
		ops = append(ops, coord.ParticipantOp{
			Name:    op.Participant,
			Action:  op.Action,
			Params:  op.Params,
			Client:  client,
		})
	}

	timeoutMs := req.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = 10000
	}

	ctx := r.Context()
	txID, err := h.coordinator.StartTransaction(ctx, req.TopicKey, ops, timeoutMs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to start transaction: %v", err))
		return
	}

	// Get the transaction to return current state
	tx, err := h.coordinator.GetTransaction(ctx, txID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get transaction: %v", err))
		return
	}

	resp := CreateTransactionResponse{
		ID:       tx.ID,
		Status:   tx.Status,
		TopicKey: tx.TopicKey,
		Message:  "Transaction accepted, poll /transactions/{id} for status",
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (h *Handler) GetTransaction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	tx, err := h.coordinator.GetTransaction(ctx, id)
	if err != nil {
		if err == txnlog.ErrNotFound {
			writeError(w, http.StatusNotFound, "transaction not found")
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get transaction: %v", err))
		return
	}

	resp := TransactionResponse{
		ID:        tx.ID,
		TopicKey:  tx.TopicKey,
		Status:    tx.Status,
		CreatedAt: tx.CreatedAt.Format(time.RFC3339),
		UpdatedAt: tx.UpdatedAt.Format(time.RFC3339),
	}
	if tx.DecisionAt != nil {
		da := tx.DecisionAt.Format(time.RFC3339)
		resp.DecisionAt = &da
	}
	if tx.DecidedBy != "" {
		resp.DecidedBy = &tx.DecidedBy
	}
	if tx.Error != "" {
		resp.Error = &tx.Error
	}

	// Build participants array
	for _, v := range tx.Votes {
		ps := ParticipantStatus{
			Name: v.Participant,
			Vote: v.Vote,
		}
		if v.PreparedAt != nil {
			pa := v.PreparedAt.Format(time.RFC3339)
			ps.PreparedAt = &pa
		}
		if v.CommittedAt != nil {
			ca := v.CommittedAt.Format(time.RFC3339)
			ps.CommittedAt = &ca
		}
		if v.AbortedAt != nil {
			aa := v.AbortedAt.Format(time.RFC3339)
			ps.AbortedAt = &aa
		}
		resp.Participants = append(resp.Participants, ps)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListTransactions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit := 50
	offset := 0
	if ql := r.URL.Query().Get("limit"); ql != "" {
		if v, err := strconv.Atoi(ql); err == nil && v > 0 {
			limit = v
		}
	}
	if qo := r.URL.Query().Get("offset"); qo != "" {
		if v, err := strconv.Atoi(qo); err == nil && v >= 0 {
			offset = v
		}
	}

	txs, err := h.coordinator.ListTransactions(ctx, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to list transactions: %v", err))
		return
	}

	// Check if this is an HTMX request (for dashboard refresh)
	if r.Header.Get("HX-Request") == "true" {
		// Return just the table rows for HTMX
		type TxView struct {
			ID        string `json:"id"`
			TopicKey  string `json:"topic_key"`
			Status    string `json:"status"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
		}
		var view []TxView
		for _, tx := range txs {
			view = append(view, TxView{
				ID:        tx.ID,
				TopicKey:  tx.TopicKey,
				Status:    tx.Status,
				CreatedAt: tx.CreatedAt.Format("2006-01-02 15:04:05"),
				UpdatedAt: tx.UpdatedAt.Format("2006-01-02 15:04:05"),
			})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		h.templates.ExecuteTemplate(w, "tx_list.html", view)
		return
	}

	writeJSON(w, http.StatusOK, txs)
}

func (h *Handler) AddParticipant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req AddParticipantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	if req.Participant == "" || req.Action == "" {
		writeError(w, http.StatusBadRequest, "participant and action are required")
		return
	}

	client, ok := h.participants[req.Participant]
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown participant: %s", req.Participant))
		return
	}

	op := coord.ParticipantOp{
		Name:    req.Participant,
		Action:  req.Action,
		Params:  req.Params,
		Client:  client,
	}

	ctx := r.Context()
	if err := h.coordinator.AddParticipant(ctx, id, op); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to add participant: %v", err))
		return
	}

	// Return updated transaction
	tx, err := h.coordinator.GetTransaction(ctx, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get transaction: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, tx)
}

func (h *Handler) CancelTransaction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	ctx := r.Context()
	if err := h.coordinator.CancelTransaction(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to cancel transaction: %v", err))
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling"})
}

// HTMX frontend handlers

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	// Fetch recent transactions
	ctx := r.Context()
	txs, err := h.coordinator.ListTransactions(ctx, 50, 0)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list transactions: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert to template-friendly format
	type TxView struct {
		ID        string `json:"id"`
		TopicKey  string `json:"topic_key"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	var view []TxView
	for _, tx := range txs {
		view = append(view, TxView{
			ID:        tx.ID,
			TopicKey:  tx.TopicKey,
			Status:    tx.Status,
			CreatedAt: tx.CreatedAt.Format(time.RFC3339),
			UpdatedAt: tx.UpdatedAt.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.templates.ExecuteTemplate(w, "dashboard.html", view)
}

func (h *Handler) TxDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ctx := r.Context()

	tx, err := h.coordinator.GetTransaction(ctx, id)
	if err != nil {
		if err == txnlog.ErrNotFound {
			http.Error(w, "transaction not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("failed to get transaction: %v", err), http.StatusInternalServerError)
		return
	}

	// Convert to template-friendly format
	type ParticipantView struct {
		Name        string  `json:"name"`
		Vote        string  `json:"vote"`
		PreparedAt   *string `json:"prepared_at,omitempty"`
		CommittedAt  *string `json:"committed_at,omitempty"`
		AbortedAt    *string `json:"aborted_at,omitempty"`
	}
	type TxView struct {
		ID         string              `json:"id"`
		TopicKey   string              `json:"topic_key"`
		Status     string              `json:"status"`
		CreatedAt  string              `json:"created_at"`
		UpdatedAt  string              `json:"updated_at"`
		DecisionAt *string             `json:"decision_at,omitempty"`
		DecidedBy  *string             `json:"decided_by,omitempty"`
		Error      *string             `json:"error,omitempty"`
		Participants []ParticipantView `json:"participants"`
	}

	var pViews []ParticipantView
	for _, p := range tx.Votes {
		var pAt, cAt, aAt *string
		if p.PreparedAt != nil {
			t := p.PreparedAt.Format("2006-01-02 15:04:05")
			pAt = &t
		}
		if p.CommittedAt != nil {
			t := p.CommittedAt.Format("2006-01-02 15:04:05")
			cAt = &t
		}
		if p.AbortedAt != nil {
			t := p.AbortedAt.Format("2006-01-02 15:04:05")
			aAt = &t
		}
		pViews = append(pViews, ParticipantView{
			Name:        p.Participant,
			Vote:        p.Vote,
			PreparedAt:   pAt,
			CommittedAt:  cAt,
			AbortedAt:    aAt,
		})
	}

	view := TxView{
		ID:         tx.ID,
		TopicKey:   tx.TopicKey,
		Status:     tx.Status,
		CreatedAt:  tx.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:  tx.UpdatedAt.Format("2006-01-02 15:04:05"),
		Participants: pViews,
	}
	if tx.DecisionAt != nil {
		t := tx.DecisionAt.Format("2006-01-02 15:04:05")
		view.DecisionAt = &t
	}
	if tx.DecidedBy != "" {
		view.DecidedBy = &tx.DecidedBy
	}
	if tx.Error != "" {
		view.Error = &tx.Error
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.templates.ExecuteTemplate(w, "tx_detail.html", view)
}

func (h *Handler) NewTxForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.templates.ExecuteTemplate(w, "new_tx.html", nil)
}
