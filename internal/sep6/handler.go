package sep6

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"aidmedium/stellar-anchor/internal/config"
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
	DepositAddress string     `bson:"deposit_address,omitempty" json:"deposit_address,omitempty"`
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
	json.NewEncoder(w).Encode(map[string]any{
		"deposit": map[string]any{
			"USDC": map[string]any{
				"enabled": true, "authentication_required": true,
				"min_amount": 1, "max_amount": 50000,
				"fields": map[string]any{
					"amount":        map[string]string{"description": "Amount to deposit in USDC"},
					"email_address": map[string]string{"description": "Email for receipt", "optional": "true"},
				},
			},
			"XLM": map[string]any{
				"enabled": true, "authentication_required": false,
				"min_amount": 1, "max_amount": 100000, "fields": map[string]any{},
			},
		},
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

func (h *Handler) Deposit(w http.ResponseWriter, r *http.Request) {
	account := middleware.StellarAccountFromContext(r.Context())
	if account == "" {
		writeErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	assetCode := r.URL.Query().Get("asset_code")
	if assetCode != "USDC" && assetCode != "XLM" {
		writeErr(w, http.StatusBadRequest, "unsupported asset_code; supported: USDC, XLM")
		return
	}

	txID := uuid.New().String()
	memo := fmt.Sprintf("DEP%s", txID[:8])

	tx := &Transaction{
		ID:             txID,
		Kind:           "deposit",
		Status:         "pending_user_transfer_start",
		StellarAccount: account,
		AssetCode:      assetCode,
		Memo:           memo,
		DepositAddress: h.cfg.DistributionKey,
		StartedAt:      time.Now().UTC(),
	}
	if err := h.store.Insert(r.Context(), tx); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create transaction")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"how":             fmt.Sprintf("Send %s to the address below with memo: %s", assetCode, memo),
		"id":              txID,
		"deposit_address": h.cfg.DistributionKey,
		"memo_type":       "text",
		"memo":            memo,
		"eta":             60,
		"min_amount":      1,
		"max_amount":      50000,
		"fee_fixed":       0.50,
		"fee_percent":     0.5,
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
	memo := fmt.Sprintf("WD%s", txID[:8])

	tx := &Transaction{
		ID:             txID,
		Kind:           "withdrawal",
		Status:         "pending_user_transfer_start",
		StellarAccount: account,
		AssetCode:      assetCode,
		Memo:           memo,
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
		"account_id":  h.cfg.DistributionKey,
		"memo_type":   "text",
		"memo":        memo,
		"id":          txID,
		"fee_fixed":   1.0,
		"fee_percent": 0.5,
		"min_amount":  5,
		"max_amount":  50000,
		"eta":         300,
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
