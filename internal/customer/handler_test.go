package customer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func router() http.Handler {
	svc, _, _ := setup()
	r := chi.NewRouter()
	NewHandler(svc).Routes(r)
	return r
}

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCallbackRoundTrip(t *testing.T) {
	h := router()

	w := do(t, h, http.MethodGet, "/customer?account=GABC&type=sep24-customer", "")
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "NEEDS_INFO", got["status"])

	body, _ := json.Marshal(map[string]string{
		"account": "GABC", "type": "sep24-customer",
		"first_name": "Ada", "last_name": "Obi", "email_address": "ada@example.com", "birth_date": "1990-05-17",
		"id_type": "bvn", "id_number": "22222222222", "bank_account_number": "0123456789", "bank_number": "000013",
	})
	w = do(t, h, http.MethodPut, "/customer", string(body))
	require.Equal(t, http.StatusOK, w.Code)
	var put map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &put))
	id, _ := put["id"].(string)
	require.NotEmpty(t, id)

	w = do(t, h, http.MethodGet, "/customer?id="+id, "")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "ACCEPTED", got["status"])
	assert.NotContains(t, w.Body.String(), "22222222222", "provided values are never echoed back")

	w = do(t, h, http.MethodDelete, "/customer/"+id, "")
	assert.Equal(t, http.StatusNoContent, w.Code)

	w = do(t, h, http.MethodGet, "/customer?id="+id, "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCallbackErrors(t *testing.T) {
	h := router()
	assert.Equal(t, http.StatusBadRequest, do(t, h, http.MethodPut, "/customer", "not json").Code)
	assert.Equal(t, http.StatusBadRequest, do(t, h, http.MethodPut, "/customer", `{"account":"GABC","id_number":"12"}`).Code)
	assert.Equal(t, http.StatusNotFound, do(t, h, http.MethodPut, "/customer", `{"id":"missing","first_name":"A"}`).Code)
	assert.Equal(t, http.StatusNotFound, do(t, h, http.MethodDelete, "/customer/missing", "").Code)
	assert.Equal(t, http.StatusBadRequest, do(t, h, http.MethodPut, "/customer", `{"first_name":"A"}`).Code, "account required for new customers")
}
