package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const secret = "0123456789abcdef0123456789abcdef"

func sign(t *testing.T, claims jwt.Claims, key string) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(key))
	require.NoError(t, err)
	return s
}

func callbackClaims(aud string, exp time.Time) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(exp),
		Audience:  jwt.ClaimStrings{aud},
	}
}

func TestRequireCallbackAuth(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := RequireCallbackAuth(secret)(ok)
	future := time.Now().Add(time.Minute)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"valid", "Bearer " + sign(t, callbackClaims(AudCallbackAPI, future), secret), http.StatusTeapot},
		{"missing", "", http.StatusForbidden},
		{"not bearer", sign(t, callbackClaims(AudCallbackAPI, future), secret), http.StatusForbidden},
		{"wrong secret", "Bearer " + sign(t, callbackClaims(AudCallbackAPI, future), "another-secret-another-secret-xx"), http.StatusForbidden},
		{"wrong audience", "Bearer " + sign(t, callbackClaims(AudPlatformAPI, future), secret), http.StatusForbidden},
		{"expired", "Bearer " + sign(t, callbackClaims(AudCallbackAPI, time.Now().Add(-time.Minute)), secret), http.StatusForbidden},
		{"no expiry", "Bearer " + sign(t, jwt.RegisteredClaims{Audience: jwt.ClaimStrings{AudCallbackAPI}}, secret), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/customer", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
		})
	}
}

func TestRejectsNoneAlgorithm(t *testing.T) {
	token, err := jwt.NewWithClaims(jwt.SigningMethodNone, callbackClaims(AudCallbackAPI, time.Now().Add(time.Minute))).
		SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/customer", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	RequireCallbackAuth(secret)(http.NotFoundHandler()).ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPlatformToken(t *testing.T) {
	now := time.Now()
	token, err := PlatformToken(secret, now)
	require.NoError(t, err)

	claims := &jwt.RegisteredClaims{}
	_, err = parse(token, secret, AudPlatformAPI, claims)
	require.NoError(t, err)
	assert.WithinDuration(t, now.Add(30*time.Second), claims.ExpiresAt.Time, time.Second)
}

func interactiveToken(t *testing.T, sub, jti string, exp time.Time) string {
	return sign(t, InteractiveClaims{
		Data: map[string]any{"asset": "stellar:native", "amount": "50"},
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			ID:        jti,
			ExpiresAt: jwt.NewNumericDate(exp),
			Audience:  jwt.ClaimStrings{AudSep24Interactive},
		},
	}, secret)
}

func TestParseInteractive(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)

	claims, err := ParseInteractive(interactiveToken(t, "GABC:123", "tx-1", future), secret, "tx-1")
	require.NoError(t, err)
	account, memo := claims.Account()
	assert.Equal(t, "GABC", account)
	assert.Equal(t, "123", memo)
	assert.Equal(t, "50", claims.Data["amount"])

	claims, err = ParseInteractive(interactiveToken(t, "GABC", "tx-1", future), secret, "tx-1")
	require.NoError(t, err)
	account, memo = claims.Account()
	assert.Equal(t, "GABC", account)
	assert.Empty(t, memo)

	_, err = ParseInteractive(interactiveToken(t, "GABC", "tx-1", future), secret, "tx-2")
	assert.Error(t, err, "token for another transaction")

	_, err = ParseInteractive(interactiveToken(t, "GABC", "tx-1", time.Now().Add(-time.Minute)), secret, "tx-1")
	assert.Error(t, err, "expired")

	_, err = ParseInteractive(interactiveToken(t, "", "tx-1", future), secret, "tx-1")
	assert.Error(t, err, "no subject")
}
