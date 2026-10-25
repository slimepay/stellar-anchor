// Package sap is a client for the Stellar Anchor Platform's Platform API.
package sap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"aidmedium/stellar-anchor/internal/auth"
)

const (
	StatusIncomplete               = "incomplete"
	StatusPendingUserTransferStart = "pending_user_transfer_start"
	StatusPendingAnchor            = "pending_anchor"
	StatusPendingExternal          = "pending_external"
	StatusOnHold                   = "on_hold"
	StatusCompleted                = "completed"
	StatusError                    = "error"

	KindWithdrawal = "withdrawal"
)

type Amount struct {
	Amount string `json:"amount"`
	Asset  string `json:"asset,omitempty"`
}

type FeeDetails struct {
	Total string `json:"total"`
	Asset string `json:"asset"`
}

type StellarID struct {
	ID      string `json:"id,omitempty"`
	Account string `json:"account,omitempty"`
	Memo    string `json:"memo,omitempty"`
}

type Payment struct {
	ID                 string  `json:"id"`
	Amount             *Amount `json:"amount"`
	PaymentType        string  `json:"payment_type"`
	SourceAccount      string  `json:"source_account"`
	DestinationAccount string  `json:"destination_account"`
}

type StellarTransaction struct {
	ID       string    `json:"id"`
	Memo     string    `json:"memo"`
	MemoType string    `json:"memo_type"`
	Payments []Payment `json:"payments"`
}

type Transaction struct {
	ID                    string               `json:"id"`
	Sep                   string               `json:"sep"`
	Kind                  string               `json:"kind"`
	Status                string               `json:"status"`
	AmountExpected        *Amount              `json:"amount_expected,omitempty"`
	AmountIn              *Amount              `json:"amount_in,omitempty"`
	AmountOut             *Amount              `json:"amount_out,omitempty"`
	FeeDetails            *FeeDetails          `json:"fee_details,omitempty"`
	StartedAt             *time.Time           `json:"started_at,omitempty"`
	UpdatedAt             *time.Time           `json:"updated_at,omitempty"`
	StellarTransactions   []StellarTransaction `json:"stellar_transactions,omitempty"`
	SourceAccount         string               `json:"source_account,omitempty"`
	WithdrawAnchorAccount string               `json:"withdraw_anchor_account,omitempty"`
	ExternalTransactionID string               `json:"external_transaction_id,omitempty"`
	Memo                  string               `json:"memo,omitempty"`
	MemoType              string               `json:"memo_type,omitempty"`
	Creator               *StellarID           `json:"creator,omitempty"`
	Message               string               `json:"message,omitempty"`
}

// UnmarshalJSON accepts sep as either a string or a number, since SAP versions
// differ in how they serialise it.
func (t *Transaction) UnmarshalJSON(b []byte) error {
	type alias Transaction
	aux := struct {
		Sep json.RawMessage `json:"sep"`
		*alias
	}{alias: (*alias)(t)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	var s string
	if err := json.Unmarshal(aux.Sep, &s); err == nil {
		t.Sep = s
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(aux.Sep, &n); err == nil {
		t.Sep = n.String()
	}
	return nil
}

// RPCError is an error returned by SAP for a single JSON-RPC call.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("platform rpc error %d: %s", e.Code, e.Message)
}

type Client struct {
	baseURL string
	secret  string
	http    *http.Client
	nextID  atomic.Int64
	now     func() time.Time
}

func NewClient(baseURL, secret string) *Client {
	return &Client{
		baseURL: baseURL,
		secret:  secret,
		http:    &http.Client{Timeout: 20 * time.Second},
		now:     time.Now,
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	token, err := auth.PlatformToken(c.secret, c.now())
	if err != nil {
		return err
	}
	body, err := json.Marshal([]rpcRequest{{
		JSONRPC: "2.0",
		ID:      c.nextID.Add(1),
		Method:  method,
		Params:  params,
	}})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("platform %s: %w", method, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("platform %s: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("platform %s: http %d", method, resp.StatusCode)
	}

	var results []rpcResponse
	if err := json.Unmarshal(raw, &results); err != nil {
		return fmt.Errorf("platform %s: decode response: %w", method, err)
	}
	if len(results) != 1 {
		return fmt.Errorf("platform %s: expected 1 result, got %d", method, len(results))
	}
	if results[0].Error != nil {
		return results[0].Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(results[0].Result, out)
}

type txParams struct {
	TransactionID string `json:"transaction_id"`
	Message       string `json:"message,omitempty"`
}

func (c *Client) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	var tx Transaction
	if err := c.call(ctx, "get_transaction", txParams{TransactionID: id}, &tx); err != nil {
		return nil, err
	}
	return &tx, nil
}

// ListTransactions returns one page of transactions for a SEP in the given statuses.
func (c *Client) ListTransactions(ctx context.Context, sep string, statuses []string, page, pageSize int) ([]Transaction, error) {
	params := struct {
		Sep        string   `json:"sep"`
		Statuses   []string `json:"statuses,omitempty"`
		PageNumber int      `json:"page_number"`
		PageSize   int      `json:"page_size"`
	}{sep, statuses, page, pageSize}

	var out struct {
		Records []Transaction `json:"records"`
	}
	if err := c.call(ctx, "get_transactions", params, &out); err != nil {
		return nil, err
	}
	return out.Records, nil
}

type RequestOnchainFunds struct {
	TransactionID  string      `json:"transaction_id"`
	Message        string      `json:"message,omitempty"`
	AmountIn       *Amount     `json:"amount_in"`
	AmountOut      *Amount     `json:"amount_out"`
	FeeDetails     *FeeDetails `json:"fee_details"`
	AmountExpected *Amount     `json:"amount_expected,omitempty"`
}

// RequestOnchainFunds moves a withdrawal to pending_user_transfer_start. SAP
// assigns the memo and destination account itself.
func (c *Client) RequestOnchainFunds(ctx context.Context, p RequestOnchainFunds) (*Transaction, error) {
	var tx Transaction
	if err := c.call(ctx, "request_onchain_funds", p, &tx); err != nil {
		return nil, err
	}
	return &tx, nil
}

func (c *Client) NotifyOffchainFundsPending(ctx context.Context, id, externalID, message string) error {
	params := struct {
		txParams
		ExternalTransactionID string `json:"external_transaction_id,omitempty"`
	}{txParams{id, message}, externalID}
	return c.call(ctx, "notify_offchain_funds_pending", params, nil)
}

func (c *Client) NotifyOffchainFundsSent(ctx context.Context, id, externalID, message string) error {
	params := struct {
		txParams
		ExternalTransactionID string    `json:"external_transaction_id,omitempty"`
		FundsSentAt           time.Time `json:"funds_sent_at"`
	}{txParams{id, message}, externalID, c.now().UTC()}
	return c.call(ctx, "notify_offchain_funds_sent", params, nil)
}

func (c *Client) NotifyTransactionError(ctx context.Context, id, message string) error {
	return c.call(ctx, "notify_transaction_error", txParams{id, message}, nil)
}

func (c *Client) NotifyTransactionOnHold(ctx context.Context, id, message string) error {
	return c.call(ctx, "notify_transaction_on_hold", txParams{id, message}, nil)
}

// IsRPCError reports whether err is a JSON-RPC error returned by SAP, as
// opposed to a transport failure.
func IsRPCError(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr)
}
