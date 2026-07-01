package sep31_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/middleware"
	"aidmedium/stellar-anchor/internal/sep31"

	"github.com/go-chi/chi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccount = "GDQERENWDDSQZS7R7WKHZI3BSOYMV3FSWR7TFUYFTKQ447PIX6NREOJM"

func testConfig() *config.Config {
	return &config.Config{
		USDCIssuer:      "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5",
		DistributionKey: "GDIST0000EXAMPLEKEY000000000000000000000000000000000000000",
	}
}

type mockStore struct {
	records  map[string]*sep31.Transaction
	failNext error
}

func newMock() *mockStore { return &mockStore{records: make(map[string]*sep31.Transaction)} }

func (m *mockStore) Insert(_ context.Context, tx *sep31.Transaction) error {
	if m.failNext != nil {
		err := m.failNext; m.failNext = nil; return err
	}
	m.records[tx.ID] = tx
	return nil
}

func (m *mockStore) FindByID(_ context.Context, id string) (*sep31.Transaction, error) {
	if m.failNext != nil {
		err := m.failNext; m.failNext = nil; return nil, err
	}
	tx, ok := m.records[id]
	if !ok {
		return nil, nil
	}
	return tx, nil
}

func withAccount(r *http.Request, account string) *http.Request {
	return r.WithContext(middleware.WithStellarAccount(r.Context(), account))
}

// routeWithID wraps the handler in a chi router so chi.URLParam works in tests.
func routeWithID(h *sep31.Handler) http.Handler {
	r := chi.NewRouter()
	r.Get("/sep31/transactions/{id}", h.GetTransaction)
	return r
}

func TestInfo(t *testing.T) {
	h := sep31.NewHandler(newMock(), testConfig())
	req := httptest.NewRequest(http.MethodGet, "/sep31/info", nil)
	w := httptest.NewRecorder()
	h.Info(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	receive, ok := body["receive"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, receive, "USDC")

	usdc := receive["USDC"].(map[string]any)
	assert.Equal(t, true, usdc["enabled"])
	assert.Contains(t, usdc, "fields")
	assert.Contains(t, usdc, "sep12")
}

func TestCreateTransaction_NoAuth(t *testing.T) {
	h := sep31.NewHandler(newMock(), testConfig())
	body, _ := json.Marshal(map[string]string{
		"asset_code": "USDC", "amount": "100",
		"receiver_account_number": "1234567890",
		"receiver_bank_code":      "058",
		"receiver_name":           "Ade Okafor",
	})
	req := httptest.NewRequest(http.MethodPost, "/sep31/transactions", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.CreateTransaction(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateTransaction_NonUSDCRejected(t *testing.T) {
	h := sep31.NewHandler(newMock(), testConfig())
	body, _ := json.Marshal(map[string]string{
		"asset_code": "XLM", "amount": "100",
		"receiver_account_number": "1234567890",
		"receiver_bank_code":      "058",
		"receiver_name":           "Ade Okafor",
	})
	req := withAccount(httptest.NewRequest(http.MethodPost, "/sep31/transactions", bytes.NewBuffer(body)), testAccount)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.CreateTransaction(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateTransaction_MissingReceiverFields(t *testing.T) {
	h := sep31.NewHandler(newMock(), testConfig())

	cases := []map[string]string{
		// missing receiver_account_number
		{"asset_code": "USDC", "amount": "100", "receiver_bank_code": "058", "receiver_name": "X"},
		// missing receiver_bank_code
		{"asset_code": "USDC", "amount": "100", "receiver_account_number": "123", "receiver_name": "X"},
		// missing receiver_name
		{"asset_code": "USDC", "amount": "100", "receiver_account_number": "123", "receiver_bank_code": "058"},
	}

	for _, tc := range cases {
		body, _ := json.Marshal(tc)
		req := withAccount(httptest.NewRequest(http.MethodPost, "/sep31/transactions", bytes.NewBuffer(body)), testAccount)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.CreateTransaction(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code, "expected 400 for payload: %v", tc)
	}
}

func TestCreateTransaction_Success(t *testing.T) {
	store := newMock()
	h := sep31.NewHandler(store, testConfig())
	body, _ := json.Marshal(map[string]string{
		"asset_code":              "USDC",
		"amount":                  "500.00",
		"receiver_account_number": "1234567890",
		"receiver_bank_code":      "058",
		"receiver_name":           "Ade Okafor",
	})
	req := withAccount(httptest.NewRequest(http.MethodPost, "/sep31/transactions", bytes.NewBuffer(body)), testAccount)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.CreateTransaction(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["id"])
	assert.Equal(t, testConfig().DistributionKey, resp["stellar_account_id"])
	assert.Equal(t, "hash", resp["stellar_memo_type"])
	assert.NotEmpty(t, resp["stellar_memo"])

	// Verify persistence
	id := resp["id"].(string)
	tx, ok := store.records[id]
	require.True(t, ok)
	assert.Equal(t, sep31.StatusPendingReceiver, tx.Status)
	assert.Equal(t, "USDC", tx.AssetCode)
	assert.Equal(t, "500.00", tx.AmountIn)
	assert.Equal(t, testAccount, tx.StellarAccountID)
}

func TestCreateTransaction_StellarMemoMax32Chars(t *testing.T) {
	store := newMock()
	h := sep31.NewHandler(store, testConfig())
	body, _ := json.Marshal(map[string]string{
		"asset_code": "USDC", "amount": "100",
		"receiver_account_number": "123", "receiver_bank_code": "058", "receiver_name": "X",
	})
	req := withAccount(httptest.NewRequest(http.MethodPost, "/sep31/transactions", bytes.NewBuffer(body)), testAccount)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.CreateTransaction(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	memo := resp["stellar_memo"].(string)
	assert.LessOrEqual(t, len(memo), 32, "Stellar hash memo must be ≤ 32 chars")
}

func TestCreateTransaction_StoreError(t *testing.T) {
	store := newMock()
	store.failNext = errors.New("mongo timeout")
	h := sep31.NewHandler(store, testConfig())
	body, _ := json.Marshal(map[string]string{
		"asset_code": "USDC", "amount": "100",
		"receiver_account_number": "123", "receiver_bank_code": "058", "receiver_name": "X",
	})
	req := withAccount(httptest.NewRequest(http.MethodPost, "/sep31/transactions", bytes.NewBuffer(body)), testAccount)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.CreateTransaction(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetTransaction_NotFound(t *testing.T) {
	h := sep31.NewHandler(newMock(), testConfig())
	router := routeWithID(h)
	req := httptest.NewRequest(http.MethodGet, "/sep31/transactions/ghost-id", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetTransaction_Found(t *testing.T) {
	store := newMock()
	store.records["tx-999"] = &sep31.Transaction{
		ID:        "tx-999",
		Status:    sep31.StatusPendingReceiver,
		AssetCode: "USDC",
		AmountIn:  "200.00",
	}
	h := sep31.NewHandler(store, testConfig())
	router := routeWithID(h)

	req := httptest.NewRequest(http.MethodGet, "/sep31/transactions/tx-999", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Transaction sep31.Transaction `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "tx-999", resp.Transaction.ID)
	assert.Equal(t, sep31.StatusPendingReceiver, resp.Transaction.Status)
	assert.Equal(t, "200.00", resp.Transaction.AmountIn)
}

func TestGetTransaction_StoreError(t *testing.T) {
	store := newMock()
	store.failNext = errors.New("timeout")
	h := sep31.NewHandler(store, testConfig())
	router := routeWithID(h)
	req := httptest.NewRequest(http.MethodGet, "/sep31/transactions/any-id", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
