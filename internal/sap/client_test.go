package sap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const secret = "0123456789abcdef0123456789abcdef"

type captured struct {
	Method string
	Params map[string]any
}

func server(t *testing.T, result string, rpcErr string) (*Client, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/", r.URL.Path)

		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return []byte(secret), nil },
			jwt.WithAudience("platform_api"), jwt.WithExpirationRequired())
		assert.NoError(t, err, "platform token")

		body, _ := io.ReadAll(r.Body)
		var reqs []map[string]any
		require.NoError(t, json.Unmarshal(body, &reqs))
		require.Len(t, reqs, 1)
		assert.Equal(t, "2.0", reqs[0]["jsonrpc"])
		got.Method = reqs[0]["method"].(string)
		got.Params, _ = reqs[0]["params"].(map[string]any)

		w.Header().Set("Content-Type", "application/json")
		if rpcErr != "" {
			_, _ = w.Write([]byte(`[{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"` + rpcErr + `"}}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"jsonrpc":"2.0","id":1,"result":` + result + `}]`))
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, secret), got
}

func TestGetTransaction(t *testing.T) {
	c, got := server(t, `{"id":"tx-1","sep":24,"kind":"withdrawal","status":"incomplete",
		"amount_expected":{"amount":"10","asset":"stellar:native"},
		"creator":{"account":"GABC","memo":"5"},
		"stellar_transactions":[{"id":"hash","memo":"99","memo_type":"id","payments":[{"id":"p1","amount":{"amount":"10","asset":"stellar:native"},"destination_account":"GDIST"}]}]}`, "")

	tx, err := c.GetTransaction(context.Background(), "tx-1")
	require.NoError(t, err)
	assert.Equal(t, "get_transaction", got.Method)
	assert.Equal(t, "tx-1", got.Params["transaction_id"])
	assert.Equal(t, "24", tx.Sep)
	assert.Equal(t, "withdrawal", tx.Kind)
	assert.Equal(t, "GABC", tx.Creator.Account)
	assert.Equal(t, "10", tx.StellarTransactions[0].Payments[0].Amount.Amount)
}

func TestSepAsString(t *testing.T) {
	c, _ := server(t, `{"id":"tx-1","sep":"6"}`, "")
	tx, err := c.GetTransaction(context.Background(), "tx-1")
	require.NoError(t, err)
	assert.Equal(t, "6", tx.Sep)
}

func TestListTransactions(t *testing.T) {
	c, got := server(t, `{"records":[{"id":"a"},{"id":"b"}]}`, "")
	txs, err := c.ListTransactions(context.Background(), "24", []string{StatusPendingAnchor}, 0, 50)
	require.NoError(t, err)
	assert.Len(t, txs, 2)
	assert.Equal(t, "get_transactions", got.Method)
	assert.Equal(t, "24", got.Params["sep"])
	assert.Equal(t, []any{"pending_anchor"}, got.Params["statuses"])
	assert.Equal(t, float64(50), got.Params["page_size"])
}

func TestRequestOnchainFunds(t *testing.T) {
	c, got := server(t, `{"id":"tx-1","status":"pending_user_transfer_start","memo":"123456","memo_type":"id"}`, "")
	tx, err := c.RequestOnchainFunds(context.Background(), RequestOnchainFunds{
		TransactionID:  "tx-1",
		AmountIn:       &Amount{"10.0000000", "stellar:native"},
		AmountOut:      &Amount{"15000.00", "iso4217:NGN"},
		FeeDetails:     &FeeDetails{"0", "stellar:native"},
		AmountExpected: &Amount{Amount: "10.0000000"},
	})
	require.NoError(t, err)
	assert.Equal(t, "123456", tx.Memo)
	assert.Equal(t, "request_onchain_funds", got.Method)
	assert.Equal(t, map[string]any{"amount": "15000.00", "asset": "iso4217:NGN"}, got.Params["amount_out"])
	assert.Equal(t, map[string]any{"total": "0", "asset": "stellar:native"}, got.Params["fee_details"])
	assert.NotContains(t, got.Params, "memo", "SAP assigns the memo")
}

func TestNotifyMethods(t *testing.T) {
	ctx := context.Background()

	c, got := server(t, `{}`, "")
	require.NoError(t, c.NotifyOffchainFundsPending(ctx, "tx-1", "ref-1", "m"))
	assert.Equal(t, "notify_offchain_funds_pending", got.Method)
	assert.Equal(t, "ref-1", got.Params["external_transaction_id"])

	require.NoError(t, c.NotifyOffchainFundsSent(ctx, "tx-1", "ref-1", "m"))
	assert.Equal(t, "notify_offchain_funds_sent", got.Method)
	assert.NotEmpty(t, got.Params["funds_sent_at"])

	require.NoError(t, c.NotifyTransactionError(ctx, "tx-1", "boom"))
	assert.Equal(t, "notify_transaction_error", got.Method)
	assert.Equal(t, "boom", got.Params["message"])

	require.NoError(t, c.NotifyTransactionOnHold(ctx, "tx-1", "review"))
	assert.Equal(t, "notify_transaction_on_hold", got.Method)
}

func TestRPCError(t *testing.T) {
	c, _ := server(t, "", "invalid status")
	err := c.NotifyTransactionError(context.Background(), "tx-1", "x")
	require.Error(t, err)
	assert.True(t, IsRPCError(err))
	assert.Contains(t, err.Error(), "invalid status")
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	err := NewClient(srv.URL, secret).NotifyTransactionError(context.Background(), "tx-1", "x")
	require.Error(t, err)
	assert.False(t, IsRPCError(err))
}
