package sep24

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/middleware"

	"github.com/google/uuid"
)

type TxStatus string

const (
	TxPendingUserTransfer TxStatus = "pending_user_transfer_start"
	TxPendingExternal     TxStatus = "pending_external"
	TxPendingAnchor       TxStatus = "pending_anchor"
	TxCompleted           TxStatus = "completed"
	TxExpired             TxStatus = "expired"
	TxError               TxStatus = "error"
)

type Transaction struct {
	ID             string     `bson:"_id"          json:"id"`
	Kind           string     `bson:"kind"         json:"kind"`
	Status         TxStatus   `bson:"status"       json:"status"`
	StellarAccount string     `bson:"stellar_account" json:"-"`
	AssetCode      string     `bson:"asset_code"   json:"asset_code"`
	AssetIssuer    string     `bson:"asset_issuer,omitempty" json:"asset_issuer,omitempty"`
	AmountIn       string     `bson:"amount_in,omitempty"    json:"amount_in,omitempty"`
	AmountOut      string     `bson:"amount_out,omitempty"   json:"amount_out,omitempty"`
	AmountFee      string     `bson:"amount_fee,omitempty"   json:"amount_fee,omitempty"`
	DepositAddress string     `bson:"deposit_address,omitempty" json:"deposit_address,omitempty"`
	StellarTxHash  string     `bson:"stellar_tx_hash,omitempty" json:"stellar_transaction_id,omitempty"`
	StartedAt      time.Time  `bson:"started_at"  json:"started_at"`
	CompletedAt    *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
}

type Handler struct {
	store Store
	cfg   *config.Config
}

func NewHandler(store Store, cfg *config.Config) *Handler {
	return &Handler{store: store, cfg: cfg}
}

func (h *Handler) Info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"deposit": map[string]any{
			"USDC": map[string]any{"enabled": true, "authentication_required": true, "min_amount": 1, "max_amount": 50000, "fee_fixed": 0.50, "fee_percent": 0.5},
			"XLM":  map[string]any{"enabled": true, "authentication_required": true, "min_amount": 1, "max_amount": 100000, "fee_fixed": 0.01, "fee_percent": 0.1},
		},
		"withdraw": map[string]any{
			"USDC": map[string]any{"enabled": true, "authentication_required": true, "min_amount": 5, "max_amount": 50000, "fee_fixed": 1.0, "fee_percent": 0.5},
		},
		"transaction":  map[string]any{"enabled": true},
		"transactions": map[string]any{"enabled": true},
	})
}

func (h *Handler) InitiateDeposit(w http.ResponseWriter, r *http.Request) {
	account := middleware.StellarAccountFromContext(r.Context())
	if account == "" {
		writeErr(w, http.StatusUnauthorized, "SEP-10 authentication required")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid form data")
		return
	}
	assetCode := r.FormValue("asset_code")
	if assetCode != "USDC" && assetCode != "XLM" {
		writeErr(w, http.StatusBadRequest, "unsupported asset_code; supported: USDC, XLM")
		return
	}

	assetIssuer := ""
	if assetCode == "USDC" {
		assetIssuer = h.cfg.USDCIssuer
	}

	tx := &Transaction{
		ID:             uuid.New().String(),
		Kind:           "deposit",
		Status:         TxPendingUserTransfer,
		StellarAccount: account,
		AssetCode:      assetCode,
		AssetIssuer:    assetIssuer,
		StartedAt:      time.Now().UTC(),
	}
	if err := h.store.Insert(r.Context(), tx); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"type": "interactive_customer_info_needed",
		"url":  fmt.Sprintf("https://%s/anchor/deposit?id=%s", h.cfg.HomeDomain, tx.ID),
		"id":   tx.ID,
	})
}

func (h *Handler) InitiateWithdraw(w http.ResponseWriter, r *http.Request) {
	account := middleware.StellarAccountFromContext(r.Context())
	if account == "" {
		writeErr(w, http.StatusUnauthorized, "SEP-10 authentication required")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid form data")
		return
	}
	assetCode := r.FormValue("asset_code")
	if assetCode != "USDC" && assetCode != "XLM" {
		writeErr(w, http.StatusBadRequest, "unsupported asset_code; supported: USDC, XLM")
		return
	}

	tx := &Transaction{
		ID:             uuid.New().String(),
		Kind:           "withdrawal",
		Status:         TxPendingUserTransfer,
		StellarAccount: account,
		AssetCode:      assetCode,
		StartedAt:      time.Now().UTC(),
	}
	if err := h.store.Insert(r.Context(), tx); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"type": "interactive_customer_info_needed",
		"url":  fmt.Sprintf("https://%s/anchor/withdraw?id=%s", h.cfg.HomeDomain, tx.ID),
		"id":   tx.ID,
	})
}

func (h *Handler) GetTransaction(w http.ResponseWriter, r *http.Request) {
	account := middleware.StellarAccountFromContext(r.Context())
	txID := r.URL.Query().Get("id")
	if txID == "" {
		writeErr(w, http.StatusBadRequest, "id query parameter required")
		return
	}

	tx, err := h.store.FindByIDAndAccount(r.Context(), txID, account)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "database error")
		return
	}
	if tx == nil {
		writeErr(w, http.StatusNotFound, "transaction not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"transaction": tx})
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
