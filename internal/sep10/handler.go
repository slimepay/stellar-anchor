package sep10

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"aidmedium/stellar-anchor/internal/config"

	"github.com/golang-jwt/jwt/v5"
	stellarkeypair "github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/txnbuild"
)

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

		anchorKP, err := stellarkeypair.ParseFull(cfg.AnchorSigningSeed)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "anchor keypair error")
			return
		}

		nonce := make([]byte, 48)
		if _, err := rand.Read(nonce); err != nil {
			writeErr(w, http.StatusInternalServerError, "nonce generation failed")
			return
		}

		// Sequence 0 — challenge tx is never valid on-chain
		sourceAccount := txnbuild.SimpleAccount{
			AccountID: anchorKP.Address(),
			Sequence:  0,
		}

		tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
			SourceAccount:        &sourceAccount,
			IncrementSequenceNum: true,
			Operations: []txnbuild.Operation{
				// Op 1: client proves ownership by signing (source = client account)
				&txnbuild.ManageData{
					Name:          fmt.Sprintf("%s auth", cfg.WebAuthDomain),
					Value:         nonce,
					SourceAccount: accountID,
				},
				// Op 2: binds challenge to this anchor's home domain
				&txnbuild.ManageData{
					Name:          fmt.Sprintf("web_auth_domain"),
					Value:         []byte(cfg.WebAuthDomain),
					SourceAccount: anchorKP.Address(),
				},
			},
			BaseFee: txnbuild.MinBaseFee,
			Preconditions: txnbuild.Preconditions{
				TimeBounds: txnbuild.NewTimebounds(
					time.Now().Unix(),
					time.Now().Add(15*time.Minute).Unix(),
				),
			},
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "challenge build failed")
			return
		}

		tx, err = tx.Sign(cfg.NetworkPassphrase, anchorKP)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "challenge sign failed")
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
// Verifies the client signed the challenge transaction, returns a JWT on success.
func PostChallenge(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Transaction string `json:"transaction"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Transaction == "" {
			writeErr(w, http.StatusBadRequest, "transaction field required")
			return
		}

		parsed, err := txnbuild.TransactionFromXDR(body.Transaction)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid transaction XDR")
			return
		}

		tx, ok := parsed.Transaction()
		if !ok {
			writeErr(w, http.StatusBadRequest, "expected a transaction, not a fee-bump")
			return
		}

		ops := tx.Operations()
		if len(ops) == 0 {
			writeErr(w, http.StatusBadRequest, "transaction has no operations")
			return
		}
		md, ok := ops[0].(*txnbuild.ManageData)
		if !ok || md.SourceAccount == "" {
			writeErr(w, http.StatusBadRequest, "first op must be manage_data with client source account")
			return
		}
		clientAddress := md.SourceAccount

		hash, err := tx.Hash(cfg.NetworkPassphrase)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "hash failed")
			return
		}

		clientKP, err := stellarkeypair.ParseAddress(clientAddress)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid client account in first op")
			return
		}
		anchorKP, err := stellarkeypair.ParseAddress(cfg.AnchorSigningKey)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "anchor key parse failed")
			return
		}

		var clientSigned, anchorSigned bool
		for _, sig := range tx.Signatures() {
			if clientKP.Verify(hash[:], sig.Signature) == nil {
				clientSigned = true
			}
			if anchorKP.Verify(hash[:], sig.Signature) == nil {
				anchorSigned = true
			}
		}
		if !anchorSigned {
			writeErr(w, http.StatusUnauthorized, "anchor signature missing from challenge")
			return
		}
		if !clientSigned {
			writeErr(w, http.StatusUnauthorized, "client signature verification failed")
			return
		}

		claims := jwt.MapClaims{
			"sub": clientAddress,
			"iss": fmt.Sprintf("https://%s", cfg.HomeDomain),
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
