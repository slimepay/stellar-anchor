package sep24

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/memo"
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
	StellarTxHash  string     `bson:"stellar_tx_hash,omitempty" json:"stellar_transaction_id,omitempty"`
	StartedAt      time.Time  `bson:"started_at"  json:"started_at"`
	CompletedAt    *time.Time `bson:"completed_at,omitempty" json:"completed_at,omitempty"`

	WithdrawAnchorAccount string `bson:"withdraw_anchor_account,omitempty" json:"withdraw_anchor_account,omitempty"`
	WithdrawMemo          string `bson:"withdraw_memo,omitempty"           json:"withdraw_memo,omitempty"`
	WithdrawMemoType      string `bson:"withdraw_memo_type,omitempty"      json:"withdraw_memo_type,omitempty"`
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
	// USDC or XLM in, naira out: withdrawals only.
	json.NewEncoder(w).Encode(map[string]any{
		"deposit": map[string]any{},
		"withdraw": map[string]any{
			"USDC": map[string]any{"enabled": true, "min_amount": 5, "max_amount": 50000},
			"XLM":  map[string]any{"enabled": true, "min_amount": 5, "max_amount": 100000},
		},
		"transaction":  map[string]any{"enabled": true},
		"transactions": map[string]any{"enabled": true},
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

	memoID, err := memo.NewID()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create memo")
		return
	}

	assetIssuer := ""
	if assetCode == "USDC" {
		assetIssuer = h.cfg.USDCIssuer
	}

	tx := &Transaction{
		ID:                    uuid.New().String(),
		Kind:                  "withdrawal",
		Status:                TxPendingUserTransfer,
		StellarAccount:        account,
		AssetCode:             assetCode,
		AssetIssuer:           assetIssuer,
		WithdrawAnchorAccount: h.cfg.DistributionKey,
		WithdrawMemo:          memoID,
		WithdrawMemoType:      memo.Type,
		StartedAt:             time.Now().UTC(),
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
