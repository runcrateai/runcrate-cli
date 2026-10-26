package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// Balance mirrors /api/v1/billing/balance (getProjectBalance), which returns
// camelCase creditsBalance + activeUsageCost. The snake_case fields are nil on
// servers that do not send them; RunwayHours is also nil when nothing is
// spending.
type Balance struct {
	Credits            float64  `json:"creditsBalance"`
	ActiveUsageCost    float64  `json:"activeUsageCost"`
	FloatingBalanceUsd *float64 `json:"floating_balance_usd,omitempty"`
	BurnUsdPerHour     *float64 `json:"burn_usd_per_hour,omitempty"`
	RunwayHours        *float64 `json:"runway_hours,omitempty"`
}

// Top-up statuses. Every one answers HTTP 200.
const (
	TopUpSucceeded      = "succeeded"
	TopUpProcessing     = "processing"
	TopUpRequiresAction = "requires_action"
	TopUpDeclined       = "declined"
)

// TopUp mirrors POST /api/v1/billing/topups. BalanceUsd is set when it
// succeeded; ConfirmURL and ExpiresAt when it needs a workspace owner or
// manager to confirm it; Reason and Message when it was declined.
type TopUp struct {
	ID             string   `json:"id"`
	Status         string   `json:"status"`
	AmountUsd      float64  `json:"amount_usd"`
	IdempotencyKey string   `json:"idempotency_key"`
	InvoiceNumber  *string  `json:"invoice_number"`
	BalanceUsd     *float64 `json:"balance_usd,omitempty"`
	ConfirmURL     string   `json:"confirm_url,omitempty"`
	ExpiresAt      *string  `json:"expires_at,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	Message        string   `json:"message,omitempty"`
	CreatedAt      string   `json:"created_at"`
	// Replayed is true when the key had been sent before: the answer is that
	// top-up's current status, not a new charge.
	Replayed bool `json:"-"`
}

// AutoRecharge mirrors /api/v1/billing/auto-recharge: below ThresholdUsd the
// balance is topped up to TargetUsd from the default card.
type AutoRecharge struct {
	Enabled           bool     `json:"enabled"`
	ThresholdUsd      *float64 `json:"threshold_usd"`
	TargetUsd         *float64 `json:"target_usd"`
	LastFailureReason *string  `json:"last_failure_reason"`
	LastFailureAt     *string  `json:"last_failure_at"`
}

// PaymentMethod mirrors one entry of /api/v1/billing/payment-methods.
type PaymentMethod struct {
	Type     string  `json:"type"`
	Brand    *string `json:"brand"`
	Last4    *string `json:"last4"`
	ExpMonth *int    `json:"exp_month"`
	ExpYear  *int    `json:"exp_year"`
	Default  bool    `json:"default"`
}

// UsageSummary mirrors the get_usage_summary RPC behind /api/v1/billing/usage.
// This is inference/token usage; compute charges live in billing transactions.
type UsageSummary struct {
	TotalRequests         int     `json:"total_requests"`
	TotalPromptTokens     int     `json:"total_prompt_tokens"`
	TotalCompletionTokens int     `json:"total_completion_tokens"`
	TotalTokens           int     `json:"total_tokens"`
	TotalCost             float64 `json:"total_cost"`
}

func (c *Client) GetBalance() (*Balance, error) {
	resp, err := c.do("GET", "/api/v1/billing/balance", nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[Balance]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// GetUsage returns the inference usage summary. `from` is an optional RFC3339
// lower bound (the server filters on from/to, not a "period" string).
func (c *Client) GetUsage(from string) (*UsageSummary, error) {
	path := "/api/v1/billing/usage"
	if from != "" {
		path += "?from=" + url.QueryEscape(from)
	}
	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[UsageSummary]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// TopUp charges amount (USD) to the workspace's default card and adds it to
// the balance. An empty idempotencyKey gets a fresh one, returned on the
// result. Sending the same key again answers that top-up's current status
// instead of charging again, which is how a processing or requires_action
// top-up is checked. When no answer came, the error is a *SubmitNetworkError
// carrying the key to send again.
func (c *Client) TopUp(amount float64, idempotencyKey string) (*TopUp, error) {
	if idempotencyKey == "" {
		idempotencyKey = NewIdempotencyKey()
	}
	raw, err := json.Marshal(map[string]any{"amount_usd": amount, "idempotency_key": idempotencyKey})
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}
	resp, err := c.doCtxHeaders(context.Background(), "POST", "/api/v1/billing/topups", bytes.NewReader(raw), nil)
	if err != nil {
		return nil, &SubmitNetworkError{Key: idempotencyKey, Err: err}
	}
	defer resp.Body.Close()
	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[TopUp]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	t := &result.Data
	if t.IdempotencyKey == "" {
		t.IdempotencyKey = idempotencyKey
	}
	t.Replayed = resp.Header.Get("Idempotent-Replayed") == "true"
	return t, nil
}

// GetAutoRecharge returns the workspace's auto-recharge setting.
func (c *Client) GetAutoRecharge() (*AutoRecharge, error) {
	return callJSON[AutoRecharge](c, "GET", "/api/v1/billing/auto-recharge", nil)
}

// SetAutoRecharge turns auto-recharge on or off. The server needs threshold
// and target even to turn it off.
func (c *Client) SetAutoRecharge(enabled bool, threshold, target float64) (*AutoRecharge, error) {
	body := map[string]any{"enabled": enabled, "threshold_usd": threshold, "target_usd": target}
	return callJSON[AutoRecharge](c, "PUT", "/api/v1/billing/auto-recharge", body)
}

// ListPaymentMethods returns the workspace's cards.
func (c *Client) ListPaymentMethods() ([]PaymentMethod, error) {
	list, err := callJSON[[]PaymentMethod](c, "GET", "/api/v1/billing/payment-methods", nil)
	if err != nil {
		return nil, err
	}
	return *list, nil
}
