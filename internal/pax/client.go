package pax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ParticipantClient interface {
	Prepare(ctx context.Context, txID string, action string, params map[string]interface{}) (bool, string, error)
	Commit(ctx context.Context, txID string) error
	Abort(ctx context.Context, txID string) error
	TxStatus(ctx context.Context, txID string) (string, error)
}

type HTTPClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// BaseURL returns the base URL of this client
func (c *HTTPClient) BaseURL() string {
	return c.baseURL
}

type prepareRequest struct {
	Action  string                 `json:"action"`
	Params  map[string]interface{} `json:"params"`
	TxID    string                 `json:"tx_id"`
}

type prepareResponse struct {
	Vote   string `json:"vote"`   // "yes" or "no"
	Reason string `json:"reason"` // optional
}

func (c *HTTPClient) Prepare(ctx context.Context, txID string, action string, params map[string]interface{}) (bool, string, error) {
	reqBody := prepareRequest{
		Action: action,
		Params: params,
		TxID:   txID,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return false, "", fmt.Errorf("marshal prepare request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/prepare", bytes.NewReader(body))
	if err != nil {
		return false, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Transaction-ID", txID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return false, "", fmt.Errorf("prepare failed: %s %s", resp.Status, string(b))
	}

	var pr prepareResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return false, "", fmt.Errorf("decode prepare response: %w", err)
	}

	if pr.Vote == "yes" {
		return true, pr.Reason, nil
	}
	return false, pr.Reason, nil
}

func (c *HTTPClient) Commit(ctx context.Context, txID string) error {
	body := []byte(`{"tx_id":"` + txID + `"}`)
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/commit", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create commit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Transaction-ID", txID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do commit request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("commit failed: %s %s", resp.Status, string(b))
	}
	return nil
}

func (c *HTTPClient) Abort(ctx context.Context, txID string) error {
	body := []byte(`{"tx_id":"` + txID + `"}`)
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/abort", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create abort request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Transaction-ID", txID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("do abort request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("abort failed: %s %s", resp.Status, string(b))
	}
	return nil
}

type txStatusResponse struct {
	Status string `json:"status"` // "prepared", "committed", "aborted", "unknown"
}

func (c *HTTPClient) TxStatus(ctx context.Context, txID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/tx/"+txID, nil)
	if err != nil {
		return "", fmt.Errorf("create tx status request: %w", err)
	}
	req.Header.Set("X-Transaction-ID", txID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("do tx status request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("tx status failed: %s %s", resp.Status, string(b))
	}

	var tsr txStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&tsr); err != nil {
		return "", fmt.Errorf("decode tx status response: %w", err)
	}
	return tsr.Status, nil
}
