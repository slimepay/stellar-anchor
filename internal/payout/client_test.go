package payout

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient(t *testing.T) {
	var lastBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Anchor-Secret") != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		lastBody = nil
		_ = json.NewDecoder(r.Body).Decode(&lastBody)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/internal/anchor/banks":
			_, _ = w.Write([]byte(`{"banks":[{"code":"000013","name":"GTBank"}]}`))
		case "POST /api/internal/anchor/resolve-account":
			if lastBody["account_number"] == "0000000000" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"error":"account not found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"account_name":"ADA OBI"}`))
		case "POST /api/internal/anchor/payouts":
			_, _ = w.Write([]byte(`{"transaction_id":"tx-1","status":"completed","reference":"ref-1","amount":"15000.00"}`))
		case "GET /api/internal/anchor/payouts/tx-1":
			_, _ = w.Write([]byte(`{"transaction_id":"tx-1","status":"processing","reference":"ref-1","amount":"15000.00"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	c := NewClient(srv.URL, "s3cret")

	banks, err := c.Banks(ctx)
	require.NoError(t, err)
	assert.Equal(t, []Bank{{Code: "000013", Name: "GTBank"}}, banks)

	name, err := c.ResolveAccount(ctx, "000013", "0123456789")
	require.NoError(t, err)
	assert.Equal(t, "ADA OBI", name)
	assert.Equal(t, "0123456789", lastBody["account_number"])

	_, err = c.ResolveAccount(ctx, "000013", "0000000000")
	var rejected *RejectedError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, "account not found", rejected.Message)

	p, err := c.Pay(ctx, Request{TransactionID: "tx-1", Amount: "15000.00", BankCode: "000013", AccountNumber: "0123456789"})
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, p.Status)
	assert.Equal(t, "tx-1", lastBody["transaction_id"])

	p, err = c.Get(ctx, "tx-1")
	require.NoError(t, err)
	assert.Equal(t, StatusProcessing, p.Status)

	_, err = c.Get(ctx, "unknown")
	assert.ErrorIs(t, err, ErrNotFound)

	_, err = NewClient(srv.URL, "wrong").Banks(ctx)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}
