package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/middleware"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAccount = "GDQERENWDDSQZS7R7WKHZI3BSOYMV3FSWR7TFUYFTKQ447PIX6NREOJM"

func testConfig() *config.Config {
	return &config.Config{
		JWTSecret:      "test-secret-at-least-32-bytes-long!",
		JWTExpiryHours: 24,
		HomeDomain:     "example.com",
	}
}

func makeToken(secret, subject string, expiry time.Duration) string {
	claims := jwt.MapClaims{
		"sub": subject,
		"iss": "https://example.com",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(expiry).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := token.SignedString([]byte(secret))
	return signed
}

// sentinel handler that writes the injected account to the response body.
func echoAccount(w http.ResponseWriter, r *http.Request) {
	account := middleware.StellarAccountFromContext(r.Context())
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(account))
}

func TestRequireSEP10JWT_NoHeader(t *testing.T) {
	cfg := testConfig()
	mw := middleware.RequireSEP10JWT(cfg)
	handler := mw(http.HandlerFunc(echoAccount))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireSEP10JWT_MalformedHeader(t *testing.T) {
	cfg := testConfig()
	mw := middleware.RequireSEP10JWT(cfg)
	handler := mw(http.HandlerFunc(echoAccount))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "NotBearer abc")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireSEP10JWT_InvalidToken(t *testing.T) {
	cfg := testConfig()
	mw := middleware.RequireSEP10JWT(cfg)
	handler := mw(http.HandlerFunc(echoAccount))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer this.is.garbage")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireSEP10JWT_WrongSecret(t *testing.T) {
	cfg := testConfig()
	mw := middleware.RequireSEP10JWT(cfg)
	handler := mw(http.HandlerFunc(echoAccount))

	// Token signed with a different secret
	token := makeToken("completely-different-secret-xxxxxx", testAccount, time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireSEP10JWT_ExpiredToken(t *testing.T) {
	cfg := testConfig()
	mw := middleware.RequireSEP10JWT(cfg)
	handler := mw(http.HandlerFunc(echoAccount))

	token := makeToken(cfg.JWTSecret, testAccount, -time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireSEP10JWT_ValidToken(t *testing.T) {
	cfg := testConfig()
	mw := middleware.RequireSEP10JWT(cfg)
	handler := mw(http.HandlerFunc(echoAccount))

	token := makeToken(cfg.JWTSecret, testAccount, time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, testAccount, w.Body.String(), "Stellar account must be injected into context")
}

func TestRequireSEP10JWT_PropagatesAccount(t *testing.T) {
	cfg := testConfig()
	mw := middleware.RequireSEP10JWT(cfg)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = middleware.StellarAccountFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	token := makeToken(cfg.JWTSecret, testAccount, time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, testAccount, captured)
}

func TestWithStellarAccount_RoundTrip(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := middleware.WithStellarAccount(req.Context(), testAccount)
	assert.Equal(t, testAccount, middleware.StellarAccountFromContext(ctx))
}

func TestStellarAccountFromContext_Empty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	account := middleware.StellarAccountFromContext(req.Context())
	assert.Empty(t, account, "empty context must return empty string")
}

func TestStellarAccountFromContext_Set(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(middleware.WithStellarAccount(req.Context(), testAccount))
	account := middleware.StellarAccountFromContext(req.Context())
	assert.Equal(t, testAccount, account)
}
