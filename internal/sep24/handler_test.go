package sep24_test

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
	"aidmedium/stellar-anchor/internal/sep24"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccount = "GDQERENWDDSQZS7R7WKHZI3BSOYMV3FSWR7TFUYFTKQ447PIX6NREOJM"

func testConfig() *config.Config {
	return &config.Config{
		HomeDomain:      "example.com",
		USDCIssuer:      "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5",
		DistributionKey: "GDIST0000EXAMPLEKEY000000000000000000000000000000000000000",
	}
}

type mockStore struct {
	records  map[string]*sep24.Transaction
	failNext error
}

func newMock() *mockStore { return &mockStore{records: make(map[string]*sep24.Transaction)} }

func (m *mockStore) Insert(_ context.Context, tx *sep24.Transaction) error {
	if m.failNext != nil {
		err := m.failNext; m.failNext = nil; return err
	}
	m.records[tx.ID] = tx
	return nil
}

func (m *mockStore) FindByIDAndAccount(_ context.Context, id, account string) (*sep24.Transaction, error) {
	if m.failNext != nil {
		err := m.failNext; m.failNext = nil; return nil, err
	}
	tx, ok := m.records[id]
	if !ok || tx.StellarAccount != account {
		return nil, nil
	}
	return tx, nil
}

func withAccount(r *http.Request, account string) *http.Request {
	return r.WithContext(middleware.WithStellarAccount(r.Context(), account))
}

func TestInfo(t *testing.T) {
	h := sep24.NewHandler(newMock(), testConfig())
	req := httptest.NewRequest(http.MethodGet, "/sep24/info", nil)
	w := httptest.NewRecorder()
	h.Info(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body, "deposit")
	assert.Contains(t, body, "withdraw")
	deposit := body["deposit"].(map[string]any)
	assert.Contains(t, deposit, "USDC")
	assert.Contains(t, deposit, "XLM")
}

func TestInitiateDeposit_NoAuth(t *testing.T) {
	h := sep24.NewHandler(newMock(), testConfig())
	req := httptest.NewRequest(http.MethodPost, "/sep24/transactions/deposit/interactive",
		bytes.NewBufferString("asset_code=USDC"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.InitiateDeposit(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestInitiateDeposit_UnsupportedAsset(t *testing.T) {
	h := sep24.NewHandler(newMock(), testConfig())
	req := withAccount(httptest.NewRequest(http.MethodPost, "/sep24/transactions/deposit/interactive",
		bytes.NewBufferString("asset_code=BTC")), testAccount)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.InitiateDeposit(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestInitiateDeposit_USDC(t *testing.T) {
	store := newMock()
	h := sep24.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodPost, "/sep24/transactions/deposit/interactive",
		bytes.NewBufferString("asset_code=USDC")), testAccount)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.InitiateDeposit(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "interactive_customer_info_needed", resp["type"])
	assert.NotEmpty(t, resp["id"])
	assert.Contains(t, resp["url"], resp["id"])

	// Verify record was stored
	tx, ok := store.records[resp["id"]]
	require.True(t, ok, "transaction must be persisted in the store")
	assert.Equal(t, "deposit", tx.Kind)
	assert.Equal(t, "USDC", tx.AssetCode)
	assert.Equal(t, testAccount, tx.StellarAccount)
	assert.Equal(t, sep24.TxPendingUserTransfer, tx.Status)
}

func TestInitiateDeposit_XLM(t *testing.T) {
	store := newMock()
	h := sep24.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodPost, "/sep24/transactions/deposit/interactive",
		bytes.NewBufferString("asset_code=XLM")), testAccount)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.InitiateDeposit(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["id"])

	tx := store.records[resp["id"]]
	require.NotNil(t, tx)
	assert.Equal(t, "XLM", tx.AssetCode)
	assert.Empty(t, tx.AssetIssuer, "XLM has no issuer")
}

func TestInitiateDeposit_StoreError(t *testing.T) {
	store := newMock()
	store.failNext = errors.New("insert failed")
	h := sep24.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodPost, "/sep24/transactions/deposit/interactive",
		bytes.NewBufferString("asset_code=USDC")), testAccount)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.InitiateDeposit(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestInitiateWithdraw_Success(t *testing.T) {
	store := newMock()
	h := sep24.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodPost, "/sep24/transactions/withdraw/interactive",
		bytes.NewBufferString("asset_code=USDC")), testAccount)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.InitiateWithdraw(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["id"])
	tx := store.records[resp["id"]]
	require.NotNil(t, tx)
	assert.Equal(t, "withdrawal", tx.Kind)
}

func TestGetTransaction_NotFound(t *testing.T) {
	h := sep24.NewHandler(newMock(), testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep24/transaction?id=doesnotexist", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetTransaction(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetTransaction_MissingID(t *testing.T) {
	h := sep24.NewHandler(newMock(), testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep24/transaction", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetTransaction(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetTransaction_Found(t *testing.T) {
	store := newMock()
	store.records["tx-abc"] = &sep24.Transaction{
		ID: "tx-abc", Kind: "deposit", AssetCode: "USDC",
		StellarAccount: testAccount, Status: sep24.TxPendingExternal,
	}
	h := sep24.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep24/transaction?id=tx-abc", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetTransaction(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Transaction sep24.Transaction `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "tx-abc", resp.Transaction.ID)
	assert.Equal(t, sep24.TxPendingExternal, resp.Transaction.Status)
}

func TestGetTransaction_WrongAccount(t *testing.T) {
	store := newMock()
	store.records["tx-xyz"] = &sep24.Transaction{
		ID: "tx-xyz", StellarAccount: "GDIFFERENTACCOUNT0000000000000000000000000000000000000000",
	}
	h := sep24.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep24/transaction?id=tx-xyz", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetTransaction(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code, "must not return another account's transaction")
}
