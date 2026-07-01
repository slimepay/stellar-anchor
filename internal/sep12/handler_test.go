package sep12_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"aidmedium/stellar-anchor/internal/middleware"
	"aidmedium/stellar-anchor/internal/sep12"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccount = "GDQERENWDDSQZS7R7WKHZI3BSOYMV3FSWR7TFUYFTKQ447PIX6NREOJM"

// mockStore is a simple in-process mock for tests.
type mockStore struct {
	customers map[string]*sep12.Customer
	failNext  error
}

func newMockStore() *mockStore {
	return &mockStore{customers: make(map[string]*sep12.Customer)}
}

func (m *mockStore) FindByAccount(_ context.Context, account string) (*sep12.Customer, error) {
	if m.failNext != nil {
		err := m.failNext
		m.failNext = nil
		return nil, err
	}
	c, ok := m.customers[account]
	if !ok {
		return nil, nil
	}
	return c, nil
}

func (m *mockStore) Upsert(_ context.Context, account string, fields sep12.UpsertFields) (string, error) {
	if m.failNext != nil {
		err := m.failNext
		m.failNext = nil
		return "", err
	}
	existing, ok := m.customers[account]
	if !ok {
		id := "mock-id-" + account[:8]
		m.customers[account] = &sep12.Customer{
			ID:             id,
			StellarAccount: account,
			Status:         sep12.StatusProcessing,
			FirstName:      fields.FirstName,
			LastName:       fields.LastName,
			Email:          fields.Email,
			IDType:         fields.IDType,
			IDNumber:       fields.IDNumber,
		}
		return id, nil
	}
	existing.FirstName = fields.FirstName
	existing.LastName = fields.LastName
	existing.Email = fields.Email
	return existing.ID, nil
}

func (m *mockStore) Delete(_ context.Context, account string) error {
	if m.failNext != nil {
		err := m.failNext
		m.failNext = nil
		return err
	}
	delete(m.customers, account)
	return nil
}

// withAccount injects a Stellar account into request context (simulates SEP-10 JWT middleware).
func withAccount(r *http.Request, account string) *http.Request {
	return r.WithContext(middleware.WithStellarAccount(r.Context(), account))
}

func TestGetCustomer_NoAuth(t *testing.T) {
	h := sep12.NewHandler(newMockStore())
	req := httptest.NewRequest(http.MethodGet, "/sep12/customer", nil)
	w := httptest.NewRecorder()
	h.GetCustomer(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetCustomer_NoRecord(t *testing.T) {
	h := sep12.NewHandler(newMockStore())
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep12/customer", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetCustomer(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, string(sep12.StatusNeedsInfo), body["status"])
	assert.NotNil(t, body["fields"], "fields must be present when NEEDS_INFO")
}

func TestGetCustomer_ExistingRecord(t *testing.T) {
	store := newMockStore()
	store.customers[testAccount] = &sep12.Customer{
		ID: "abc123", Status: sep12.StatusAccepted,
		FirstName: "Ade", LastName: "Okafor", Email: "ade@test.com",
	}
	h := sep12.NewHandler(store)
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep12/customer", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetCustomer(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body sep12.Customer
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "abc123", body.ID)
	assert.Equal(t, sep12.StatusAccepted, body.Status)
	assert.Equal(t, "Ade", body.FirstName)
}

func TestGetCustomer_DBError(t *testing.T) {
	store := newMockStore()
	store.failNext = errors.New("mongo connection lost")
	h := sep12.NewHandler(store)
	req := withAccount(httptest.NewRequest(http.MethodGet, "/sep12/customer", nil), testAccount)
	w := httptest.NewRecorder()
	h.GetCustomer(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPutCustomer_NoAuth(t *testing.T) {
	h := sep12.NewHandler(newMockStore())
	req := httptest.NewRequest(http.MethodPut, "/sep12/customer", bytes.NewBufferString(`{}`))
	w := httptest.NewRecorder()
	h.PutCustomer(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPutCustomer_MissingRequiredFields(t *testing.T) {
	h := sep12.NewHandler(newMockStore())
	body, _ := json.Marshal(map[string]string{"first_name": "Ade"}) // missing last_name, email
	req := withAccount(httptest.NewRequest(http.MethodPut, "/sep12/customer", bytes.NewBuffer(body)), testAccount)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.PutCustomer(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPutCustomer_Success(t *testing.T) {
	h := sep12.NewHandler(newMockStore())
	body, _ := json.Marshal(sep12.UpsertFields{
		FirstName: "Ade", LastName: "Okafor", Email: "ade@test.com",
		IDType: "nin", IDNumber: "12345678901",
	})
	req := withAccount(httptest.NewRequest(http.MethodPut, "/sep12/customer", bytes.NewBuffer(body)), testAccount)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.PutCustomer(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["id"])
	assert.Equal(t, string(sep12.StatusProcessing), resp["status"])
}

func TestPutCustomer_Idempotent(t *testing.T) {
	store := newMockStore()
	h := sep12.NewHandler(store)

	upsert := func(name string) {
		body, _ := json.Marshal(sep12.UpsertFields{FirstName: name, LastName: "L", Email: "a@b.com"})
		req := withAccount(httptest.NewRequest(http.MethodPut, "/sep12/customer", bytes.NewBuffer(body)), testAccount)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.PutCustomer(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}
	upsert("First")
	upsert("Updated")

	c, _ := store.FindByAccount(context.Background(), testAccount)
	require.NotNil(t, c)
	assert.Equal(t, "Updated", c.FirstName, "second upsert must update the record")
}

func TestDeleteCustomer_NoAuth(t *testing.T) {
	h := sep12.NewHandler(newMockStore())
	req := httptest.NewRequest(http.MethodDelete, "/sep12/customer", nil)
	w := httptest.NewRecorder()
	h.DeleteCustomer(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestDeleteCustomer_Success(t *testing.T) {
	store := newMockStore()
	store.customers[testAccount] = &sep12.Customer{ID: "abc", Status: sep12.StatusAccepted}
	h := sep12.NewHandler(store)

	req := withAccount(httptest.NewRequest(http.MethodDelete, "/sep12/customer", nil), testAccount)
	w := httptest.NewRecorder()
	h.DeleteCustomer(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Nil(t, store.customers[testAccount], "record must be removed after delete")
}
