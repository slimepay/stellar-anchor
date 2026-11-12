// Package auth handles the HS256 tokens shared with the Anchor Platform:
// callback requests from SAP, Platform API requests to SAP, the SEP-24
// interactive URL token and the SEP-6 and SEP-24 more_info_url tokens.
package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	AudCallbackAPI      = "callback_api"
	AudPlatformAPI      = "platform_api"
	AudSep24Interactive = "sep24_interactive"
	AudSep6MoreInfo     = "sep6_more_info"
	AudSep24MoreInfo    = "sep24_more_info"
)

// PlatformToken signs a short lived token for calls to the SAP Platform API.
func PlatformToken(secret string, now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(30 * time.Second)),
		Audience:  jwt.ClaimStrings{AudPlatformAPI},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// RequireCallbackAuth rejects requests that do not carry a valid SAP callback token.
func RequireCallbackAuth(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || token == "" {
				writeForbidden(w)
				return
			}
			if _, err := parse(token, secret, AudCallbackAPI, &jwt.RegisteredClaims{}); err != nil {
				writeForbidden(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// InteractiveClaims is the token SAP appends to the SEP-24 interactive URL.
type InteractiveClaims struct {
	Data map[string]any `json:"data,omitempty"`
	jwt.RegisteredClaims
}

// Account returns the Stellar account and optional memo the token was issued
// for. SAP sets the subject to "account" or "account:memo".
func (c *InteractiveClaims) Account() (account, memo string) {
	account, memo, _ = strings.Cut(c.Subject, ":")
	return account, memo
}

// ParseInteractive verifies the SEP-24 interactive token and checks that it was
// issued for transactionID.
func ParseInteractive(token, secret, transactionID string) (*InteractiveClaims, error) {
	claims := &InteractiveClaims{}
	if _, err := parse(token, secret, AudSep24Interactive, claims); err != nil {
		return nil, err
	}
	if claims.ID == "" || claims.ID != transactionID {
		return nil, errors.New("token was not issued for this transaction")
	}
	if claims.Subject == "" {
		return nil, errors.New("token has no subject")
	}
	return claims, nil
}

// MoreInfoSecrets are the secrets SAP signs SEP-6 and SEP-24 more_info_url
// tokens with.
type MoreInfoSecrets struct {
	Sep6  string
	Sep24 string
}

// ParseMoreInfo verifies a more_info_url token against the secret of the SEP
// it claims, checks that it was issued for transactionID, and returns that SEP
// ("6" or "24").
func ParseMoreInfo(token, transactionID string, s MoreInfoSecrets) (string, error) {
	for _, c := range []struct{ sep, secret, aud string }{
		{"24", s.Sep24, AudSep24MoreInfo},
		{"6", s.Sep6, AudSep6MoreInfo},
	} {
		if c.secret == "" {
			continue
		}
		claims := &jwt.RegisteredClaims{}
		if _, err := parse(token, c.secret, c.aud, claims); err != nil {
			continue
		}
		if claims.ID == "" || claims.ID != transactionID {
			return "", errors.New("token was not issued for this transaction")
		}
		return c.sep, nil
	}
	return "", errors.New("invalid more info token")
}

func parse(token, secret, audience string, claims jwt.Claims) (*jwt.Token, error) {
	return jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return []byte(secret), nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(5*time.Second),
	)
}

func writeForbidden(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"forbidden"}`))
}
