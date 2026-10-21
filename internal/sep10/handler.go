package sep10

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"aidmedium/stellar-anchor/internal/config"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stellar/go-stellar-sdk/clients/horizonclient"
	stellarkeypair "github.com/stellar/go-stellar-sdk/keypair"
	hProtocol "github.com/stellar/go-stellar-sdk/protocols/horizon"
	"github.com/stellar/go-stellar-sdk/txnbuild"
)

// challengeTimeout is how long a challenge stays valid after it is issued.
const challengeTimeout = 15 * time.Minute

// AccountGetter loads an account from Horizon. *horizonclient.Client satisfies it.
type AccountGetter interface {
	AccountDetail(request horizonclient.AccountRequest) (hProtocol.Account, error)
}

type challengeResponse struct {
	Transaction       string `json:"transaction"`
	NetworkPassphrase string `json:"network_passphrase"`
}

type tokenResponse struct {
	Token string `json:"token"`
}

type errResponse struct {
	Error string `json:"error"`
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errResponse{Error: msg})
}

// GetChallenge handles GET /sep10/auth
// Returns a Stellar transaction challenge the client must sign to prove keypair ownership.
func GetChallenge(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID := strings.TrimSpace(r.URL.Query().Get("account"))
		if accountID == "" {
			writeErr(w, http.StatusBadRequest, "account query parameter is required")
			return
		}
		if _, err := stellarkeypair.ParseAddress(accountID); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid account address")
			return
		}

		if _, err := stellarkeypair.ParseFull(cfg.AnchorSigningSeed); err != nil {
			writeErr(w, http.StatusInternalServerError, "anchor keypair error")
			return
		}

		// Source is the anchor account with sequence 0, so the tx can never be submitted.
		// Op 1 is "<home_domain> auth" with the client as source, op 2 is web_auth_domain.
		tx, err := txnbuild.BuildChallengeTx(
			cfg.AnchorSigningSeed,
			accountID,
			cfg.WebAuthDomain,
			cfg.HomeDomain,
			cfg.NetworkPassphrase,
			challengeTimeout,
			nil,
		)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "challenge build failed")
			return
		}

		txBase64, err := tx.Base64()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "challenge encode failed")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(challengeResponse{
			Transaction:       txBase64,
			NetworkPassphrase: cfg.NetworkPassphrase,
		})
	}
}

// PostChallenge handles POST /sep10/auth
// Verifies the signed challenge against the client account's signers and returns a JWT.
// If the account does not exist on the network, only its master key is accepted.
func PostChallenge(cfg *config.Config, accounts AccountGetter) http.HandlerFunc {
	used := newUsedChallenges()

	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Transaction string `json:"transaction"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Transaction == "" {
			writeErr(w, http.StatusBadRequest, "transaction field required")
			return
		}

		homeDomains := []string{cfg.HomeDomain}

		// Checks source account, sequence 0, time bounds, op keys, web_auth_domain
		// and the anchor signature.
		tx, clientAccountID, _, _, err := txnbuild.ReadChallengeTx(
			body.Transaction,
			cfg.AnchorSigningKey,
			cfg.NetworkPassphrase,
			cfg.WebAuthDomain,
			homeDomains,
		)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid challenge transaction: "+err.Error())
			return
		}

		account, err := accounts.AccountDetail(horizonclient.AccountRequest{AccountID: clientAccountID})
		switch {
		case horizonclient.IsNotFoundError(err):
			_, err = txnbuild.VerifyChallengeTxSigners(
				body.Transaction,
				cfg.AnchorSigningKey,
				cfg.NetworkPassphrase,
				cfg.WebAuthDomain,
				homeDomains,
				clientAccountID,
			)
		case err != nil:
			writeErr(w, http.StatusServiceUnavailable, "could not load client account")
			return
		default:
			_, err = txnbuild.VerifyChallengeTxThreshold(
				body.Transaction,
				cfg.AnchorSigningKey,
				cfg.NetworkPassphrase,
				cfg.WebAuthDomain,
				homeDomains,
				mediumThreshold(account),
				activeSigners(account),
			)
		}
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "challenge signature verification failed: "+err.Error())
			return
		}

		hash, err := tx.Hash(cfg.NetworkPassphrase)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "hash failed")
			return
		}
		txHash := hex.EncodeToString(hash[:])
		if !used.claim(txHash, time.Unix(tx.Timebounds().MaxTime, 0)) {
			writeErr(w, http.StatusUnauthorized, "challenge has already been used")
			return
		}

		claims := jwt.MapClaims{
			"sub": clientAccountID,
			"iss": fmt.Sprintf("https://%s", cfg.HomeDomain),
			"jti": txHash,
			"iat": time.Now().Unix(),
			"exp": time.Now().Add(time.Duration(cfg.JWTExpiryHours) * time.Hour).Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, err := token.SignedString([]byte(cfg.JWTSecret))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "jwt sign failed")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{Token: signed})
	}
}

// mediumThreshold returns the account's medium threshold, never less than 1,
// so a signature from a zero weight key can't pass on its own.
func mediumThreshold(account hProtocol.Account) txnbuild.Threshold {
	if account.Thresholds.MedThreshold == 0 {
		return 1
	}
	return txnbuild.Threshold(account.Thresholds.MedThreshold)
}

// activeSigners drops zero weight signers, e.g. a master key that was disabled.
// A signature from one of them is then rejected as unrecognized.
func activeSigners(account hProtocol.Account) txnbuild.SignerSummary {
	signers := txnbuild.SignerSummary{}
	for key, weight := range account.SignerSummary() {
		if weight > 0 {
			signers[key] = weight
		}
	}
	return signers
}

// usedChallenges remembers accepted challenge hashes until they expire so
// each signed challenge can only be exchanged for a token once.
type usedChallenges struct {
	mu      sync.Mutex
	expires map[string]time.Time
}

func newUsedChallenges() *usedChallenges {
	return &usedChallenges{expires: map[string]time.Time{}}
}

func (u *usedChallenges) claim(hash string, expiresAt time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	now := time.Now()
	for h, exp := range u.expires {
		if now.After(exp) {
			delete(u.expires, h)
		}
	}

	if _, seen := u.expires[hash]; seen {
		return false
	}
	u.expires[hash] = expiresAt
	return true
}
