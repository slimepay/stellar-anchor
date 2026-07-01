package sep6_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/middleware"
	"aidmedium/stellar-anchor/internal/sep6"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccount = "GDQERENWDDSQZS7R7WKHZI3BSOYMV3FSWR7TFUYFTKQ447PIX6NREOJM"

func testConfig() *config.Config {
	return &config.Config{
		DistributionKey: "GDIST0000EXAMPLEKEY000000000000000000000000000000000000000",
		USDCIssuer:      "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5",
	}
}

type mockStore struct {
	records  map[string]*sep6.Transaction
	failNext error
}

func newMock() *mockStore { return &mockStore{records: make(map[string]*sep6.Transaction)} }

func (m *mockStore) Insert(_ context.Context, tx *sep6.Transaction) error {
	if m.failNext != nil {
		err := m.failNext; m.failNext = nil; return err
	}
	m.records[tx.ID] = tx
	return nil
}

func (m *mockStore) FindByIDAndAccount(_ context.Context, id, account string) (*sep6.Transaction, error) {
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
	h := sep6.NewHandler(newMock(), testConfig())
	req := httptest.NewRequest(http.MethodGet, "/sep6/info", nil)
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

	usdc := deposit["USDC"].(map[string]any)
	assert.Equal(t, true, usdc["enabled"])
	assert.Equal(t, true, usdc["authentication_required"])
}

func TestDeposit_NoAuth(t *testing.T) {
	h := sep6.NewHandler(newMock(), testConfig())
	req := httptest.NewRequest(http.MethodGet, "/sep6/deposit?asset_code=USDC", nil)
	w := httptest.NewRecorder()
	h.Deposit(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestDeposit_UnsupportedAsset(t *testing.T) {
	h := sep6.NewHandler(newMock(), testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/deposit?asset_code=ETH", nil), testAccount)
	w := httptest.NewRecorder()
	h.Deposit(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeposit_USDC_Success(t *testing.T) {
	store := newMock()
	h := sep6.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/deposit?asset_code=USDC", nil), testAccount)
	w := httptest.NewRecorder()
	h.Deposit(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["id"])
	assert.NotEmpty(t, resp["deposit_address"])
	assert.NotEmpty(t, resp["memo"])
	assert.Equal(t, "text", resp["memo_type"])
	assert.Contains(t, resp["memo"].(string), "DEP")

	// Verify persisted
	id := resp["id"].(string)
	tx, ok := store.records[id]
	require.True(t, ok)
	assert.Equal(t, "deposit", tx.Kind)
	assert.Equal(t, "USDC", tx.AssetCode)
	assert.Equal(t, testConfig().DistributionKey, tx.DepositAddress)
	assert.Equal(t, testAccount, tx.StellarAccount)
}

func TestDeposit_XLM_Success(t *testing.T) {
	store := newMock()
	h := sep6.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/deposit?asset_code=XLM", nil), testAccount)
	w := httptest.NewRecorder()
	h.Deposit(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	id := resp["id"].(string)
	tx := store.records[id]
	require.NotNil(t, tx)
	assert.Equal(t, "XLM", tx.AssetCode)
}

func TestDeposit_MemoUnique(t *testing.T) {
	store := newMock()
	h := sep6.NewHandler(store, testConfig())

	var memos []string
	for i := 0; i < 5; i++ {
		req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/deposit?asset_code=USDC", nil), testAccount)
		w := httptest.NewRecorder()
		h.Deposit(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		memos = append(memos, resp["memo"].(string))
	}
	// All memos must be distinct
	seen := make(map[string]bool)
	for _, m := range memos {
		assert.False(t, seen[m], "memo collision detected: %s", m)
		seen[m] = true
	}
}

func TestDeposit_StoreError(t *testing.T) {
	store := newMock()
	store.failNext = errors.New("db unavailable")
	h := sep6.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/deposit?asset_code=USDC", nil), testAccount)
	w := httptest.NewRecorder()
	h.Deposit(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestWithdraw_NoAuth(t *testing.T) {
	h := sep6.NewHandler(newMock(), testConfig())
	req := httptest.NewRequest(http.MethodGet, "/sep6/withdraw?asset_code=USDC&dest=1234567890", nil)
	w := httptest.NewRecorder()
	h.Withdraw(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWithdraw_NonUSDCRejected(t *testing.T) {
	h := sep6.NewHandler(newMock(), testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/withdraw?asset_code=XLM&dest=1234567890", nil), testAccount)
	w := httptest.NewRecorder()
	h.Withdraw(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWithdraw_MissingDest(t *testing.T) {
	h := sep6.NewHandler(newMock(), testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/withdraw?asset_code=USDC", nil), testAccount)
	w := httptest.NewRecorder()
	h.Withdraw(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWithdraw_Success(t *testing.T) {
	store := newMock()
	h := sep6.NewHandler(store, testConfig())
	req := withAccount(
		httptest.NewRequest(http.MethodGet, "/sep6/withdraw?asset_code=USDC&dest=1234567890&dest_extra=058", nil),
		testAccount,
	)
	w := httptest.NewRecorder()
	h.Withdraw(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["id"])
	assert.Equal(t, testConfig().DistributionKey, resp["account_id"])
	assert.Contains(t, resp["memo"].(string), "WD")

	id := resp["id"].(string)
	tx := store.records[id]
	require.NotNil(t, tx)
	assert.Equal(t, "withdrawal", tx.Kind)
	assert.Equal(t, "1234567890", tx.Dest)
	assert.Equal(t, "058", tx.DestExtra)
}

func TestGetTransaction_MissingID(t *testing.T) {
	h := sep6.NewHandler(newMock(), testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/transaction", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetTransaction(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetTransaction_NotFound(t *testing.T) {
	h := sep6.NewHandler(newMock(), testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/transaction?id=ghost", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetTransaction(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetTransaction_Found(t *testing.T) {
	store := newMock()
	store.records["tx-1"] = &sep6.Transaction{
		ID: "tx-1", Kind: "deposit", AssetCode: "USDC",
		StellarAccount: testAccount, Status: "pending_external",
	}
	h := sep6.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/transaction?id=tx-1", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetTransaction(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Transaction sep6.Transaction `json:"transaction"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "tx-1", resp.Transaction.ID)
	assert.Equal(t, "USDC", resp.Transaction.AssetCode)
}

func TestGetTransaction_WrongAccount(t *testing.T) {
	store := newMock()
	store.records["tx-2"] = &sep6.Transaction{
		ID: "tx-2", StellarAccount: "GDIFFERENT0000000000000000000000000000000000000000000000",
	}
	h := sep6.NewHandler(store, testConfig())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep6/transaction?id=tx-2", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetTransaction(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code, "must not expose another account's transaction")
}
