package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"aidmedium/stellar-anchor/internal/config"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const ContextKeyStellarAccount contextKey = "stellar_account"

func RequireSEP10JWT(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "missing SEP-10 JWT"})
				return
			}

			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return []byte(cfg.JWTSecret), nil
			})
			if err != nil || !token.Valid {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired JWT"})
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid claims"})
				return
			}

			account, _ := claims["sub"].(string)
			ctx := context.WithValue(r.Context(), ContextKeyStellarAccount, account)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func StellarAccountFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ContextKeyStellarAccount).(string)
	return v
}

// WithStellarAccount returns a context with the given Stellar account injected.
// Used in tests to simulate a valid SEP-10 JWT.
func WithStellarAccount(ctx context.Context, account string) context.Context {
	return context.WithValue(ctx, ContextKeyStellarAccount, account)
}
