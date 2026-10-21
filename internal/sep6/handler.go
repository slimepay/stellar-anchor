package sep6

import (
	"encoding/json"
	"net/http"
	"time"

	"aidmedium/stellar-anchor/internal/config"
	"aidmedium/stellar-anchor/internal/memo"
	"aidmedium/stellar-anchor/internal/middleware"

	"github.com/google/uuid"
)

type Transaction struct {
	ID             string     `bson:"_id"          json:"id"`
	Kind           string     `bson:"kind"         json:"kind"`
	Status         string     `bson:"status"       json:"status"`
	StellarAccount string     `bson:"stellar_account" json:"-"`
	AssetCode      string     `bson:"asset_code"   json:"asset_code"`
	AmountIn       string     `bson:"amount_in,omitempty"  json:"amount_in,omitempty"`
	AmountOut      string     `bson:"amount_out,omitempty" json:"amount_out,omitempty"`
	Memo           string     `bson:"memo,omitempty"       json:"memo,omitempty"`
	Dest           string     `bson:"dest,omitempty"       json:"-"`
	DestExtra      string     `bson:"dest_extra,omitempty" json:"-"`
	StartedAt      time.Time  `bson:"started_at"   json:"started_at"`
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
	// USDC in, naira out: withdrawals only.
	json.NewEncoder(w).Encode(map[string]any{
		"deposit": map[string]any{},
		"withdraw": map[string]any{
			"USDC": map[string]any{
				"enabled": true, "authentication_required": true,
				"min_amount": 5, "max_amount": 50000,
				"types": map[string]any{
					"bank_account": map[string]any{
						"fields": map[string]any{
							"dest":       map[string]string{"description": "Bank account number"},
							"dest_extra": map[string]string{"description": "Bank sort/routing code"},
						},
					},
				},
			},
		},
	})
}

func (h *Handler) Withdraw(w http.ResponseWriter, r *http.Request) {
	account := middleware.StellarAccountFromContext(r.Context())
	if account == "" {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	assetCode := r.URL.Query().Get("asset_code")
	if assetCode != "USDC" {
		writeErr(w, http.StatusBadRequest, "only USDC withdrawals are supported")
		return
	}
	dest := r.URL.Query().Get("dest")
	if dest == "" {
		writeErr(w, http.StatusBadRequest, "dest (bank account number) is required")
		return
	}

	txID := uuid.New().String()
	memoID, err := memo.NewID()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create memo")
		return
	}

	tx := &Transaction{
		ID:             txID,
		Kind:           "withdrawal",
		Status:         "pending_user_transfer_start",
		StellarAccount: account,
		AssetCode:      assetCode,
		Memo:           memoID,
		Dest:           dest,
		DestExtra:      r.URL.Query().Get("dest_extra"),
		StartedAt:      time.Now().UTC(),
	}
	if err := h.store.Insert(r.Context(), tx); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"account_id": h.cfg.DistributionKey,
		"memo_type":  memo.Type,
		"memo":       memoID,
		"id":         txID,
		"min_amount": 5,
		"max_amount": 50000,
		"eta":        300,
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
