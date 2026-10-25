// Package payout talks to the Slimepay core backend, which sends naira to bank
// accounts through SafeHaven.
package payout

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

var ErrNotFound = errors.New("payout not found")

// RejectedError means core refused the request outright, for example an
// account that cannot be resolved. Retrying the same request will not help.
type RejectedError struct{ Message string }

func (e *RejectedError) Error() string { return "core rejected request: " + e.Message }

type Bank struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type Request struct {
	TransactionID string `json:"transaction_id"`
	Amount        string `json:"amount"`
	BankCode      string `json:"bank_code"`
	AccountNumber string `json:"account_number"`
	Narration     string `json:"narration,omitempty"`
}

type Payout struct {
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
	Reference     string `json:"reference"`
	Amount        string `json:"amount"`
	BankCode      string `json:"bank_code"`
	AccountNumber string `json:"account_number"`
	Error         string `json:"error,omitempty"`
}

type Core interface {
	Banks(ctx context.Context) ([]Bank, error)
	ResolveAccount(ctx context.Context, bankCode, accountNumber string) (string, error)
	Pay(ctx context.Context, req Request) (*Payout, error)
	Get(ctx context.Context, transactionID string) (*Payout, error)
}

type Client struct {
	baseURL string
	secret  string
	http    *http.Client
}

func NewClient(baseURL, secret string) *Client {
	return &Client{baseURL: baseURL, secret: secret, http: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) Banks(ctx context.Context) ([]Bank, error) {
	var out struct {
		Banks []Bank `json:"banks"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/internal/anchor/banks", nil, &out); err != nil {
		return nil, err
	}
	return out.Banks, nil
}

func (c *Client) ResolveAccount(ctx context.Context, bankCode, accountNumber string) (string, error) {
	body := map[string]string{"bank_code": bankCode, "account_number": accountNumber}
	var out struct {
		AccountName string `json:"account_name"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/internal/anchor/resolve-account", body, &out); err != nil {
		return "", err
	}
	return out.AccountName, nil
}

// Pay asks core to pay out a withdrawal. Core keys payouts on the transaction
// ID, so calling Pay again for the same transaction never sends money twice.
func (c *Client) Pay(ctx context.Context, req Request) (*Payout, error) {
	var out Payout
	if err := c.do(ctx, http.MethodPost, "/api/internal/anchor/payouts", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Get(ctx context.Context, transactionID string) (*Payout, error) {
	var out Payout
	if err := c.do(ctx, http.MethodGet, "/api/internal/anchor/payouts/"+url.PathEscape(transactionID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("X-Anchor-Secret", c.secret)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("core %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("core %s %s: %w", method, path, err)
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return json.Unmarshal(raw, out)
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusBadRequest,
		resp.StatusCode == http.StatusConflict,
		resp.StatusCode == http.StatusUnprocessableEntity:
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return &RejectedError{Message: e.Error}
	default:
		return fmt.Errorf("core %s %s: http %d", method, path, resp.StatusCode)
	}
}
